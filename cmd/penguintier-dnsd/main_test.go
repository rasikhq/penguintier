package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

func TestRunArgs(t *testing.T) {
	tests := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{args: []string{"--version"}, wantCode: 0, wantStdout: "penguintier-dnsd dev\n"},
		{args: []string{"--help"}, wantCode: 0, wantStdout: "usage: penguintier-dnsd"},
		{args: []string{"bogus"}, wantCode: 2, wantStderr: "usage: penguintier-dnsd"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

// TestRunWithoutToken checks that the daemon refuses to start without a
// token, with exit code 1 (so systemd reports a failure).
func TestRunWithoutToken(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	t.Setenv(ztapi.EnvTokenFile, "")
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no API token") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
