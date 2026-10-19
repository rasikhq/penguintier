package ztapi

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestValidToken(t *testing.T) {
	tests := map[string]bool{
		"abcdefghijklmnopqrstuvwx":  true,
		"0123456789abcdefghijklmn":  true,
		"abcdefghijklmnopqrstuvw":   false, // 23
		"abcdefghijklmnopqrstuvwxy": false, // 25
		"ABCDEFGHIJKLMNOPQRSTUVWX":  false, // upper case
		"abcdefghijklmnopqrstuvw!":  false,
		"abcdefghijklmnopqrstuvw\n": false, // ValidToken doesn't trim
		" bcdefghijklmnopqrstuvwx":  false,
		"":                          false,
		"abcdefghijklmnopqrstuvwé":  false, // non-ASCII
	}
	for s, want := range tests {
		if got := ValidToken(s); got != want {
			t.Errorf("ValidToken(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestWriteUserToken(t *testing.T) {
	home := setTestHome(t)
	const token = "abcdefghijklmnopqrstuvwx"

	path, err := WriteUserToken(token + "\n") // the helper's output has a newline
	if err != nil {
		t.Fatalf("WriteUserToken: %v", err)
	}
	if want := filepath.Join(home, ".zeroTierOneAuthToken"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	assertFile(t, path, token, 0o600)

	// FindToken now finds it, and the owner/mode check passes.
	got, gotPath, err := FindToken()
	if err != nil || got != token || gotPath != path {
		t.Errorf("FindToken() = %q, %q, %v; want the written token", got, gotPath, err)
	}
	assertOnlyFiles(t, home, ".zeroTierOneAuthToken")
}

func TestWriteUserTokenReplacesUnsafeFile(t *testing.T) {
	home := setTestHome(t)
	writeTokenFile(t, home, UserTokenFileName, "oldoldoldoldoldoldoldold", 0o644)

	path, err := WriteUserToken("newnewnewnewnewnewnewnew")
	if err != nil {
		t.Fatalf("WriteUserToken: %v", err)
	}
	assertFile(t, path, "newnewnewnewnewnewnewnew", 0o600)
}

func TestWriteUserTokenReplacesSymlinkNotTarget(t *testing.T) {
	home := setTestHome(t)
	target := writeTokenFile(t, t.TempDir(), "elsewhere", "untouched", 0o600)
	if err := os.Symlink(target, filepath.Join(home, UserTokenFileName)); err != nil {
		t.Fatal(err)
	}

	path, err := WriteUserToken("abcdefghijklmnopqrstuvwx")
	if err != nil {
		t.Fatalf("WriteUserToken: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Error("the token file is still a symlink")
	}
	assertFile(t, target, "untouched", 0o600)
}

func TestWriteUserTokenRejectsBadToken(t *testing.T) {
	home := setTestHome(t)
	for _, bad := range []string{"", "short", "ABCDEFGHIJKLMNOPQRSTUVWX", "abcdefghijkl mnopqrstuvwx"} {
		_, err := WriteUserToken(bad)
		var invalid *InvalidTokenError
		if !errors.As(err, &invalid) {
			t.Errorf("WriteUserToken(%q) error = %v, want *InvalidTokenError", bad, err)
		}
	}
	// Nothing was written, not even a temporary file.
	assertOnlyFiles(t, home)
}

// assertFile checks a file's exact content and permission bits.
func assertFile(t *testing.T, path, wantContent string, wantMode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != wantContent {
		t.Errorf("%s contains %q, want %q", path, data, wantContent)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Errorf("%s has mode %04o, want %04o", path, got, wantMode)
	}
}

// assertOnlyFiles checks that dir holds exactly the named entries, so no
// temporary files were left behind.
func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("%s contains %v, want exactly %v", dir, got, want)
	}
	for i := range want {
		if got[i] != want[i] { // ReadDir sorts by name
			t.Fatalf("%s contains %v, want exactly %v", dir, got, want)
		}
	}
}
