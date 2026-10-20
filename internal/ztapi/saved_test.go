package ztapi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newTestStore returns a store whose files live in a fresh temporary
// directory. The config directory does not exist yet.
func newTestStore(t *testing.T) *SavedStore {
	t.Helper()
	dir := t.TempDir()
	return &SavedStore{
		Path:          filepath.Join(dir, "config", "penguintier", "saved_networks.json"),
		DesktopUIPath: filepath.Join(dir, ".zerotier_ui", "saved_networks.json"),
	}
}

func TestSavedStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// A missing file is an empty store.
	nets, err := s.Load()
	if err != nil || len(nets) != 0 {
		t.Fatalf("Load() on a missing file = %v, %v; want an empty map", nets, err)
	}

	home := SavedNetwork{Name: "home-lab", AllowManaged: true, AllowDNS: true}
	office := SavedNetwork{Name: "office", AllowGlobal: true, AllowDefault: true}
	if err := s.Put("c0ffee1234000002", office); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("c0ffee1234000001", home); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The exact file layout is the contract in docs/architecture.md.
	want := `{
  "version": 1,
  "networks": {
    "c0ffee1234000001": {
      "name": "home-lab",
      "allowManaged": true,
      "allowGlobal": false,
      "allowDefault": false,
      "allowDNS": true
    },
    "c0ffee1234000002": {
      "name": "office",
      "allowManaged": false,
      "allowGlobal": true,
      "allowDefault": true,
      "allowDNS": false
    }
  }
}
`
	assertFile(t, s.Path, want, 0o600)
	info, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("config directory mode = %04o, want 0700", info.Mode().Perm())
	}

	nets, err = s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantNets := map[NetworkID]SavedNetwork{"c0ffee1234000001": home, "c0ffee1234000002": office}
	if !reflect.DeepEqual(nets, wantNets) {
		t.Errorf("Load() = %+v, want %+v", nets, wantNets)
	}

	// Remove, and removing again is fine.
	if err := s.Remove("c0ffee1234000002"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove("c0ffee1234000002"); err != nil {
		t.Fatalf("Remove again: %v", err)
	}
	nets, _ = s.Load()
	if len(nets) != 1 {
		t.Errorf("after Remove: %+v, want one network", nets)
	}

	assertOnlyFiles(t, filepath.Dir(s.Path), "saved_networks.json")
}

func TestSavedStoreSaveNil(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(nil); err != nil {
		t.Fatalf("Save(nil): %v", err)
	}
	assertFile(t, s.Path, "{\n  \"version\": 1,\n  \"networks\": {}\n}\n", 0o600)
}

func TestSavedStorePutRejectsBadID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Put("bogus", SavedNetwork{}); err == nil {
		t.Error("Put with a bad ID: want an error")
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("Put with a bad ID created the file")
	}
}

func TestSavedStoreLoadProblems(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string // part of the error message; "" = no error
		wantLen int
	}{
		{name: "damaged", content: `{"version":1,"networks":{`, wantErr: "damaged"},
		{name: "not an object", content: `[]`, wantErr: "damaged"},
		{name: "newer version", content: `{"version":2,"networks":{}}`, wantErr: "unsupported version 2"},
		{name: "no version", content: `{"networks":{}}`, wantErr: "unsupported version 0"},
		{
			name:    "bad IDs are skipped",
			content: `{"version":1,"networks":{"bogus":{"name":"x"},"C0FFEE1234000001":{},"c0ffee1234000002":{"name":"ok"}}}`,
			wantLen: 1,
		},
		{
			name:    "unknown fields are ignored",
			content: `{"version":1,"later":true,"networks":{"c0ffee1234000002":{"name":"ok","color":"blue"}}}`,
			wantLen: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.Path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			nets, err := s.Load()
			if tc.wantErr == "" {
				if err != nil || len(nets) != tc.wantLen {
					t.Fatalf("Load() = %v, %v; want %d networks", nets, err, tc.wantLen)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want one containing %q", err, tc.wantErr)
			}

			// Put must not overwrite a file it couldn't read.
			if err := s.Put("c0ffee1234000001", SavedNetwork{Name: "new"}); err == nil {
				t.Error("Put over an unreadable file succeeded")
			}
			assertFile(t, s.Path, tc.content, 0o600)
		})
	}
}

