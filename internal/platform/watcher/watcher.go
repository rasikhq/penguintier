package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Names on the session bus.
const (
	// WatcherName is the StatusNotifierItem watcher's well-known name.
	WatcherName   = "org.kde.StatusNotifierWatcher"
	watcherPath   = dbus.ObjectPath("/StatusNotifierWatcher")
	propertiesGet = "org.freedesktop.DBus.Properties.Get"

	// GNOME Shell itself answers extension questions, on its own bus name.
	// (There is also a bus name "org.gnome.Shell.Extensions", but that is
	// a helper for the Extensions app that only runs on demand.)
	shellName            = "org.gnome.Shell"
	shellPath            = dbus.ObjectPath("/org/gnome/Shell")
	shellExtensionsIface = "org.gnome.Shell.Extensions"
)

// Status is what Check found out.
type Status struct {
	// Watcher: something owns org.kde.StatusNotifierWatcher.
	Watcher bool `json:"watcher"`
	// HostRegistered: the watcher says a host (the panel that draws the
	// icons) is registered.
	HostRegistered bool `json:"hostRegistered"`
	// Program is the watcher's process name, such as "gnome-shell" or
	// "plasmashell". Empty when unknown.
	Program string `json:"program,omitempty"`
}

// Ready reports whether tray icons can be shown.
func (s Status) Ready() bool {
	return s.Watcher && s.HostRegistered
}

// Check asks the session bus whether a tray host is running. conn is a
// session bus connection.
func Check(ctx context.Context, conn *dbus.Conn) (Status, error) {
	var owner string
	err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, WatcherName).Store(&owner)
	if err != nil {
		// The bus answers NameHasNoOwner when nobody owns the name. That is
		// the "no tray host" answer, not a failure.
		var dbusErr dbus.Error
		if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("ask D-Bus for %s: %w", WatcherName, err)
	}

	status := Status{Watcher: true, Program: programOf(ctx, conn, owner)}
	var v dbus.Variant
	err = conn.Object(WatcherName, watcherPath).CallWithContext(ctx, propertiesGet, dbus.FlagNoAutoStart,
		WatcherName, "IsStatusNotifierHostRegistered").Store(&v)
	if err != nil {
		// A watcher without the property (some minimal ones): give it the
		// benefit of the doubt rather than warn for nothing.
		status.HostRegistered = true
		return status, nil
	}
	registered, ok := v.Value().(bool)
	status.HostRegistered = !ok || registered
	return status, nil
}

// programOf returns the process name of a bus connection, such as
// "gnome-shell", or "" if it can't be found.
func programOf(ctx context.Context, conn *dbus.Conn, busName string) string {
	var pid uint32
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, busName).Store(&pid); err != nil {
		return ""
	}
	// /proc/<pid>/comm holds a process's program name, e.g. "gnome-shell\n".
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

// The AppIndicator extension's IDs ("UUIDs"). Debian and Ubuntu package it
// under Ubuntu's ID; Fedora, Arch, openSUSE and extensions.gnome.org use
// the upstream one.
const (
	UbuntuExtensionUUID   = "ubuntu-appindicators@ubuntu.com"
	UpstreamExtensionUUID = "appindicatorsupport@rgcjonas.gmail.com"
)

// extensionUUIDs are checked in this order.
var extensionUUIDs = []string{UbuntuExtensionUUID, UpstreamExtensionUUID}

// GNOME Shell's extension states (ExtensionState in its
// js/misc/extensionUtils.js). Only these two matter here.
const (
	extensionStateActive   = 1
	extensionStateInactive = 2
)

// Extension is the state of GNOME's AppIndicator extension.
type Extension struct {
	// UUID is the extension found, or "" when none is installed.
	UUID string `json:"uuid,omitempty"`
	// Installed: its files are on disk.
	Installed bool `json:"installed"`
	// Loaded: GNOME Shell knows it. An extension installed after login is
	// on disk but not loaded until the next login (on Wayland).
	Loaded bool `json:"loaded"`
	// Enabled: the user turned it on.
	Enabled bool `json:"enabled"`
	// Active: it is running. Enabled but not active means extensions are
	// switched off as a whole, or it failed to start (Failed).
	Active bool `json:"active"`
	// Failed: GNOME Shell reports an error or an unsupported version.
	Failed bool `json:"failed"`
}

// extensionDirs are where GNOME Shell looks for extensions: the user's
// folder, then the system ones. A variable so tests can change it.
var extensionDirs = func() []string {
	dirs := []string{}
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		dirs = append(dirs, filepath.Join(data, "gnome-shell", "extensions"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "gnome-shell", "extensions"))
	}
	return append(dirs, "/usr/local/share/gnome-shell/extensions", "/usr/share/gnome-shell/extensions")
}

// CheckExtension finds GNOME's AppIndicator extension and asks GNOME
// Shell about it. conn is a session bus connection. Errors are not
// returned: what could not be found stays false.
func CheckExtension(ctx context.Context, conn *dbus.Conn) Extension {
	var found Extension
	for _, uuid := range extensionUUIDs {
		ext := Extension{UUID: uuid, Installed: installed(uuid)}

		// GetExtensionInfo returns the extension's metadata as a map, or
		// an empty map when GNOME Shell doesn't know it.
		var info map[string]dbus.Variant
		err := conn.Object(shellName, shellPath).CallWithContext(ctx,
			shellExtensionsIface+".GetExtensionInfo", dbus.FlagNoAutoStart, uuid).Store(&info)
		if err == nil && len(info) > 0 {
			ext.Loaded = true
			ext.Installed = true
			if v, ok := info["enabled"]; ok {
				ext.Enabled, _ = v.Value().(bool)
			}
			if v, ok := info["state"]; ok {
				// GNOME Shell sends the state as a double ("d").
				state, _ := v.Value().(float64)
				ext.Active = state == extensionStateActive
				ext.Failed = state != extensionStateActive && state != extensionStateInactive
			}
		}

		// Prefer the one GNOME Shell knows, then one on disk.
		switch {
		case ext.Loaded:
			return ext
		case ext.Installed && !found.Installed:
			found = ext
		}
	}
	return found
}

// installed reports whether the extension's folder exists.
func installed(uuid string) bool {
	for _, dir := range extensionDirs() {
		if info, err := os.Stat(filepath.Join(dir, uuid)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
