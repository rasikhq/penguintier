package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/doctor"
	"github.com/rasikhq/penguintier/internal/platform/autostart"
	"github.com/rasikhq/penguintier/internal/tray"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// TestRun checks the exit code and output of the command line.
//
// This is a "table-driven test", the usual Go style: each row of the table
// is one case, and the loop below runs them all. t.Run gives each case its
// own name in the test output, e.g. TestRun/version.
func TestRun(t *testing.T) {
	// The real tray runs until Quit; a stand-in records how it was started.
	var startedWithAutostart []bool
	startTray = func(autostartRun bool, _ io.Writer) int {
		startedWithAutostart = append(startedWithAutostart, autostartRun)
		return 0
	}
	// The doctor reads made-up facts, never the real machine.
	gatherFacts = func(context.Context) doctor.Facts { return doctor.Facts{Version: "dev"} }

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring that must appear on stdout ("" = don't check)
	}{
		{name: "no args starts the tray", args: nil, wantCode: 0},
		{name: "version", args: []string{"--version"}, wantCode: 0, wantStdout: "penguintier dev\n"},
		{name: "single dash version", args: []string{"-version"}, wantCode: 0, wantStdout: "penguintier dev\n"},
		{name: "autostart", args: []string{"--autostart"}, wantCode: 0},
		{name: "help", args: []string{"--help"}, wantCode: 0},
		// These facts have no ZeroTier at all, so the doctor fails.
		{name: "doctor", args: []string{"doctor"}, wantCode: 1, wantStdout: "Penguintier doctor"},
		{name: "doctor json", args: []string{"doctor", "--json"}, wantCode: 1, wantStdout: `"schemaVersion": 1`},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: 2},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 2},
		{name: "doctor unknown flag", args: []string{"doctor", "--bogus"}, wantCode: 2},
		{name: "doctor extra argument", args: []string{"doctor", "now"}, wantCode: 2},
		{name: "deauthorize unknown flag", args: []string{"deauthorize", "--bogus"}, wantCode: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// bytes.Buffer is an in-memory io.Writer, so the test can read
			// back what run printed.
			var stdout, stderr bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("run(%q) exit code = %d, want %d (stderr: %s)", tc.args, code, tc.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("run(%q) stdout = %q, want it to contain %q", tc.args, stdout.String(), tc.wantStdout)
			}
		})
	}
	// Two rows start the tray: "no args" and "autostart".
	if len(startedWithAutostart) != 2 || startedWithAutostart[0] || !startedWithAutostart[1] {
		t.Errorf("tray started with autostart = %v, want [false true]", startedWithAutostart)
	}
}

// TestDoctorJSONIsValid: --json prints JSON a script can read, and the
// exit code follows the verdict.
func TestDoctorJSONIsValid(t *testing.T) {
	gatherFacts = func(context.Context) doctor.Facts { return doctor.Facts{Version: "v1.2.3"} }
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--json"}, strings.NewReader(""), &stdout, &stderr)
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout.String())
	}
	if code != 1 || report.OK {
		t.Errorf("exit code %d, ok %t; want 1, false", code, report.OK)
	}
	if report.Checks[0].Summary != "v1.2.3" {
		t.Errorf("first check = %+v, want the version", report.Checks[0])
	}
}

func TestDeauthorize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	token := filepath.Join(home, ztapi.UserTokenFileName)
	writeToken := func() {
		t.Helper()
		if err := os.WriteFile(token, []byte("abcdefghijklmnopqrstuvwx"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name      string
		token     bool // a token copy exists
		args      []string
		input     string
		wantCode  int
		wantGone  bool
		wantInOut string
	}{
		{name: "nothing to delete", args: nil, wantCode: 0, wantGone: true, wantInOut: "nothing to do"},
		{name: "answer yes", token: true, input: "y\n", wantCode: 0, wantGone: true, wantInOut: "Deleted"},
		{name: "answer YES", token: true, input: "YES\n", wantCode: 0, wantGone: true},
		{name: "answer no", token: true, input: "n\n", wantCode: 1, wantGone: false, wantInOut: "Nothing deleted"},
		{name: "no answer (EOF)", token: true, input: "", wantCode: 1, wantGone: false},
		{name: "--yes", token: true, args: []string{"--yes"}, wantCode: 0, wantGone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(token) // start clean; it may not exist
			if tt.token {
				writeToken()
			}
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"deauthorize"}, tt.args...), strings.NewReader(tt.input), &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d (stderr %s)", code, tt.wantCode, stderr.String())
			}
			_, err := os.Stat(token)
			if gone := errors.Is(err, os.ErrNotExist); gone != tt.wantGone {
				t.Errorf("token gone = %t, want %t", gone, tt.wantGone)
			}
			if !strings.Contains(stdout.String(), tt.wantInOut) {
				t.Errorf("stdout %q lacks %q", stdout.String(), tt.wantInOut)
			}
		})
	}
}

func TestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "penguintier", "state.json")

	// No file: a fresh state.
	s, err := loadState(path)
	if err != nil || s.FirstRunDone || s.DesktopUIImported || s.StartAtLoginDecided {
		t.Fatalf("fresh state = %+v, %v", s, err)
	}

	s.FirstRunDone, s.StartAtLoginDecided = true, true
	if err := saveState(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(path)
	if err != nil || got != s {
		t.Errorf("loaded %+v, %v; want %+v", got, err, s)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode %v, %v; want 0600", info.Mode().Perm(), err)
	}

	// A damaged file: a fresh state and an error to log.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := loadState(path); err == nil || s.FirstRunDone {
		t.Errorf("damaged file: %+v, %v; want a fresh state and an error", s, err)
	}
}

