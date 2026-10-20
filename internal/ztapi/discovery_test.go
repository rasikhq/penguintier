package ztapi

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// setTestHome gives the test an empty temporary $HOME and clears every
// setting discovery reads, so the developer's real files can't leak into
// the test. t.Setenv puts the old values back when the test ends.
func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(EnvTokenFile, "")
	t.Setenv(EnvAPIURL, "")
	// Paths that don't exist, unless a test creates them.
	setSystemPaths(t, filepath.Join(home, "system-authtoken.secret"), filepath.Join(home, "system-zerotier-one.port"))
	return home
}

// setSystemPaths points the system token and port files somewhere else for
// one test.
func setSystemPaths(t *testing.T, tokenPath, portPath string) {
	t.Helper()
	oldToken, oldPort := systemTokenPath, systemPortPath
	systemTokenPath, systemPortPath = tokenPath, portPath
	t.Cleanup(func() { systemTokenPath, systemPortPath = oldToken, oldPort })
}

// writeTokenFile writes content to dir/name with the given mode and returns
// the path. Chmod after writing makes the mode exact, whatever the umask.
func writeTokenFile(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindToken(t *testing.T) {
	const (
		tokenA = "aaaaaaaaaaaaaaaaaaaaaaaa"
		tokenB = "bbbbbbbbbbbbbbbbbbbbbbbb"
		tokenC = "cccccccccccccccccccccccc"
	)

	// setup creates files in the temporary home (and elsewhere) and returns
	// the path FindToken should report ("" if it should fail).
	tests := []struct {
		name      string
		setup     func(t *testing.T, home string) string
		wantToken string
		wantErr   func(err error) bool
	}{
		{
			name:    "nothing found",
			setup:   func(_ *testing.T, _ string) string { return "" },
			wantErr: func(err error) bool { return errors.Is(err, ErrTokenMissing) },
		},
		{
			name: "env file first",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, tokenB, 0o600)
				p := writeTokenFile(t, t.TempDir(), "my-token", tokenA, 0o600)
				t.Setenv(EnvTokenFile, p)
				return p
			},
			wantToken: tokenA,
		},
		{
			name: "env file missing is an error, not a fallback",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, tokenB, 0o600)
				t.Setenv(EnvTokenFile, filepath.Join(home, "does-not-exist"))
				return ""
			},
			wantErr: func(err error) bool { return errors.Is(err, fs.ErrNotExist) },
		},
		{
			name: "zeroTierOneAuthToken",
			setup: func(t *testing.T, home string) string {
				return writeTokenFile(t, home, UserTokenFileName, tokenA, 0o600)
			},
			wantToken: tokenA,
		},
		{
			name: "zeroTierOneAuthToken wins over zerotier-local-auth",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, localAuthFileName, tokenB, 0o600)
				return writeTokenFile(t, home, UserTokenFileName, tokenA, 0o600)
			},
			wantToken: tokenA,
		},
		{
			name: "zerotier-local-auth",
			setup: func(t *testing.T, home string) string {
				return writeTokenFile(t, home, localAuthFileName, tokenB, 0o600)
			},
			wantToken: tokenB,
		},
		{
			name: "trailing newline and spaces are trimmed",
			setup: func(t *testing.T, home string) string {
				return writeTokenFile(t, home, UserTokenFileName, "  "+tokenA+"\n", 0o600)
			},
			wantToken: tokenA,
		},
		{
			name: "read-only file (0400) is fine",
			setup: func(t *testing.T, home string) string {
				return writeTokenFile(t, home, UserTokenFileName, tokenA, 0o400)
			},
			wantToken: tokenA,
		},
		{
			name: "world-readable file is refused",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, tokenA, 0o644)
				writeTokenFile(t, home, localAuthFileName, tokenB, 0o600) // not used
				return ""
			},
			wantErr: isFixableInsecure,
		},
		{
			name: "group-readable file is refused",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, localAuthFileName, tokenA, 0o640)
				return ""
			},
			wantErr: isFixableInsecure,
		},
		{
			name: "group-writable file is refused",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, tokenA, 0o620)
				return ""
			},
			wantErr: isFixableInsecure,
		},
		{
			name: "unsafe env file is refused",
			setup: func(t *testing.T, _ string) string {
				t.Setenv(EnvTokenFile, writeTokenFile(t, t.TempDir(), "token", tokenA, 0o604))
				return ""
			},
			wantErr: isFixableInsecure,
		},
		{
			name: "malformed token",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, "not a token", 0o600)
				return ""
			},
			wantErr: func(err error) bool {
				var invalid *InvalidTokenError
				return errors.As(err, &invalid)
			},
		},
		{
			name: "empty file",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, UserTokenFileName, "", 0o600)
				return ""
			},
			wantErr: func(err error) bool {
				var invalid *InvalidTokenError
				return errors.As(err, &invalid)
			},
		},
		{
			name: "a directory is not a token file",
			setup: func(t *testing.T, home string) string {
				if err := os.Mkdir(filepath.Join(home, UserTokenFileName), 0o700); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			wantErr: func(err error) bool { return err != nil && !errors.Is(err, ErrTokenMissing) },
		},
		{
			name: "readable system token, no owner check",
			setup: func(t *testing.T, home string) string {
				// 0644 would be refused for a file in $HOME, but the
				// system file is the service's own business.
				return writeTokenFile(t, home, "system-authtoken.secret", tokenC, 0o644)
			},
			wantToken: tokenC,
		},
		{
			name: "home file wins over system token",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, "system-authtoken.secret", tokenC, 0o644)
				return writeTokenFile(t, home, localAuthFileName, tokenB, 0o600)
			},
			wantToken: tokenB,
		},
		{
			name: "malformed system token",
			setup: func(t *testing.T, home string) string {
				writeTokenFile(t, home, "system-authtoken.secret", "short", 0o600)
				return ""
			},
			wantErr: func(err error) bool {
				var invalid *InvalidTokenError
				return errors.As(err, &invalid)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := setTestHome(t)
			wantPath := tc.setup(t, home)

			token, path, err := FindToken()
			if tc.wantErr != nil {
				if !tc.wantErr(err) {
					t.Fatalf("FindToken() error = %v (token %q, path %q), not the expected error", err, token, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindToken(): %v", err)
			}
			if token != tc.wantToken || path != wantPath {
				t.Errorf("FindToken() = %q, %q; want %q, %q", token, path, tc.wantToken, wantPath)
			}
		})
	}
}

