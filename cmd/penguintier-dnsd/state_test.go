package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)

	// No file yet: empty, not an error.
	links, err := loadState(path)
	if err != nil || len(links) != 0 {
		t.Fatalf("loadState(missing) = %v, %v; want empty", links, err)
	}

	want := map[string]Config{
		"zt34000001": {Network: "c0ffee1234000001", Servers: []netip.Addr{netip.MustParseAddr("10.147.17.1"), netip.MustParseAddr("2001:db8::53")}, Domain: "home.arpa"},
	}
	if err := saveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loadState() = %+v, want %+v", got, want)
	}

	// Only the owner may read it, and no temporary files are left.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %o, want 600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want just the state file", len(entries))
	}

	// The format is part of the contract with older/newer daemons across
	// a package upgrade: check the exact JSON.
	data, _ := os.ReadFile(path)
	wantJSON := `{
  "links": {
    "zt34000001": {
      "network": "c0ffee1234000001",
      "servers": [
        "10.147.17.1",
        "2001:db8::53"
      ],
      "domain": "home.arpa"
    }
  }
}
`
	if string(data) != wantJSON {
		t.Errorf("state file =\n%s\nwant\n%s", data, wantJSON)
	}
}

func TestLoadStateErrors(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(broken); err == nil {
		t.Error("loadState(broken JSON): no error")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	links, err := loadState(empty)
	if err != nil || links == nil {
		t.Errorf("loadState({}) = %v, %v; want an empty, usable map", links, err)
	}
}
