package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Started from a terminal, the tray would keep the terminal busy until it
// quits, and Ctrl+C or closing the terminal would quit it too. So in that
// case penguintier starts a copy of itself in the background, the way the
// app grid would, and returns at once. --foreground keeps the old way
// (handy for watching the log), and so do development builds: `go run`
// deletes its binary when the first process exits.

// logIdentifier tags the background tray's log lines in the journal:
// journalctl --user -t penguintier
const logIdentifier = "penguintier"

// inBackground reports whether this start should move to the background.
func inBackground(autostartRun, foreground bool) bool {
	return !autostartRun && !foreground && version != "dev" && startedFromTerminal()
}

// startedFromTerminal reports whether stdin and stderr are a terminal. A
// variable so tests can decide.
var startedFromTerminal = func() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stderr)
}

// isTerminal reports whether f is a terminal: a character device other
// than /dev/null (the app grid and autostart connect stdin to /dev/null,
// which is a character device too).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, null)
}

// startInBackground starts `penguintier --foreground` detached from this
// terminal, with its log in the systemd journal, and returns the exit
// code for this process. A variable so tests can replace it.
var startInBackground = func(stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "penguintier:", err)
		return 1
	}
	args := []string{exe, "--foreground"}
	logHint := "Watch its log: journalctl --user -t " + logIdentifier + " -f"
	// systemd-cat runs a program with its output going to the journal.
	if cat, err := exec.LookPath("systemd-cat"); err == nil {
		args = append([]string{cat, "--identifier=" + logIdentifier, "--"}, args...)
	} else {
		logHint = "Its log is not kept. Run “penguintier --foreground” to see it."
	}

	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // our own binary, maybe through systemd-cat
	// nil Stdin, Stdout and Stderr mean /dev/null: nothing ties the copy
	// to this terminal.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// Setsid puts the copy in a new session without a terminal, so neither
	// Ctrl+C nor closing the terminal reaches it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, "penguintier: start in the background:", err)
		return 1
	}
	// Release lets the copy run on; we won't wait for it.
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintln(stderr, "penguintier:", err)
	}
	fmt.Fprintln(stdout, "Penguintier is starting in the system tray. Quit it from its menu.")
	fmt.Fprintln(stdout, logHint)
	fmt.Fprintln(stdout, "To keep it in this terminal instead: penguintier --foreground")
	return 0
}