func isFixableInsecure(err error) bool {
	var insecure *InsecureTokenFileError
	return errors.As(err, &insecure) && insecure.Fixable()
}

func TestFindTokenUnreadableSystemToken(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file")
	}
	home := setTestHome(t)
	writeTokenFile(t, home, "system-authtoken.secret", "cccccccccccccccccccccccc", 0o000)
	if _, _, err := FindToken(); !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("err = %v, want ErrTokenMissing", err)
	}
}

func TestFindTokenWithoutHome(t *testing.T) {
	setTestHome(t)
	t.Setenv("HOME", "")
	if _, _, err := FindToken(); !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("err = %v, want ErrTokenMissing", err)
	}
}

// fakeFileInfo is a stand-in for the result of os.Stat, so the test can
// pretend a file belongs to another user (a normal user can't chown).
//
// Embedding fs.FileInfo (a field with a type but no name) gives
// fakeFileInfo all of the interface's methods. The embedded value is nil,
// so calling them would crash, but checkPrivateFile only calls the two
// methods overridden below.
type fakeFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
	uid  uint32
}

func (f fakeFileInfo) Mode() fs.FileMode { return f.mode }
func (f fakeFileInfo) Sys() any          { return &syscall.Stat_t{Uid: f.uid} }

func TestCheckPrivateFile(t *testing.T) {
	me := uint32(os.Geteuid())
	tests := []struct {
		name        string
		info        fakeFileInfo
		wantErr     bool
		wantFixable bool
	}{
		{name: "mine, 0600", info: fakeFileInfo{mode: 0o600, uid: me}},
		{name: "mine, 0400", info: fakeFileInfo{mode: 0o400, uid: me}},
		{name: "mine, 0644", info: fakeFileInfo{mode: 0o644, uid: me}, wantErr: true, wantFixable: true},
		{name: "mine, 0606", info: fakeFileInfo{mode: 0o606, uid: me}, wantErr: true, wantFixable: true},
		{name: "someone else's, 0600", info: fakeFileInfo{mode: 0o600, uid: me + 1}, wantErr: true, wantFixable: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPrivateFile("/home/u/.zeroTierOneAuthToken", tc.info)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("checkPrivateFile: %v", err)
				}
				return
			}
			var insecure *InsecureTokenFileError
			if !errors.As(err, &insecure) {
				t.Fatalf("err = %v, want *InsecureTokenFileError", err)
			}
			if insecure.Fixable() != tc.wantFixable {
				t.Errorf("Fixable() = %v, want %v", insecure.Fixable(), tc.wantFixable)
			}
			if insecure.Error() == "" {
				t.Error("empty error message")
			}
		})
	}
}

