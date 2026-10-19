package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeTools creates fake clipboard programs in a new directory and makes it
// the only entry in $PATH. Each fake saves its standard input to
// <name>.out in that directory. Names in failing exit with an error
// instead. $PATH holds only dir (so real tools on this machine are never
// used), which is why the scripts call cat by its full path.
func fakeTools(t *testing.T, names []string, failing []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		script := "#!/bin/sh\n/bin/cat > " + filepath.Join(dir, name+".out") + "\n"
		for _, f := range failing {
			if f == name {
				script = "#!/bin/sh\n/bin/cat > /dev/null\nexit 1\n"
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestCopy(t *testing.T) {
	tests := []struct {
		name      string
		installed []string
		failing   []string
		wayland   string // $WAYLAND_DISPLAY
		x11       string // $DISPLAY
		wantTool  string // which fake should receive the text ("" = none)
		wantErr   bool
	}{
		{name: "wayland uses wl-copy", installed: []string{"wl-copy", "xclip", "xsel"}, wayland: "wayland-0", x11: ":0", wantTool: "wl-copy"},
		{name: "wayland without wl-copy uses xclip", installed: []string{"xclip", "xsel"}, wayland: "wayland-0", x11: ":0", wantTool: "xclip"},
		{name: "x11 skips wl-copy", installed: []string{"wl-copy", "xclip"}, x11: ":0", wantTool: "xclip"},
		{name: "x11 falls back to xsel", installed: []string{"wl-copy", "xsel"}, x11: ":0", wantTool: "xsel"},
		{name: "failing wl-copy falls back to xclip", installed: []string{"wl-copy", "xclip"}, failing: []string{"wl-copy"}, wayland: "wayland-0", x11: ":0", wantTool: "xclip"},
		{name: "wayland only, no wl-copy", installed: []string{"xclip"}, wayland: "wayland-0", wantErr: true},
		{name: "no display", installed: []string{"wl-copy", "xclip", "xsel"}, wantErr: true},
		{name: "nothing installed", wayland: "wayland-0", x11: ":0", wantErr: true},
		{name: "every tool fails", installed: []string{"wl-copy", "xclip"}, failing: []string{"wl-copy", "xclip"}, wayland: "wayland-0", x11: ":0", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := fakeTools(t, tc.installed, tc.failing)
			t.Setenv("WAYLAND_DISPLAY", tc.wayland)
			t.Setenv("DISPLAY", tc.x11)

			const text = "10.147.17.5"
			err := Copy(context.Background(), text)
			if tc.wantErr {
				if !errors.Is(err, ErrNoTool) {
					t.Fatalf("Copy = %v, want ErrNoTool", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Copy = %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dir, tc.wantTool+".out"))
			if err != nil {
				t.Fatalf("%s did not receive the text: %v", tc.wantTool, err)
			}
			if string(got) != text {
				t.Errorf("%s got %q, want %q", tc.wantTool, got, text)
			}
		})
	}
}
