package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/dnssetup"
)

func TestRunUsage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root; the not-root case can't be tested")
	}
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "no arguments", args: nil, want: exitUsage},
		{name: "two arguments", args: []string{"enable", "/etc/passwd"}, want: exitUsage},
		{name: "unknown verb", args: []string{"install"}, want: exitUsage},
		{name: "enable needs root", args: []string{"enable"}, want: exitUsage},
		{name: "undo needs root", args: []string{"undo"}, want: exitUsage},
	}
	t.Setenv("PATH", os.Getenv("PATH")) // run changes $PATH; t.Setenv restores it
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), tt.args, &stdout, &stderr); code != tt.want {
				t.Errorf("exit code %d, want %d (stderr %s)", code, tt.want, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}

// TestCheckPrintsJSON: `check` works without root and ends with the JSON
// line. Whether this machine is supported doesn't matter here.
func TestCheckPrintsJSON(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv(dnssetup.EnvCheckNames, "invalid.") // never resolves, so no real lookup leaves the machine
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"check"}, &stdout, &stderr)
	var res dnssetup.Result
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if code != exitRefused || res.Outcome != dnssetup.OutcomeRefused {
		t.Errorf("exit %d, result %q; want refused (no name resolves)", code, res.Outcome)
	}
}

func TestExitCode(t *testing.T) {
	for outcome, want := range map[string]int{
		dnssetup.OutcomeEnabled: exitOK, dnssetup.OutcomeUndone: exitOK, dnssetup.OutcomeRolledBack: exitRolledBack,
		dnssetup.OutcomeRefused: exitRefused, dnssetup.OutcomeFailed: exitFailed,
	} {
		if got := exitCode(outcome); got != want {
			t.Errorf("exitCode(%q) = %d, want %d", outcome, got, want)
		}
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\n\nb\nc\nd\n", 2); got != "c / d" {
		t.Errorf("lastLines = %q", got)
	}
}

// TestLockHeld: a lock another process holds is seen; a free or missing
// lock file is not.
func TestLockHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock-frontend")
	if held, err := lockHeld(path); held || err != nil {
		t.Fatalf("missing file: held %t, err %v", held, err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := lockHeld(path); held || err != nil {
		t.Fatalf("free lock: held %t, err %v", held, err)
	}

	// fcntl locks belong to a process, and the kernel never reports a
	// process's own lock as blocking it, so another process must hold it.
	// That process is this test binary itself, started again to run only
	// TestHoldLockHelper.
	if os.Getenv("PT_HOLD_LOCK") != "" {
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHoldLockHelper")
	cmd.Env = append(os.Environ(), "PT_HOLD_LOCK="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	buf := make([]byte, 6)
	if _, err := io.ReadFull(stdout, buf); err != nil || string(buf) != "locked" {
		t.Fatalf("the helper process did not take the lock: %q %v", buf, err)
	}
	if held, err := lockHeld(path); !held || err != nil {
		t.Errorf("lock held by another process: held %t, err %v", held, err)
	}
}

// TestHoldLockHelper is not a real test: TestLockHeld starts this binary
// with PT_HOLD_LOCK set, and then it holds that lock until killed.
func TestHoldLockHelper(t *testing.T) {
	path := os.Getenv("PT_HOLD_LOCK")
	if path == "" {
		t.Skip("only run by TestLockHeld")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	lock := syscall.Flock_t{Type: syscall.F_WRLCK}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	fmt.Print("locked")
	time.Sleep(time.Minute)
}