func TestPort(t *testing.T) {
	tests := []struct {
		name    string
		content *string // nil = no port file
		want    int
	}{
		{name: "no file", content: nil, want: 9993},
		{name: "plain", content: ptr("9993"), want: 9993},
		{name: "newline", content: ptr("12345\n"), want: 12345},
		{name: "not a number", content: ptr("abc"), want: 9993},
		{name: "zero", content: ptr("0"), want: 9993},
		{name: "too big", content: ptr("70000"), want: 9993},
		{name: "empty", content: ptr(""), want: 9993},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := setTestHome(t)
			if tc.content != nil {
				writeTokenFile(t, home, "system-zerotier-one.port", *tc.content, 0o644)
			}
			if got := Port(); got != tc.want {
				t.Errorf("Port() = %d, want %d", got, tc.want)
			}
		})
	}
}

// ptr returns a pointer to a copy of s (Go can't take the address of a
// literal like &"9993").
func ptr(s string) *string { return &s }

func TestAPIURLs(t *testing.T) {
	t.Run("port file, IPv4 first", func(t *testing.T) {
		home := setTestHome(t)
		writeTokenFile(t, home, "system-zerotier-one.port", "9994", 0o644)
		got, err := APIURLs()
		want := []string{"http://127.0.0.1:9994", "http://[::1]:9994"}
		// reflect.DeepEqual compares slices element by element.
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("APIURLs() = %v, %v; want %v", got, err, want)
		}
	})

	t.Run("override", func(t *testing.T) {
		setTestHome(t)
		t.Setenv(EnvAPIURL, "http://127.0.0.1:19993/")
		got, err := APIURLs()
		want := []string{"http://127.0.0.1:19993"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("APIURLs() = %v, %v; want %v", got, err, want)
		}
	})

	for _, bad := range []string{"127.0.0.1:9993", "ftp://127.0.0.1", "http://", "://x"} {
		t.Run("bad override "+bad, func(t *testing.T) {
			setTestHome(t)
			t.Setenv(EnvAPIURL, bad)
			if got, err := APIURLs(); err == nil {
				t.Errorf("APIURLs() = %v, want an error", got)
			}
		})
	}
}

func TestFindTokenUnreadableHomeFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file")
	}
	// Our own file, but with no read permission: still our file, so the
	// plain "permission denied" error comes back (chmod would fix it).
	home := setTestHome(t)
	writeTokenFile(t, home, UserTokenFileName, "aaaaaaaaaaaaaaaaaaaaaaaa", 0o000)
	_, _, err := FindToken()
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want a permission error", err)
	}
}
