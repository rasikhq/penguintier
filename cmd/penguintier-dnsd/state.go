package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The state file remembers which links we applied DNS settings to, and
// what we set. It lives in the service's RuntimeDirectory (under /run,
// which is emptied at boot), so:
//
//   - after a daemon restart we still recognize our own links, and revert
//     them later (without it they would look like someone else's), and
//   - after a reboot it is gone, and so are resolved's runtime settings.
//
// The format is JSON:
//
//	{"links": {"zt3jn5bvfq": {"network": "8056c2e21c000001", "servers": ["10.147.17.1"], "domain": "pt.test"}}}

// stateFileName is the file's name inside the runtime directory.
const stateFileName = "applied.json"

// savedState is the JSON layout of the state file.
type savedState struct {
	Links map[string]Config `json:"links"`
}

// loadState reads the state file. A missing file is not an error: it
// means we have applied nothing yet.
func loadState(path string) (map[string]Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // our own state file, in our RuntimeDirectory
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	var s savedState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	if s.Links == nil {
		// A JSON file without "links" leaves the map nil. Writing to a
		// nil map panics in Go (reading is fine), so make an empty one.
		s.Links = map[string]Config{}
	}
	return s.Links, nil
}

// saveState writes the state file atomically: first a temporary file in
// the same directory, then a rename over the old file. A rename within one
// directory is atomic on Linux, so a reader (or a crash) never sees a
// half-written file.
func saveState(path string, links map[string]Config) error {
	data, err := json.MarshalIndent(savedState{Links: links}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+stateFileName+".*")
	if err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	// If anything below fails, remove the temporary file. After a
	// successful rename it no longer exists and Remove just fails quietly.
	defer os.Remove(tmp.Name())

	// CreateTemp already makes the file 0600 (only we can read it); the
	// state holds no secrets, but nobody else needs it either.
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return fmt.Errorf("save state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil { //nolint:gosec // path is our own state file in $RUNTIME_DIRECTORY
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