// TestSavedStoreAtomicFailureKeepsOldFile checks the point of an atomic
// write: if writing fails, the old file is still complete.
func TestSavedStoreAtomicFailureKeepsOldFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	s := newTestStore(t)
	if err := s.Put("c0ffee1234000001", SavedNetwork{Name: "old"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}

	// A read-only directory: the temporary file can't be created.
	dir := filepath.Dir(s.Path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // so t.TempDir can delete it

	if err := s.Put("c0ffee1234000002", SavedNetwork{Name: "new"}); err == nil {
		t.Fatal("Put into a read-only directory succeeded")
	}
	after, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("file changed after a failed write:\n%s\nwant:\n%s", after, before)
	}
	assertOnlyFiles(t, dir, "saved_networks.json")
}

func TestImportDesktopUI(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.DesktopUIPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.DesktopUIPath, readFixture(t, "handmade/desktopui-saved_networks.json"), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := s.ImportDesktopUI()
	if err != nil {
		t.Fatalf("ImportDesktopUI: %v", err)
	}
	if n != 4 {
		t.Errorf("imported %d networks, want 4", n)
	}

	nets, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[NetworkID]SavedNetwork{
		// Flags from the settings string; everything else in it is dropped.
		"c0ffee1234000001": {Name: "home-lab", AllowManaged: false, AllowGlobal: true, AllowDNS: true},
		// No name in the entry: the name from settings. Missing flags keep
		// ZeroTier's defaults (allowManaged on).
		"c0ffee1234000002": {Name: "office-from-settings", AllowManaged: true, AllowDefault: true},
		// Settings that are not JSON: defaults.
		"c0ffee1234000003": {Name: "broken-settings", AllowManaged: true},
		// Upper-case ID is normalized; settings stored as an object.
		"c0ffee1234000004": {Name: "upper-case-id", AllowManaged: true, AllowGlobal: true},
	}
	if !reflect.DeepEqual(nets, want) {
		t.Errorf("imported:\n%+v\nwant:\n%+v", nets, want)
	}

	// One time only: our file exists now, so a second import does nothing,
	// even after the user changed something.
	if err := s.Remove("c0ffee1234000003"); err != nil {
		t.Fatal(err)
	}
	n, err = s.ImportDesktopUI()
	if err != nil || n != 0 {
		t.Errorf("second ImportDesktopUI() = %d, %v; want 0, nil", n, err)
	}
	nets, _ = s.Load()
	if _, ok := nets["c0ffee1234000003"]; ok {
		t.Error("the second import brought back a forgotten network")
	}
}

func TestImportDesktopUINothingToImport(t *testing.T) {
	s := newTestStore(t)
	n, err := s.ImportDesktopUI()
	if err != nil || n != 0 {
		t.Errorf("ImportDesktopUI() = %d, %v; want 0, nil", n, err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("ImportDesktopUI created our file although there was nothing to import")
	}
}

func TestImportDesktopUIDamaged(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.DesktopUIPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.DesktopUIPath, []byte(`{"c0ffee1234000001": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportDesktopUI(); err == nil {
		t.Error("ImportDesktopUI of a damaged file: want an error")
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("a failed import created our file")
	}
}

func TestDefaultSavedStore(t *testing.T) {
	home := setTestHome(t)

	s, err := DefaultSavedStore()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "penguintier", "saved_networks.json"); s.Path != want {
		t.Errorf("Path = %q, want %q", s.Path, want)
	}
	if want := filepath.Join(home, ".zerotier_ui", "saved_networks.json"); s.DesktopUIPath != want {
		t.Errorf("DesktopUIPath = %q, want %q", s.DesktopUIPath, want)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	s, err = DefaultSavedStore()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "penguintier", "saved_networks.json"); s.Path != want {
		t.Errorf("Path with XDG_CONFIG_HOME = %q, want %q", s.Path, want)
	}
}

func TestSavedFromNetworkAndFlags(t *testing.T) {
	n := Network{ID: "c0ffee1234000001", Name: "home-lab", AllowManaged: true, AllowDNS: true, Status: StatusOK}
	saved := SavedFromNetwork(n)
	want := SavedNetwork{Name: "home-lab", AllowManaged: true, AllowDNS: true}
	if saved != want {
		t.Errorf("SavedFromNetwork = %+v, want %+v", saved, want)
	}
	wantFlags := map[Flag]bool{FlagAllowManaged: true, FlagAllowGlobal: false, FlagAllowDefault: false, FlagAllowDNS: true}
	if got := saved.Flags(); !reflect.DeepEqual(got, wantFlags) {
		t.Errorf("Flags() = %v, want %v", got, wantFlags)
	}
}
