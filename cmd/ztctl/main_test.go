package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// useMock starts a fake service and points ztctl at it through the same
// environment variables a developer would set.
func useMock(t *testing.T, scenario string) *ztmock.Server {
	t.Helper()
	srv, err := ztmock.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(ztmock.Token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ztapi.EnvAPIURL, ts.URL)
	t.Setenv(ztapi.EnvTokenFile, tokenFile)
	return srv
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		scenario   string
		args       []string
		wantCode   int
		wantStdout []string // substrings
		wantStderr []string // substrings
	}{
		{name: "no args", args: nil, wantCode: 2, wantStderr: []string{"usage:"}},
		{name: "help", args: []string{"--help"}, wantCode: 0, wantStdout: []string{"usage:", "allowDNS"}},
		{name: "unknown command", args: []string{"frobnicate"}, wantCode: 2, wantStderr: []string{`unknown command "frobnicate"`}},
		{name: "status extra arg", args: []string{"status", "x"}, wantCode: 2},
		{name: "join no id", args: []string{"join"}, wantCode: 2},
		{name: "join bad id", args: []string{"join", "xyz"}, wantCode: 2, wantStderr: []string{"invalid network ID"}},
		{name: "set no flags", args: []string{"set", "c0ffee1234000001"}, wantCode: 2},
		{name: "set bad flag", args: []string{"set", "c0ffee1234000001", "allowAll=true"}, wantCode: 2, wantStderr: []string{"unknown flag"}},
		{name: "set bad value", args: []string{"set", "c0ffee1234000001", "allowDNS=maybe"}, wantCode: 2},
		{name: "set no equals", args: []string{"set", "c0ffee1234000001", "allowDNS"}, wantCode: 2},

		{
			name: "status", scenario: ztmock.Online, args: []string{"status"}, wantCode: 0,
			wantStdout: []string{"address:       a1b2c3d4e5", "online:        true", "version:       1.16.2"},
		},
		{
			name: "list", scenario: ztmock.Online, args: []string{"list"}, wantCode: 0,
			wantStdout: []string{"ID", "c0ffee1234000001", "home-lab", "10.147.17.5/24", "allowDNS=true", "office"},
		},
		{
			name: "join", scenario: ztmock.Online, args: []string{"join", "0123456789ABCDEF"}, wantCode: 0,
			wantStdout: []string{"joined 0123456789abcdef (status REQUESTING_CONFIGURATION)"},
		},
		{
			name: "leave", scenario: ztmock.Online, args: []string{"leave", "c0ffee1234000002"}, wantCode: 0,
			wantStdout: []string{"left c0ffee1234000002"},
		},
		{
			// The service answers 200 for this too.
			name: "leave not joined", scenario: ztmock.Online, args: []string{"leave", "0123456789abcdef"}, wantCode: 0,
			wantStdout: []string{"left 0123456789abcdef"},
		},
		{
			name: "set", scenario: ztmock.Online, args: []string{"set", "c0ffee1234000002", "allowDNS=true", "allowGlobal=1"}, wantCode: 0,
			wantStdout: []string{"allowManaged=true allowGlobal=true allowDefault=false allowDNS=true"},
		},
		{
			name: "unauthorized", scenario: ztmock.Unauthorized, args: []string{"status"}, wantCode: 1,
			wantStderr: []string{"rejected the API token", "hint:"},
		},
		{
			name: "service down", scenario: ztmock.ServiceDown, args: []string{"list"}, wantCode: 1,
			wantStderr: []string{"not running", "systemctl start zerotier-one"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.scenario != "" {
				useMock(t, tc.scenario)
			}
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout does not contain %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr does not contain %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestRunNoToken(t *testing.T) {
	useMock(t, ztmock.Online)
	t.Setenv(ztapi.EnvTokenFile, "")
	t.Setenv("HOME", t.TempDir()) // no token files in here
	var stdout, stderr bytes.Buffer
	code := run([]string{"list"}, &stdout, &stderr)
	// The system token may be readable if the tests run as root; then the
	// mock rejects it instead. Either way it fails.
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if os.Geteuid() != 0 && !strings.Contains(stderr.String(), "pkexec") {
		t.Errorf("stderr has no hint on copying the token:\n%s", stderr.String())
	}
}

func TestRunEmptyList(t *testing.T) {
	useMock(t, ztmock.Online)
	for _, id := range []string{"c0ffee1234000001", "c0ffee1234000002"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"leave", id}, &out, &errOut); code != 0 {
			t.Fatalf("leave %s: exit %d: %s", id, code, errOut.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list: exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "(no networks joined)") {
		t.Errorf("stdout = %q", stdout.String())
	}
}