// TestHooksStartAtLogin: Start at Login turns on once after the first
// successful run, and never against the user's choice.
func TestHooksStartAtLogin(t *testing.T) {
	newHooks := func(t *testing.T) *desktopHooks {
		dir := t.TempDir()
		return &desktopHooks{
			state:            appState{Version: stateVersion},
			statePath:        filepath.Join(dir, "state.json"),
			autostartPath:    filepath.Join(dir, "autostart", autostart.FileName),
			autoStartAtLogin: true,
		}
	}
	saved := func(t *testing.T, h *desktopHooks) appState {
		t.Helper()
		s, err := loadState(h.statePath)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("first success turns it on", func(t *testing.T) {
		h := newHooks(t)
		h.Connected()
		if !h.StartAtLogin() || !saved(t, h).StartAtLoginDecided {
			t.Fatal("Connected did not turn on Start at Login")
		}
		// The user turns it off; the next run's Connected leaves it off.
		if err := h.SetStartAtLogin(false); err != nil {
			t.Fatal(err)
		}
		h.Connected()
		if h.StartAtLogin() {
			t.Error("Connected turned Start at Login back on")
		}
	})

	t.Run("the user's choice before the first success wins", func(t *testing.T) {
		h := newHooks(t)
		if err := h.SetStartAtLogin(true); err != nil {
			t.Fatal(err)
		}
		if err := h.SetStartAtLogin(false); err != nil {
			t.Fatal(err)
		}
		h.Connected()
		if h.StartAtLogin() {
			t.Error("Connected overrode the user's choice")
		}
	})

	t.Run("development builds leave it alone", func(t *testing.T) {
		h := newHooks(t)
		h.autoStartAtLogin = false
		h.Connected()
		if h.StartAtLogin() || saved(t, h).StartAtLoginDecided {
			t.Error("a development build turned on Start at Login")
		}
	})
}

// writeScript writes an executable shell script.
func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestAuthorize runs authorize with a fake pkexec and a fake token helper.
func TestAuthorize(t *testing.T) {
	bin, libexec, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(tray.EnvLibexecDir, libexec)
	// Only our fake pkexec is on PATH (plus the shell itself).
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	writeScript(t, libexec, tokenHelper, `echo abcdefghijklmnopqrstuvwx`)
	// The fake pkexec exits with $FAKE_PKEXEC_EXIT, or runs the helper.
	writeScript(t, bin, "pkexec", `if [ -n "$FAKE_PKEXEC_EXIT" ]; then echo "fake pkexec message" >&2; exit "$FAKE_PKEXEC_EXIT"; fi; exec "$@"`)
	token := filepath.Join(home, ztapi.UserTokenFileName)

	tests := []struct {
		exit      string // "" = pkexec runs the helper
		wantErr   error  // for errors.Is
		wantInErr string
	}{
		{exit: ""},
		{exit: "126", wantErr: tray.ErrCancelled},
		{exit: "127", wantInErr: "penguintier doctor"},
		{exit: "1", wantInErr: "fake pkexec message"},
		{exit: "3", wantInErr: "no valid token"},
		{exit: "9", wantInErr: "exit code 9"},
	}
	for _, tt := range tests {
		t.Run("exit "+tt.exit, func(t *testing.T) {
			_ = os.Remove(token) // start clean; it may not exist
			t.Setenv("FAKE_PKEXEC_EXIT", tt.exit)
			err := authorize(t.Context())

			if tt.exit == "" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(token)
				if err != nil || string(data) != "abcdefghijklmnopqrstuvwx" {
					t.Errorf("token file = %q, %v", data, err)
				}
				if info, err := os.Stat(token); err != nil || info.Mode().Perm() != 0o600 {
					t.Errorf("token file mode %v, want 0600", info.Mode().Perm())
				}
				return
			}
			if err == nil {
				t.Fatal("no error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantInErr)
			}
			if _, err := os.Stat(token); err == nil {
				t.Error("a token file was written after a failure")
			}
		})
	}

	t.Run("no pkexec", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if err := authorize(t.Context()); err == nil || !strings.Contains(err.Error(), "pkexec is not installed") {
			t.Errorf("err = %v", err)
		}
	})
}

// TestConfirm: zenity's answer decides, kdialog is the fallback, and
// without either the user gets the manual command.
func TestConfirm(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")

	writeScript(t, bin, "zenity", "exit 0")
	if yes, err := confirm(t.Context()); !yes || err != nil {
		t.Errorf("zenity OK: %t, %v", yes, err)
	}
	writeScript(t, bin, "zenity", "exit 1")
	if yes, err := confirm(t.Context()); yes || err != nil {
		t.Errorf("zenity Cancel: %t, %v", yes, err)
	}
	// zenity can't open a window (exit 255): kdialog asks instead.
	writeScript(t, bin, "zenity", "exit 255")
	writeScript(t, bin, "kdialog", "exit 0")
	if yes, err := confirm(t.Context()); !yes || err != nil {
		t.Errorf("kdialog fallback: %t, %v", yes, err)
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := confirm(t.Context()); err == nil || !strings.Contains(err.Error(), "systemd-resolved") {
		t.Errorf("no dialog program: %v", err)
	}
}
