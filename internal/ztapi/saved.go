package ztapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// savedVersion is the version number written into saved_networks.json. Bump
// it only for a change old versions can't read, and teach Load to read the
// old format too.
const savedVersion = 1

// SavedNetwork is a network the user disconnected from. The tray lists it
// with "Reconnect" and "Forget", and Reconnect restores these settings.
type SavedNetwork struct {
	Name         string `json:"name"`
	AllowManaged bool   `json:"allowManaged"`
	AllowGlobal  bool   `json:"allowGlobal"`
	AllowDefault bool   `json:"allowDefault"`
	AllowDNS     bool   `json:"allowDNS"`
}

// SavedFromNetwork takes the parts of a joined network that are worth
// saving before it is left.
func SavedFromNetwork(n Network) SavedNetwork {
	return SavedNetwork{
		Name:         n.Name,
		AllowManaged: n.AllowManaged,
		AllowGlobal:  n.AllowGlobal,
		AllowDefault: n.AllowDefault,
		AllowDNS:     n.AllowDNS,
	}
}

// Flags returns the four settings in the form Client.SetFlags takes.
func (s SavedNetwork) Flags() map[Flag]bool {
	return map[Flag]bool{
		FlagAllowManaged: s.AllowManaged,
		FlagAllowGlobal:  s.AllowGlobal,
		FlagAllowDefault: s.AllowDefault,
		FlagAllowDNS:     s.AllowDNS,
	}
}

// savedFile is the layout of saved_networks.json:
//
//	{"version": 1, "networks": {"8056c2e21c000001": {"name": "...", "allowManaged": true, ...}}}
type savedFile struct {
	Version  int                        `json:"version"`
	Networks map[NetworkID]SavedNetwork `json:"networks"`
}

// SavedStore reads and writes the saved-networks file.
//
// It keeps nothing in memory: every call reads or writes the file. It does
// no locking, so let one goroutine own it (in the tray, the controller).
type SavedStore struct {
	// Path is our file, normally
	// $XDG_CONFIG_HOME/penguintier/saved_networks.json.
	Path string
	// DesktopUIPath is the official ZeroTier desktop app's file, read once
	// by ImportDesktopUI. Normally ~/.zerotier_ui/saved_networks.json.
	DesktopUIPath string
}

// DefaultSavedStore returns a SavedStore with the standard paths.
func DefaultSavedStore() (*SavedStore, error) {
	// os.UserConfigDir returns $XDG_CONFIG_HOME, or ~/.config if that is
	// not set.
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find config directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	return &SavedStore{
		Path:          filepath.Join(configDir, "penguintier", "saved_networks.json"),
		DesktopUIPath: filepath.Join(home, ".zerotier_ui", "saved_networks.json"),
	}, nil
}

// Load returns all saved networks. A missing file is not an error: it
// returns an empty map. Entries whose key is not a valid network ID are
// skipped.
func (s *SavedStore) Load() (map[NetworkID]SavedNetwork, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[NetworkID]SavedNetwork{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved networks: %w", err)
	}

	var file savedFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read saved networks %s: the file is damaged: %w", s.Path, err)
	}
	if file.Version != savedVersion {
		// Probably written by a newer Penguintier. Refuse rather than
		// guess, so we never overwrite data we don't understand.
		return nil, fmt.Errorf("read saved networks %s: unsupported version %d (want %d)", s.Path, file.Version, savedVersion)
	}

	nets := make(map[NetworkID]SavedNetwork, len(file.Networks))
	for id, n := range file.Networks {
		if id.Valid() {
			nets[id] = n
		}
	}
	return nets, nil
}

// Save replaces the file with nets. The write is atomic (see
// writeFileAtomic), and the file is private to the user (mode 0600).
func (s *SavedStore) Save(nets map[NetworkID]SavedNetwork) error {
	if nets == nil {
		// A nil map would be written as "networks": null.
		nets = map[NetworkID]SavedNetwork{}
	}
	data, err := json.MarshalIndent(savedFile{Version: savedVersion, Networks: nets}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode saved networks: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := writeFileAtomic(s.Path, data, 0o600); err != nil {
		return fmt.Errorf("save networks: %w", err)
	}
	return nil
}

// Put adds or replaces one saved network.
func (s *SavedStore) Put(id NetworkID, n SavedNetwork) error {
	if !id.Valid() {
		return fmt.Errorf("save network: invalid network ID %q", id)
	}
	nets, err := s.Load()
	if err != nil {
		return err
	}
	nets[id] = n
	return s.Save(nets)
}

// Remove deletes one saved network ("Forget"). Removing an ID that is not
// saved is not an error.
func (s *SavedStore) Remove(id NetworkID) error {
	nets, err := s.Load()
	if err != nil {
		return err
	}
	if _, ok := nets[id]; !ok {
		return nil // nothing to do, and no need to rewrite the file
	}
	delete(nets, id)
	return s.Save(nets)
}

// ImportDesktopUI copies the saved networks of ZeroTier's official desktop
// app into our file, once. It returns how many networks it imported.
//
// "Once" works like this: it only runs while our own file does not exist
// yet. After an import our file exists, so later calls do nothing. If the
// official app's file doesn't exist either, nothing happens.
func (s *SavedStore) ImportDesktopUI() (int, error) {
	if _, err := os.Stat(s.Path); err == nil {
		return 0, nil // we have our own file: the import already happened, or isn't needed
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("import saved networks: %w", err)
	}

	data, err := os.ReadFile(s.DesktopUIPath)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("import saved networks: %w", err)
	}

	nets, err := parseDesktopUI(data)
	if err != nil {
		return 0, fmt.Errorf("import saved networks from %s: %w", s.DesktopUIPath, err)
	}
	if err := s.Save(nets); err != nil {
		return 0, err
	}
	return len(nets), nil
}

// desktopUIEntry is one entry of the official app's saved_networks.json.
// That file maps a network ID to {"id", "name", "settings"}, and settings
// is the network's full JSON (as GET /network/{id} returns it), stored as
// a string.
type desktopUIEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// json.RawMessage keeps the value's raw JSON bytes undecoded, so we can
	// look at it ourselves: it may be a string holding JSON, or an object.
	Settings json.RawMessage `json:"settings"`
}

// parseDesktopUI reads the official app's file. Only the name and the four
// "Allow ..." settings are kept.
func parseDesktopUI(data []byte) (map[NetworkID]SavedNetwork, error) {
	var entries map[string]desktopUIEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}

	nets := make(map[NetworkID]SavedNetwork, len(entries))
	for key, e := range entries {
		id, err := ParseNetworkID(key)
		if err != nil {
			id, err = ParseNetworkID(e.ID)
		}
		if err != nil {
			continue // no usable network ID: skip this entry
		}

		// Start from ZeroTier's defaults for a newly joined network, then
		// let the stored settings overwrite them. json.Unmarshal only sets
		// the fields whose keys appear in the JSON, so a missing key keeps
		// its default.
		saved := SavedNetwork{AllowManaged: true}
		settings := e.Settings
		var asString string
		if json.Unmarshal(settings, &asString) == nil {
			settings = json.RawMessage(asString) // it was a string holding JSON
		}
		if len(settings) > 0 {
			// A damaged settings value keeps whatever could be read, plus
			// the defaults. That beats dropping the network.
			_ = json.Unmarshal(settings, &saved)
		}
		if e.Name != "" {
			saved.Name = e.Name
		}
		nets[id] = saved
	}
	return nets, nil
}
