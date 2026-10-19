package autostart

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the autostart file's name: the app ID plus ".desktop".
const FileName = "io.github.rasikhq.Penguintier.desktop"

// entry is what Enable writes. --autostart tells the tray it was started
// at login, so it never shows first-run prompts then. TryExec makes the
// desktop skip the entry when penguintier is no longer installed: the file
// lives in the user's home, so uninstalling the package leaves it behind.
const entry = `[Desktop Entry]
Type=Application
Name=Penguintier
Comment=ZeroTier tray icon, started at login
TryExec=penguintier
Exec=penguintier --autostart
Icon=io.github.rasikhq.Penguintier
Terminal=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
`

// DefaultPath returns where the autostart file goes:
// $XDG_CONFIG_HOME/autostart/io.github.rasikhq.Penguintier.desktop
// (normally ~/.config/autostart/...).
func DefaultPath() (string, error) {
	// os.UserConfigDir returns $XDG_CONFIG_HOME, or ~/.config if that is
	// not set.
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "autostart", FileName), nil
}

// Enabled reports whether the file at path makes Penguintier start at
// login: it exists, and it is not switched off with Hidden=true or
// X-GNOME-Autostart-enabled=false (desktop settings apps do that instead
// of deleting the file).
func Enabled(path string) bool {
	data, err := os.ReadFile(path) //nolint:gosec // our own autostart file
	if err != nil {
		return false // missing (or unreadable): not enabled
	}
	return !switchedOff(data)
}

// switchedOff reports whether a .desktop file has Hidden=true or
// X-GNOME-Autostart-enabled=false in its [Desktop Entry] group.
func switchedOff(data []byte) bool {
	inEntry := false
	// A bufio.Scanner splits its input into lines.
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "Hidden" && value == "true" {
			return true
		}
		if key == "X-GNOME-Autostart-enabled" && value == "false" {
			return true
		}
	}
	return false
}

// Enable writes the autostart file, replacing whatever is at path.
func Enable(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create autostart folder: %w", err)
	}
	// Write a temporary file and rename it over the old one, so a crash
	// never leaves a half-written autostart file behind.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+FileName+".*")
	if err != nil {
		return fmt.Errorf("write autostart file: %w", err)
	}
	// After a successful rename the temporary name is gone and Remove
	// fails quietly; after an error it cleans up.
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(entry); err != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return fmt.Errorf("write autostart file: %w", err)
	}
	// CreateTemp makes the file 0600. Desktop files are not secret, and
	// 0644 is what desktops write themselves.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write autostart file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write autostart file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write autostart file: %w", err)
	}
	return nil
}

// Disable removes the autostart file. A missing file is not an error.
func Disable(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove autostart file: %w", err)
	}
	return nil
}
