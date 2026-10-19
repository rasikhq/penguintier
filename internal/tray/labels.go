package tray

import (
	"fmt"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Underscores in menu labels
//
// The tray menu travels over D-Bus in the "dbusmenu" format. Its spec
// borrows GTK's mnemonics: an underscore marks the next character as the
// keyboard shortcut and is not shown, and "__" shows one "_". A network
// called "lab_01" would show as "lab01". So labels need escaping, but the
// hosts disagree on how:
//
//   - KDE Plasma and the GTK-based hosts (XFCE, Cinnamon, MATE, waybar, ...)
//     follow the spec: every "_" must be doubled.
//   - GNOME Shell's AppIndicator extension (checked in version 59, the one
//     in Debian 13) runs label.replace(/_([^_])/, '$1'): it removes only
//     the FIRST underscore that is followed by another character, and
//     shows "__" as two underscores. Doubling everything would show
//     "some__new__state" as "some_new__state". Instead we double just that
//     first underscore, which GNOME then removes again.
//
// No single escaping works on both kinds of host (as soon as a label has
// two underscores), so the controller asks D-Bus which program runs the
// tray host and picks the style for it.

// labelStyle is how the tray host reads underscores in labels.
type labelStyle int

const (
	// labelStyleSpec: the dbusmenu spec (KDE, GTK-based hosts). The zero
	// value, so it is the default.
	labelStyleSpec labelStyle = iota
	// labelStyleGNOME: GNOME Shell's AppIndicator extension.
	labelStyleGNOME
)

// escapeLabel returns s escaped so that the tray host shows exactly s.
func escapeLabel(s string, style labelStyle) string {
	if style == labelStyleGNOME {
		i := firstMnemonicUnderscore(s)
		if i < 0 {
			return s // GNOME removes nothing from this label
		}
		return s[:i] + "_" + s[i:]
	}
	return strings.ReplaceAll(s, "_", "__")
}

// firstMnemonicUnderscore returns the byte index of the first "_" that is
// followed by a character other than "_", or -1 if there is none. That is
// the underscore GNOME's extension removes.
//
// Indexing a string gives bytes, not characters. That is safe here: "_" is
// one byte, and in UTF-8 the bytes of a multi-byte character such as "é"
// never look like "_".
func firstMnemonicUnderscore(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '_' && s[i+1] != '_' {
			return i
		}
	}
	return -1
}

// trayHostStyle finds out which label style the running tray host uses.
// found is false when no tray host is running (yet); then the caller
// should ask again later.
//
// The tray host is whoever owns the D-Bus name
// org.kde.StatusNotifierWatcher. On GNOME that is the gnome-shell process
// itself, because the AppIndicator extension runs inside it.
func trayHostStyle(conn *dbus.Conn) (style labelStyle, found bool) {
	bus := conn.BusObject() // the bus's own object, org.freedesktop.DBus

	var owner string
	if err := bus.Call("org.freedesktop.DBus.GetNameOwner", 0, "org.kde.StatusNotifierWatcher").Store(&owner); err != nil {
		return labelStyleSpec, false
	}
	var pid uint32
	if err := bus.Call("org.freedesktop.DBus.GetConnectionUnixProcessID", 0, owner).Store(&pid); err != nil {
		return labelStyleSpec, true // a host runs, but we can't tell which
	}
	// /proc/<pid>/comm holds a process's program name, e.g. "gnome-shell\n".
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return labelStyleSpec, true
	}
	if strings.TrimSpace(string(comm)) == "gnome-shell" {
		return labelStyleGNOME, true
	}
	return labelStyleSpec, true
}
