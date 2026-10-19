package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableDisable(t *testing.T) {
	// The autostart folder doesn't exist yet: Enable creates it.
	path := filepath.Join(t.TempDir(), "autostart", FileName)
	if Enabled(path) {
		t.Fatal("Enabled before Enable")
	}

	if err := Enable(path); err != nil {
		t.Fatal(err)
	}
	if !Enabled(path) {
		t.Fatal("not Enabled after Enable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nExec=penguintier --autostart\n") {
		t.Errorf("autostart file has no Exec=penguintier --autostart line:\n%s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("autostart file mode = %v (err %v), want 0644", info.Mode().Perm(), err)
	}
	// Only the file itself is left: no temporary files.
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("autostart folder has %d entries, want 1", len(entries))
	}

	if err := Disable(path); err != nil {
		t.Fatal(err)
	}
	if Enabled(path) {
		t.Error("Enabled after Disable")
	}
	if err := Disable(path); err != nil {
		t.Errorf("Disable of a missing file: %v", err)
	}
}

func TestEnabledSwitchedOff(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "plain", content: entry, want: true},
		{name: "Hidden=true", content: entry + "Hidden=true\n", want: false},
		{name: "GNOME switch off", content: strings.Replace(entry, "X-GNOME-Autostart-enabled=true", "X-GNOME-Autostart-enabled=false", 1), want: false},
		{name: "Hidden=false", content: entry + "Hidden=false\n", want: true},
		{name: "Hidden in another group", content: entry + "\n[Desktop Action x]\nHidden=true\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := Enabled(path); got != tt.want {
				t.Errorf("Enabled = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/cfg")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/cfg/autostart/" + FileName; got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}
