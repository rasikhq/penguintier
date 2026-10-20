package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/rasikhq/penguintier/internal/atomicfile"
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

// saveState writes the state file. The write is atomic (see
// internal/atomicfile), so a reader (or a crash) never sees a half-written
// file. Mode 0600: the state holds no secrets, but nobody else needs it.
func saveState(path string, links map[string]Config) error {
	data, err := json.MarshalIndent(savedState{Links: links}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
