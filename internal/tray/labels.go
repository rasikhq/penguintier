package tray

import (
	"strings"

	"github.com/rasikhq/penguintier/internal/platform/watcher"
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

// hostStyle picks the label style for the tray host that watcher.Check
// found. found is false when nothing owns org.kde.StatusNotifierWatcher
// (yet); then the caller asks again later.
//
// On GNOME the owner is the gnome-shell process itself, because the
// AppIndicator extension runs inside it. Any other host, or one whose
// program name can't be read, gets the spec style.
func hostStyle(status watcher.Status) (style labelStyle, found bool) {
	if !status.Watcher {
		return labelStyleSpec, false
	}
	if status.Program == "gnome-shell" {
		return labelStyleGNOME, true
	}
	return labelStyleSpec, true
}
