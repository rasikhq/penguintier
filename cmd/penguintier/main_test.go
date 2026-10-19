package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRun checks the exit code and output of the stub command line.
//
// This is a "table-driven test", the usual Go style: each row of the table
// is one case, and the loop below runs them all. t.Run gives each case its
// own name in the test output, e.g. TestRun/version.
func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring that must appear on stdout ("" = don't check)
	}{
		{name: "no args starts the tray", args: nil, wantCode: 0},
		{name: "version", args: []string{"--version"}, wantCode: 0, wantStdout: "penguintier dev"},
		{name: "single dash version", args: []string{"-version"}, wantCode: 0, wantStdout: "penguintier dev"},
		{name: "autostart", args: []string{"--autostart"}, wantCode: 0},
		{name: "help", args: []string{"--help"}, wantCode: 0},
		{name: "doctor", args: []string{"doctor"}, wantCode: 0, wantStdout: "not implemented yet"},
		{name: "doctor json", args: []string{"doctor", "--json"}, wantCode: 0, wantStdout: `{"error"`},
		{name: "deauthorize", args: []string{"deauthorize"}, wantCode: 0},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: 2},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 2},
		{name: "doctor unknown flag", args: []string{"doctor", "--bogus"}, wantCode: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// bytes.Buffer is an in-memory io.Writer, so the test can read
			// back what run printed.
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("run(%q) exit code = %d, want %d (stderr: %s)", tc.args, code, tc.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("run(%q) stdout = %q, want it to contain %q", tc.args, stdout.String(), tc.wantStdout)
			}
		})
	}
}
