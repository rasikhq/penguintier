// Command penguintier-dns-helper switches the computer to systemd-resolved
// for the tray's "Enable DNS support…", safely, and undoes that switch. It
// runs as root through pkexec (polkit action
// io.github.rasikhq.Penguintier.enable-dns).
//
//	penguintier-dns-helper enable   check the setup, back up, switch, verify (undo on any failure)
//	penguintier-dns-helper undo     undo an earlier switch (finished or not)
//	penguintier-dns-helper check    only say whether this setup is supported (no root needed)
//
// It takes no other arguments and no paths: the polkit prompt can't be used
// to touch anything but the switch. The steps, checks and backup are in
// internal/dnssetup; "Enable DNS support" in docs/architecture.md describes
// them.
//
// Output: progress lines on stderr; the last line on stdout is JSON,
// {"result": "...", "message": "...", "backup": "..."}.
//
// Exit codes:
//
//	0  enabled, undone, or (check) supported
//	1  the switch failed, and everything was put back
//	2  wrong usage, or not run as root
//	3  refused: the setup is not supported, nothing was changed
//	4  failed, and putting things back failed too (the manual steps are printed)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rasikhq/penguintier/internal/dnssetup"
)

// Exit codes (see the package comment).
const (
	exitOK         = 0
	exitRolledBack = 1
	exitUsage      = 2
	exitRefused    = 3
	exitFailed     = 4
)

// safePath is the only $PATH the helper uses to find apt-get, dpkg,
// systemctl, nmcli and getent. pkexec sets a similar one; this also covers
// running the helper directly as root with an odd $PATH.
const safePath = "/usr/sbin:/usr/bin:/sbin:/bin"

const usage = "usage: pkexec " + dnssetup.HelperPath + " enable|undo|check"

func main() {
	// Stop on Ctrl+C, SIGTERM or a closed terminal. The switch then undoes
	// itself; it never stops halfway.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run does the work and returns the exit code. It is separate from main
// so tests can call it.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	if err := os.Setenv("PATH", safePath); err != nil {
		fmt.Fprintln(stderr, "penguintier-dns-helper:", err)
		return exitUsage
	}

	switch args[0] {
	case "check":
		v := dnssetup.Check(dnssetup.Gather(ctx, dnssetup.Env{}))
		if v.OK {
			writeResult(stdout, dnssetup.Result{Outcome: "supported", Message: v.Text()})
			return exitOK
		}
		writeResult(stdout, dnssetup.Result{Outcome: dnssetup.OutcomeRefused, Message: v.Text()})
		return exitRefused
	case "enable", "undo":
		// Checked here and not only by polkit: running it with sudo by
		// mistake as another user must fail clearly.
		if os.Geteuid() != 0 {
			fmt.Fprintln(stderr, "penguintier-dns-helper: must run as root; "+usage)
			return exitUsage
		}
	default:
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}

	sys := &realSystem{}
	defer sys.close()
	sw := dnssetup.Switch{
		Store:    dnssetup.Store{},
		System:   sys,
		Timeouts: dnssetup.DefaultTimeouts,
		Log:      func(line string) { fmt.Fprintln(stderr, "penguintier-dns-helper:", line) },
	}

	var res dnssetup.Result
	if args[0] == "enable" {
		res = sw.Enable(ctx, dnssetup.Gather(ctx, dnssetup.Env{}))
	} else {
		res = sw.Undo(ctx)
	}
	writeResult(stdout, res)
	return exitCode(res.Outcome)
}

// exitCode maps an outcome to the exit code (see the package comment).
func exitCode(outcome string) int {
	switch outcome {
	case dnssetup.OutcomeEnabled, dnssetup.OutcomeUndone:
		return exitOK
	case dnssetup.OutcomeRolledBack:
		return exitRolledBack
	case dnssetup.OutcomeRefused:
		return exitRefused
	default:
		return exitFailed
	}
}

// writeResult prints the result as one JSON line.
func writeResult(w io.Writer, res dnssetup.Result) {
	data, err := json.Marshal(res)
	if err != nil {
		// A struct of three strings always encodes; this can't happen.
		fmt.Fprintf(w, "{\"result\":%q}\n", res.Outcome)
		return
	}
	fmt.Fprintln(w, string(data))
}
