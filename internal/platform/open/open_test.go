package open

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeXdgOpen puts a fake xdg-open first in $PATH. It writes its arguments
// to a file and returns that file's path.
func fakeXdgOpen(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	// $PATH holds only dir, so the script calls mv by its full path.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + ".tmp && /bin/mv " + out + ".tmp " + out + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return out
}

func TestURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://sso.example.com/authorize?nwid=c0ffee1234000003&state=x"},
		{name: "http", url: "http://example.com/"},
		{name: "file is refused", url: "file:///etc/passwd", wantErr: true},
		{name: "custom scheme is refused", url: "steam://run/1", wantErr: true},
		{name: "option is refused", url: "--help", wantErr: true},
		{name: "no host", url: "https:///path", wantErr: true},
		{name: "empty", url: "", wantErr: true},
		{name: "not a URL", url: "http://[::1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := fakeXdgOpen(t)
			err := URL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("URL(%q) = nil, want an error", tc.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("URL(%q) = %v", tc.url, err)
			}

			// xdg-open runs in the background; wait for its output file.
			deadline := time.Now().Add(5 * time.Second)
			for {
				data, err := os.ReadFile(out)
				if err == nil {
					if got := strings.TrimSpace(string(data)); got != tc.url {
						t.Errorf("xdg-open got %q, want %q", got, tc.url)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("fake xdg-open never ran")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestURLNoXdgOpen(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty directory: no xdg-open
	if err := URL("https://example.com/"); err == nil {
		t.Fatal("URL without xdg-open = nil, want an error")
	}
}
