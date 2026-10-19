package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// appState is what the tray remembers between runs, in
// $XDG_CONFIG_HOME/penguintier/state.json. Every field starts false (Go's
// "zero value"), which is the right start for a brand-new user, so a
// missing file needs no special case.
type appState struct {
	// Version of the file layout, for future changes.
	Version int `json:"version"`
	// FirstRunDone: the first interactive run (not --autostart) has
	// happened. Only that run asks for authorization by itself; later runs
	// show "Authorize Penguintier…" in the menu instead.
	FirstRunDone bool `json:"firstRunDone"`
	// DesktopUIImported: the one-time import of the official ZeroTier
	// app's saved networks has run.
	DesktopUIImported bool `json:"desktopUIImported"`
	// StartAtLoginDecided: Start at Login was turned on automatically after
	// the first successful run, or the user set it in the menu. From then
	// on Penguintier never changes it by itself.
	StartAtLoginDecided bool `json:"startAtLoginDecided"`
}

// stateVersion is the current file layout.
const stateVersion = 1

// defaultStatePath returns $XDG_CONFIG_HOME/penguintier/state.json.
func defaultStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "penguintier", "state.json"), nil
}

// loadState reads the state file. A missing file gives a fresh state. A
// damaged file also gives a fresh state, plus an error to log: the flags
// only make Penguintier do some things once, so starting over is safe.
func loadState(path string) (appState, error) {
	data, err := os.ReadFile(path) //nolint:gosec // our own state file
	if errors.Is(err, fs.ErrNotExist) {
		return appState{Version: stateVersion}, nil
	}
	if err != nil {
		return appState{Version: stateVersion}, fmt.Errorf("read %s: %w", path, err)
	}
	var s appState
	if err := json.Unmarshal(data, &s); err != nil {
		return appState{Version: stateVersion}, fmt.Errorf("read %s: the file is damaged: %w", path, err)
	}
	s.Version = stateVersion
	return s, nil
}

// saveState writes the state file atomically: a temporary file first, then
// a rename over the old one, so a crash never leaves half a file.
func saveState(path string, s appState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	// CreateTemp makes the file with mode 0600 (only we can read it).
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state.json.*")
	if err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	// After a successful rename the temporary name is gone and Remove
	// fails quietly; after an error it cleans up.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return fmt.Errorf("save state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
