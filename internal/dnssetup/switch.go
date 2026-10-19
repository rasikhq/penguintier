package dnssetup

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// System is what the switch does to the computer. The real one, in
// cmd/penguintier-dns-helper, runs apt-get, dpkg, systemctl, nmcli and
// getent as root; the tests use a fake.
type System interface {
	// InstallResolved installs systemd-resolved, and nothing else.
	InstallResolved(ctx context.Context) error
	// Purge removes packages together with their settings files.
	Purge(ctx context.Context, packages []string) error
	// ResolvedOnBus reports whether systemd-resolved owns its bus name,
	// org.freedesktop.resolve1.
	ResolvedOnBus(ctx context.Context) bool
	// RestartResolved restarts systemd-resolved.
	RestartResolved(ctx context.Context) error
	// ReloadNetworkManager runs `nmcli general reload <what>`: "dns-full"
	// sends NetworkManager's DNS servers to resolved again, "dns-rc"
	// makes it write /etc/resolv.conf again.
	ReloadNetworkManager(ctx context.Context, what string) error
	// Lookup resolves name the way programs do (getent ahosts).
	Lookup(ctx context.Context, name string) error
}

// Timeouts are how long the switch waits for each check. Poll is the time
// between two tries.
type Timeouts struct {
	Bus     time.Duration // for resolved's bus name, before and after a restart
	Lookup  time.Duration // for the first lookup through resolved
	Retry   time.Duration // for the lookup after a NetworkManager reload, and after an undo
	Install time.Duration // for apt-get / dpkg
	Poll    time.Duration
}

// DefaultTimeouts are the ones in docs/architecture.md.
var DefaultTimeouts = Timeouts{
	Bus:     10 * time.Second,
	Lookup:  20 * time.Second,
	Retry:   10 * time.Second,
	Install: 10 * time.Minute,
	Poll:    500 * time.Millisecond,
}

// The outcomes of Enable and Undo, as Result.Outcome reports them.
const (
	OutcomeEnabled    = "enabled"
	OutcomeRolledBack = "rolled-back"
	OutcomeRefused    = "refused"
	OutcomeUndone     = "undone"
	OutcomeFailed     = "failed"
)

// Result is how a switch or an undo ended. penguintier-dns-helper prints
// it as JSON for the tray.
type Result struct {
	Outcome string `json:"result"`
	Message string `json:"message"`
	Backup  string `json:"backup,omitempty"`
}

// Switch runs Enable and Undo.
type Switch struct {
	Store    Store
	System   System
	Timeouts Timeouts
	// Log gets one line per step, for the person or program watching.
	// nil means no log.
	Log func(line string)
}

// Enable switches the computer to systemd-resolved, if Check accepts
// facts (which the caller just gathered). Every failure after the backup
// is undone before Enable returns, and the Result says what happened.
//
// When ctx is cancelled (SIGTERM, say), Enable stops waiting and undoes
// the switch. A running apt-get is not stopped halfway, though: killing
// the package manager in the middle could leave packages half installed.
func (s Switch) Enable(ctx context.Context, facts Facts) Result {
	if v := Check(facts); !v.OK {
		return Result{Outcome: OutcomeRefused, Message: v.Text()}
	}

	s.log("Backing up " + fileList() + " to " + BackupDir)
	m, err := s.Store.Backup(facts.LookupName)
	if err != nil {
		return Result{Outcome: OutcomeRefused, Message: "Could not make the backup, so nothing was changed: " + err.Error()}
	}
	m.Installed = []string{PackageResolved}
	if err := s.Store.Save(m); err != nil {
		return s.rollback(ctx, m, "could not save the backup's manifest: "+err.Error())
	}

	s.log("Installing systemd-resolved")
	// context.WithoutCancel keeps ctx's values but not its cancellation:
	// apt-get runs to the end even if we are being stopped. Its own
	// timeout still applies.
	installCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Timeouts.Install)
	err = s.System.InstallResolved(installCtx)
	cancel()
	if err != nil {
		return s.rollback(ctx, m, "installing systemd-resolved failed: "+err.Error())
	}
	if ctx.Err() != nil {
		return s.rollback(ctx, m, "the switch was interrupted")
	}

	s.log("Waiting for systemd-resolved on the system bus")
	onBus := func() bool { return s.System.ResolvedOnBus(ctx) }
	if !s.waitFor(ctx, s.Timeouts.Bus, onBus) {
		// A stop request leads to the undo, not to the next remedy.
		if ctx.Err() != nil {
			return s.rollback(ctx, m, "the switch was interrupted")
		}
		// The Debian race (see the package comment): resolved started
		// before D-Bus knew its user. A restart fixes it.
		s.log("systemd-resolved runs without its bus name; restarting it")
		if err := s.System.RestartResolved(ctx); err != nil {
			return s.rollback(ctx, m, "restarting systemd-resolved failed: "+err.Error())
		}
		if !s.waitFor(ctx, s.Timeouts.Bus, onBus) {
			return s.rollback(ctx, m, interrupted(ctx, "systemd-resolved did not appear on the system bus"))
		}
	}

	s.log("Checking that " + m.CheckName + " still resolves")
	resolves := func() bool { return s.System.Lookup(ctx, m.CheckName) == nil }
	if !s.waitFor(ctx, s.Timeouts.Lookup, resolves) {
		if ctx.Err() != nil {
			return s.rollback(ctx, m, "the switch was interrupted")
		}
		s.log("Names don't resolve yet; asking NetworkManager to send its DNS servers again")
		if err := s.System.ReloadNetworkManager(ctx, "dns-full"); err != nil {
			s.log("nmcli failed: " + err.Error())
		}
		if !s.waitFor(ctx, s.Timeouts.Retry, resolves) {
			return s.rollback(ctx, m, interrupted(ctx, "names did not resolve through systemd-resolved"))
		}
	}

	m.State = StateEnabled
	m.Message = "systemd-resolved runs and names resolve."
	if err := s.Store.Save(m); err != nil {
		// The switch worked; only the record of it failed. Say so, but
		// don't undo a working setup for it.
		s.log("could not update the manifest: " + err.Error())
	}
	return Result{
		Outcome: OutcomeEnabled,
		Message: "DNS support is on: systemd-resolved runs and names resolve. Your previous files are kept in " +
			BackupDir + "; to switch back: pkexec " + HelperPath + " undo",
		Backup: BackupDir,
	}
}

