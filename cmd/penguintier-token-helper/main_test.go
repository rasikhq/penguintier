package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

func TestRun(t *testing.T) {
	const token = "abcdefghijklmnopqrstuv12"

	tests := []struct {
		name       string
		content    *string // nil = the file does not exist
		dir        bool    // make the path a directory instead
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string // substring
	}{
		{name: "valid", content: ptr(token), wantCode: 0, wantStdout: token + "\n"},
		{name: "trailing newline", content: ptr(token + "\n"), wantCode: 0, wantStdout: token + "\n"},
		{name: "arguments", content: ptr(token), args: []string{"/etc/shadow"}, wantCode: 2, wantStderr: "takes no arguments"},
		{name: "missing file", content: nil, wantCode: 1, wantStderr: "no such file"},
		{name: "directory", dir: true, wantCode: 1, wantStderr: "not a regular file"},
		{name: "empty", content: ptr(""), wantCode: 3, wantStderr: "does not contain a valid"},
		{name: "too short", content: ptr("abc"), wantCode: 3},
		{name: "upper case", content: ptr(strings.ToUpper(token)), wantCode: 3},
		{name: "two lines", content: ptr(token + "\n" + token), wantCode: 3},
		{name: "another file's content", content: ptr("root:$6$secret:19000:0:99999:7:::\n"), wantCode: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "authtoken.secret")
			switch {
			case tc.dir:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case tc.content != nil:
				if err := os.WriteFile(path, []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var stdout, stderr bytes.Buffer
			code := run(path, tc.args, &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tc.wantCode, stderr.String())
			}
			if stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.wantStdout)
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
			// Whatever went wrong, the file's content never reaches stderr.
			if tc.content != nil && *tc.content != "" && code != 0 && strings.Contains(stderr.String(), *tc.content) {
				t.Errorf("stderr leaks the file content: %q", stderr.String())
			}
		})
	}
}

// TestSameRulesAsZtapi checks that the helper's copy of the token rules
// matches internal/ztapi (the helper repeats them to stay self-contained).
func TestSameRulesAsZtapi(t *testing.T) {
	if tokenLength != ztapi.TokenLength {
		t.Errorf("tokenLength = %d, ztapi.TokenLength = %d", tokenLength, ztapi.TokenLength)
	}
	if tokenPath != ztapi.SystemTokenPath {
		t.Errorf("tokenPath = %q, ztapi.SystemTokenPath = %q", tokenPath, ztapi.SystemTokenPath)
	}
	samples := []string{
		"abcdefghijklmnopqrstuvwx", "0123456789abcdefghijklmn", "abcdefghijklmnopqrstuvw",
		"abcdefghijklmnopqrstuvwxy", "ABCDEFGHIJKLMNOPQRSTUVWX", "abcdefghijklmnopqrstuvw!",
		"abcdefghijklmnopqrstuvwé", "", " bcdefghijklmnopqrstuvwx",
	}
	for _, s := range samples {
		if validToken(s) != ztapi.ValidToken(s) {
			t.Errorf("validToken(%q) = %v, ztapi.ValidToken = %v", s, validToken(s), ztapi.ValidToken(s))
		}
	}
}

func ptr(s string) *string { return &s }
