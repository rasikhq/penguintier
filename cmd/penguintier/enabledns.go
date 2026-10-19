package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rasikhq/penguintier/internal/dnssetup"
	"github.com/rasikhq/penguintier/internal/tray"
)

// "Enable DNS support…" switches the computer to systemd-resolved. That
// changes how the whole computer looks up names, so (see "Enable DNS
// support" in docs/architecture.md):
//
//  1. The tray first checks, without any privileges, that this is a setup
//     Penguintier supports. If not, it explains why and changes nothing.
//  2. It asks the user, listing what will change and what is backed up.
//  3. penguintier-dns-helper does the switch as root (one password
//     prompt): back up, install, check, and undo everything by itself if
//     names stop resolving.

// dnsHelper is the root helper that does the switch.
const dnsHelper = "penguintier-dns-helper"

const enableDNSTitle = "Enable DNS support"

// gatherDNSFacts reads the facts for the supported-setup check. A
// variable so tests can give made-up facts.
var gatherDNSFacts = func(ctx context.Context) dnssetup.Facts {
	return dnssetup.Gather(ctx, dnssetup.Env{})
}

// enableDNS is "Enable DNS support…". It returns tray.ErrCancelled when
// the user said no or a dialog already said everything.
func enableDNS(ctx context.Context) error {
	facts := gatherDNSFacts(ctx)
	if v := dnssetup.Check(facts); !v.OK {
		if showInfo(ctx, v.Text()) {
			return tray.ErrCancelled // the dialog explained it
		}
		return errors.New(v.Text())
	}

	yes, err := confirm(ctx, confirmText(facts.LookupName))
	if err != nil {
		return err
	}
	if !yes {
		return tray.ErrCancelled
	}

	helper, err := tray.FindHelper(dnsHelper)
	if err != nil {
		return fmt.Errorf("%w. Nothing was changed", err)
	}
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return errors.New("pkexec is not installed, so Penguintier can't ask for the password. Nothing was changed")
	}

	// The helper runs until the switch is done or undone (up to a few
	// minutes). If the tray quits meanwhile, the helper carries on: it
	// runs as root, and stopping it halfway is what it must never do.
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, pkexec, helper, "enable") //nolint:gosec // pkexec and our own helper, found in fixed places
	cmd.Stdout = &stdout
	err = cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		return fmt.Errorf("run pkexec: %w", err)
	}
	return helperOutcome(code, stdout.String())
}

// helperOutcome turns the helper's exit code and output into the tray's
// answer: nil when DNS support is on, ErrCancelled when the password
// prompt was dismissed, otherwise an error whose text tells the user what
// happened (the helper's own message when it has one).
func helperOutcome(code int, stdout string) error {
	res, ok := lastResult(stdout)
	switch {
	case code == 0 && ok && res.Outcome == dnssetup.OutcomeEnabled:
		return nil
	case code == pkexecDismissed:
		return tray.ErrCancelled
	case code == pkexecNotAuthorized:
		return errors.New("not allowed to switch DNS (wrong password, or no polkit agent is running). Nothing was changed")
	case ok && res.Message != "":
		return errors.New(res.Message)
	default:
		return fmt.Errorf("the DNS switch stopped unexpectedly (exit code %d). Check with “penguintier doctor”; to undo it: pkexec %s undo",
			code, dnssetup.HelperPath)
	}
}

// lastResult reads the JSON line the helper prints last.
func lastResult(stdout string) (dnssetup.Result, bool) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var res dnssetup.Result
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Outcome == "" {
		return dnssetup.Result{}, false
	}
	return res, true
}

// confirmText is the question, with what the switch changes and how it
// protects the user.
func confirmText(checkName string) string {
	return "Switch this computer's DNS to systemd-resolved, so ZeroTier networks can set DNS for their own domains?\n\n" +
		"This changes how the whole computer looks up names. Penguintier will:\n" +
		"1. Back up /etc/resolv.conf and /etc/nsswitch.conf to " + dnssetup.BackupDir + ".\n" +
		"2. Install systemd-resolved (nothing else).\n" +
		"3. Check that " + checkName + " still resolves.\n" +
		"4. If it doesn't, undo everything by itself and put the backed-up files back.\n\n" +
		"To switch back later: pkexec " + dnssetup.HelperPath + " undo\n" +
		"If you edited network settings files by hand, keep your own copy of them as well.\n\n" +
		"You will be asked for an administrator password."
}

// question is one program that can ask a yes/no question. Both exit 0 for
// yes and 1 for no.
type question struct {
	name string
	args func(text string) []string
}

// questions are tried in order: zenity (GNOME and most GTK desktops), then
// kdialog (KDE).
var questions = []question{
	{name: "zenity", args: func(text string) []string {
		return []string{"--question", "--no-markup", "--width=520", "--title=" + enableDNSTitle, "--text=" + text,
			"--ok-label=Switch", "--cancel-label=Cancel"}
	}},
	{name: "kdialog", args: func(text string) []string {
		return []string{"--title", enableDNSTitle, "--yes-label", "Switch", "--no-label", "Cancel", "--warningyesno", text}
	}},
}

// infos show a message with one OK button.
var infos = []question{
	{name: "zenity", args: func(text string) []string {
		return []string{"--warning", "--no-markup", "--width=520", "--title=" + enableDNSTitle, "--text=" + text}
	}},
	{name: "kdialog", args: func(text string) []string {
		return []string{"--title", enableDNSTitle, "--sorry", text}
	}},
}

// confirm asks the question with the first dialog program that works. It
// returns an error when there is none: without a clear yes, nothing may
// change.
func confirm(ctx context.Context, text string) (bool, error) {
	for _, q := range questions {
		code, ok := runDialog(ctx, q, text)
		switch {
		case ok && code == 0:
			return true, nil
		case ok && code == 1:
			return false, nil
		}
		// Not installed, no display, a crash: try the next program.
	}
	return false, errors.New("no dialog program (zenity or kdialog) to ask with, so nothing was changed. " +
		"To switch by hand, see “penguintier doctor”")
}

// showInfo shows text in a dialog and reports whether one was shown.
func showInfo(ctx context.Context, text string) bool {
	for _, q := range infos {
		if code, ok := runDialog(ctx, q, text); ok && (code == 0 || code == 1) {
			return true
		}
	}
	return false
}

// runDialog runs one dialog program and returns its exit code. ok is false
// when it is not installed or could not run at all.
func runDialog(ctx context.Context, q question, text string) (code int, ok bool) {
	path, err := exec.LookPath(q.name)
	if err != nil {
		return 0, false
	}
	err = exec.CommandContext(ctx, path, q.args(text)...).Run() //nolint:gosec // a fixed program from the lists above
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, true
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), true
	default:
		return 0, false
	}
}