// Undo removes what an earlier switch installed and puts the backed-up
// files back: for a switch that worked (StateEnabled) or one that never
// finished (StateInProgress, StateUndoFailed).
func (s Switch) Undo(ctx context.Context) Result {
	m, err := s.Store.Load()
	if err != nil {
		return Result{Outcome: OutcomeFailed, Message: err.Error()}
	}
	if m == nil || m.State == StateRolledBack || m.State == StateUndone {
		return Result{Outcome: OutcomeRefused, Message: "There is no DNS switch to undo."}
	}
	s.log("Undoing the switch to systemd-resolved")
	if err := s.undo(ctx, m); err != nil {
		m.State = StateUndoFailed
		m.Message = err.Error()
		_ = s.Store.Save(m) // the result below carries the error either way
		return Result{Outcome: OutcomeFailed, Message: "Undoing failed: " + err.Error() + ". " + manualSteps(), Backup: BackupDir}
	}
	m.State = StateUndone
	m.Message = "systemd-resolved was removed and the previous files were restored."
	_ = s.Store.Save(m) // the undo worked; the record is a convenience
	return Result{Outcome: OutcomeUndone, Message: "systemd-resolved was removed, your previous DNS files are back, and names resolve.", Backup: BackupDir}
}

// rollback undoes a switch that failed for reason, and returns the
// Result to report.
func (s Switch) rollback(ctx context.Context, m *Manifest, reason string) Result {
	s.log("Undoing the switch: " + reason)
	if err := s.undo(ctx, m); err != nil {
		m.State = StateUndoFailed
		m.Message = reason + "; undoing failed: " + err.Error()
		_ = s.Store.Save(m) // the result below carries the error either way
		return Result{
			Outcome: OutcomeFailed,
			Message: "DNS support was not enabled: " + reason + ". Putting things back also failed: " + err.Error() + ". " + manualSteps(),
			Backup:  BackupDir,
		}
	}
	m.State = StateRolledBack
	m.Message = reason
	_ = s.Store.Save(m) // the rollback worked; the record is a convenience
	return Result{
		Outcome: OutcomeRolledBack,
		Message: "DNS support was not enabled: " + reason + ". Everything was put back as it was, and names resolve again.",
		Backup:  BackupDir,
	}
}

// undo removes the installed packages, restores the files, lets
// NetworkManager write /etc/resolv.conf again, and checks that names
// resolve. It runs even when ctx was cancelled: stopping halfway would be
// the worst outcome.
func (s Switch) undo(ctx context.Context, m *Manifest) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Timeouts.Install)
	defer cancel()

	var errs []error
	if len(m.Installed) > 0 {
		s.log("Removing systemd-resolved")
		if err := s.System.Purge(ctx, m.Installed); err != nil {
			errs = append(errs, fmt.Errorf("removing %v: %w", m.Installed, err))
		}
	}
	// Restore after the removal: the package's removal script rewrites
	// /etc/resolv.conf, and the backup must win.
	s.log("Restoring " + fileList())
	if err := s.Store.Restore(m); err != nil {
		errs = append(errs, err)
	}
	if err := s.System.ReloadNetworkManager(ctx, "dns-rc"); err != nil {
		s.log("nmcli failed: " + err.Error()) // the restored file is NetworkManager's own, so this is not fatal
	}
	if m.CheckName != "" {
		s.log("Checking that " + m.CheckName + " resolves again")
		if !s.waitFor(ctx, s.Timeouts.Retry, func() bool { return s.System.Lookup(ctx, m.CheckName) == nil }) {
			errs = append(errs, errors.New(m.CheckName+" still does not resolve"))
		}
	}
	return errors.Join(errs...)
}

// waitFor calls cond every Poll until it returns true, timeout has passed,
// or ctx is cancelled. It reports whether cond became true (never after
// ctx was cancelled).
func (s Switch) waitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return false
		}
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(s.Timeouts.Poll):
		}
	}
}

func (s Switch) log(line string) {
	if s.Log != nil {
		s.Log(line)
	}
}

// interrupted says the switch was stopped, if it was, instead of reason.
func interrupted(ctx context.Context, reason string) string {
	if ctx.Err() != nil {
		return "the switch was interrupted"
	}
	return reason
}

// fileList is AffectedFiles for messages: "/etc/resolv.conf,
// /etc/nsswitch.conf, /etc/systemd/resolved.conf".
func fileList() string {
	list := ""
	for i, f := range AffectedFiles {
		if i > 0 {
			list += ", "
		}
		list += f
	}
	return list
}

// manualSteps is how to finish an undo by hand.
func manualSteps() string {
	return "To finish by hand: sudo dpkg --purge systemd-resolved; then copy the files from " + BackupDir +
		" back (for example: sudo cp " + BackupDir + "/resolv.conf /etc/resolv.conf); then: sudo nmcli general reload dns-rc"
}
