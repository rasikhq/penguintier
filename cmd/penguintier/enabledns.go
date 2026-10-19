package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/packagekit"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
	"github.com/rasikhq/penguintier/internal/tray"
)

// resolvedPackage is the package that provides systemd-resolved on
// Debian, Ubuntu and Fedora.
const resolvedPackage = "systemd-resolved"

// enableDNSTitle and enableDNSQuestion are the confirmation dialog's texts.
const (
	enableDNSTitle    = "Enable DNS support"
	enableDNSQuestion = "Install systemd-resolved, so that ZeroTier networks can set DNS for their own domains?\n\n" +
		"This switches the whole computer's DNS to systemd-resolved: /etc/resolv.conf will point to its " +
		"local resolver (127.0.0.53), and NetworkManager hands it your usual DNS servers. " +
		"Removing the systemd-resolved package switches back.\n\n" +
		"You will be asked for an administrator password."
)

// question is one program that can ask a yes/no question. Both exit 0 for
// yes and 1 for no.
type question struct {
	name string
	args []string
}

// questions are tried in order: zenity (GNOME and most GTK desktops), then
// kdialog (KDE).
var questions = []question{
	{name: "zenity", args: []string{
		"--question", "--no-markup", "--width=460",
		"--title=" + enableDNSTitle, "--text=" + enableDNSQuestion,
		"--ok-label=Install", "--cancel-label=Cancel",
	}},
	{name: "kdialog", args: []string{
		"--title", enableDNSTitle, "--yes-label", "Install", "--no-label", "Cancel",
		"--warningyesno", enableDNSQuestion,
	}},
}

// resolvedWait is how long enableDNS waits for systemd-resolved to appear
// on the bus after the install. The package starts it right away.
const resolvedWait = 15 * time.Second

// enableDNS asks the user, then installs systemd-resolved through
// PackageKit, which shows the password prompt. penguintier-dnsd notices
// systemd-resolved by itself and applies the networks' DNS settings.
//
// It returns tray.ErrCancelled when the user said no.
func enableDNS(ctx context.Context) error {
	yes, err := confirm(ctx)
	if err != nil {
		return err
	}
	if !yes {
		return tray.ErrCancelled
	}

	pk, err := packagekit.Connect()
	if err != nil {
		return fmt.Errorf("%w\nInstall it yourself instead: %s", err, installCommand())
	}
	defer pk.Close()

	already, err := pk.Install(ctx, resolvedPackage)
	switch {
	case errors.Is(err, packagekit.ErrCancelled):
		return tray.ErrCancelled
	case errors.Is(err, packagekit.ErrNotAuthorized):
		return errors.New("installing software was not authorized (the password prompt was closed, or this account may not install software)")
	case errors.Is(err, packagekit.ErrNotAvailable):
		return errors.New("PackageKit is not installed. Install systemd-resolved yourself: " + installCommand())
	case err != nil:
		return fmt.Errorf("%w\nInstall it yourself instead: %s", err, installCommand())
	case already:
		// The menu only offers this while resolved is not running.
		return errors.New("systemd-resolved is installed but not running. Start it with: sudo systemctl enable --now systemd-resolved")
	}
	return waitForResolved(ctx)
}

// confirm asks the question with the first dialog program that works. It
// returns an error when there is none.
func confirm(ctx context.Context) (bool, error) {
	for _, q := range questions {
		path, err := exec.LookPath(q.name)
		if err != nil {
			continue // not installed
		}
		err = exec.CommandContext(ctx, path, q.args...).Run() //nolint:gosec // a fixed program from the list above
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
			return false, nil
		}
		// Anything else (no display, a crash): try the next program.
	}
	return false, errors.New("no dialog program (zenity or kdialog) to ask with. Install systemd-resolved yourself: " + installCommand())
}

// installCommand is the terminal command that installs systemd-resolved
// on this distribution.
func installCommand() string {
	switch watcher.ReadOSRelease().Family() {
	case "fedora":
		return "sudo dnf install systemd-resolved"
	case "suse":
		return "sudo zypper install systemd-network"
	default:
		return "sudo apt install systemd-resolved"
	}
}

// waitForResolved waits until systemd-resolved is on the bus. The package
// starts it during the install, so this is normally immediate.
func waitForResolved(ctx context.Context) error {
	probe := &dnsstatus.Probe{}
	defer probe.Close()

	ctx, cancel := context.WithTimeout(ctx, resolvedWait)
	defer cancel()
	for {
		if up, err := probe.ResolvedRunning(ctx); err == nil && up {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("systemd-resolved was installed but did not start. Start it with: sudo systemctl enable --now systemd-resolved")
		case <-time.After(500 * time.Millisecond):
		}
	}
}
