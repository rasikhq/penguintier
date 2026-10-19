package resolved

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIfIndex(t *testing.T) {
	// A fake /sys/class/net with two interfaces, one of them broken.
	dir := t.TempDir()
	writeIfIndex(t, dir, "zt3jn5bvfq", "12\n")
	writeIfIndex(t, dir, "broken0", "not a number\n")
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })

	tests := []struct {
		name      string
		ifname    string
		want      int
		wantErr   bool
		wantNoInf bool // the error must wrap ErrNoInterface
	}{
		{name: "found", ifname: "zt3jn5bvfq", want: 12},
		{name: "missing interface", ifname: "ztgone", wantErr: true, wantNoInf: true},
		{name: "bad file content", ifname: "broken0", wantErr: true},
		{name: "empty name", ifname: "", wantErr: true},
		{name: "path escape", ifname: "../etc", wantErr: true},
		{name: "dot dot", ifname: "..", wantErr: true},
		{name: "space", ifname: "zt 1", wantErr: true},
		{name: "too long", ifname: "zt0123456789abcd", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IfIndex(tt.ifname)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IfIndex(%q) error = %v, wantErr %v", tt.ifname, err, tt.wantErr)
			}
			if tt.wantNoInf && !errors.Is(err, ErrNoInterface) {
				t.Errorf("IfIndex(%q) error = %v, want ErrNoInterface", tt.ifname, err)
			}
			if got != tt.want {
				t.Errorf("IfIndex(%q) = %d, want %d", tt.ifname, got, tt.want)
			}
		})
	}
}

// TestIfIndexReal checks the real /sys/class/net: every Linux machine has
// "lo".
func TestIfIndexReal(t *testing.T) {
	if _, err := os.Stat("/sys/class/net/lo"); err != nil {
		t.Skip("no /sys/class/net/lo on this machine")
	}
	got, err := IfIndex("lo")
	if err != nil || got <= 0 {
		t.Fatalf("IfIndex(lo) = %d, %v; want a positive index", got, err)
	}
}

func writeIfIndex(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "ifindex"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
