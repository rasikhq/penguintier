package main

// Light and dark style.
//
// The web page follows the system style through the CSS media query
// prefers-color-scheme. WebKitGTK answers that query from the GTK theme,
// and so does GTK for the window's title bar. But on GNOME the "Dark Style"
// switch no longer changes the GTK 3 theme: it only sets the desktop
// portal's color-scheme setting. Left alone, a GTK 3 window like ours stays
// light while the rest of the desktop is dark.
//
// So before GTK starts, we ask the desktop portal over D-Bus which style the
// user prefers. If it is dark, we set GTK_THEME to the dark variant of the
// user's own GTK theme (for example "Adwaita:dark"). GTK then draws a dark
// title bar, and WebKitGTK reports prefers-color-scheme: dark.
//
// The portal also tells us the desktop's accent color (GNOME 47 and later,
// KDE). The page uses it for the Join button and links, like native apps.

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// The XDG desktop portal's settings interface. Desktops that support the
// portal (GNOME, KDE, and others) answer on the session bus.
const (
	portalService = "org.freedesktop.portal.Desktop"
	portalPath    = "/org/freedesktop/portal/desktop"
	portalReadOne = "org.freedesktop.portal.Settings.ReadOne"
	portalRead    = "org.freedesktop.portal.Settings.Read" // older portals
)

// colorSchemeDark is the value of the portal's color-scheme setting that
// means "prefer dark". (0 means no preference and 2 means prefer light.)
const colorSchemeDark = 1

// desktopStyle is what we learn from the desktop portal.
type desktopStyle struct {
	prefersDark bool   // the user turned on a dark style
	gtkTheme    string // the GTK theme name, e.g. "Adwaita"; "" if unknown
	accentColor string // a CSS color, e.g. "rgb(53 132 228)"; "" if unknown
}

// readDesktopStyle asks the desktop portal for the user's style. Any error
// (no session bus, no portal, an old portal) leaves the fields at their zero
// values, false and "": the window then simply uses the GTK defaults.
func readDesktopStyle() desktopStyle {
	// A context with a timeout: if the portal doesn't answer quickly, give
	// up rather than delay the window. cancel releases the timer; defer runs
	// it when this function returns.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// SessionBus returns one connection that the whole program shares, so
	// it must not be closed here.
	conn, err := dbus.SessionBus()
	if err != nil {
		return desktopStyle{}
	}
	portal := conn.Object(portalService, portalPath)

	var style desktopStyle
	if v, ok := readPortalSetting(ctx, portal, "org.freedesktop.appearance", "color-scheme"); ok {
		// A type assertion: v holds an interface value, and
		// `v.(uint32)` takes out the uint32 inside. ok is false if it
		// holds some other type.
		scheme, ok := v.(uint32)
		style.prefersDark = ok && scheme == colorSchemeDark
	}
	// GNOME's portal publishes this key; other desktops may not, and then
	// it stays "".
	if v, ok := readPortalSetting(ctx, portal, "org.gnome.desktop.interface", "gtk-theme"); ok {
		style.gtkTheme, _ = v.(string) // "" if it isn't a string
	}
	if v, ok := readPortalSetting(ctx, portal, "org.freedesktop.appearance", "accent-color"); ok {
		style.accentColor = cssColor(v)
	}
	return style
}

// readPortalSetting reads one portal setting. It returns the value and true,
// or nil and false if the setting can't be read.
func readPortalSetting(ctx context.Context, portal dbus.BusObject, namespace, key string) (any, bool) {
	var value dbus.Variant
	err := portal.CallWithContext(ctx, portalReadOne, 0, namespace, key).Store(&value)
	if err != nil {
		// ReadOne is fairly new (2023). Older portals only have Read,
		// which wraps the value in one more variant.
		var outer dbus.Variant
		if err := portal.CallWithContext(ctx, portalRead, 0, namespace, key).Store(&outer); err != nil {
			return nil, false
		}
		inner, ok := outer.Value().(dbus.Variant)
		if !ok {
			return nil, false
		}
		value = inner
	}
	return value.Value(), true
}

// cssColor turns the portal's accent color into a CSS color such as
// "rgb(230 45 66)". The portal sends three numbers from 0 to 1 (red, green,
// blue); numbers outside that range mean "no accent color". Anything else
// gives "".
func cssColor(v any) string {
	// godbus hands over a D-Bus struct as a slice of its fields, []any.
	fields, ok := v.([]any)
	if !ok || len(fields) != 3 {
		return ""
	}
	var rgb [3]int // an array of 3 ints; Go fills it with zeros
	for i, field := range fields {
		f, ok := field.(float64)
		if !ok || f < 0 || f > 1 {
			return ""
		}
		rgb[i] = int(math.Round(f * 255))
	}
	return fmt.Sprintf("rgb(%d %d %d)", rgb[0], rgb[1], rgb[2])
}

// darkGTKTheme returns the GTK_THEME value that switches GTK, and with it
// WebKitGTK, to the dark style. It returns "" when nothing should change:
// the user doesn't prefer dark, GTK_THEME is already set (the user's choice
// wins), the theme name is unknown, or the theme is already a dark one.
func darkGTKTheme(style desktopStyle, gtkThemeEnv string) string {
	if !style.prefersDark || gtkThemeEnv != "" || style.gtkTheme == "" {
		return ""
	}
	if isDarkThemeName(style.gtkTheme) {
		return ""
	}
	// "Name:dark" asks GTK for the theme's dark variant. A theme without
	// one falls back to its normal look.
	return style.gtkTheme + ":dark"
}

// isDarkThemeName reports whether a GTK theme name already names a dark
// theme, such as "Adwaita-dark" or "Yaru:dark".
func isDarkThemeName(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, "-dark") || strings.HasSuffix(name, ":dark")
}

// applyDesktopStyle reads the desktop's style and sets GTK_THEME when the
// desktop prefers dark. It returns what it read, and whether the window will
// be dark. It must run before wails.Run, because GTK reads GTK_THEME once,
// when it starts.
func applyDesktopStyle() (style desktopStyle, dark bool) {
	style = readDesktopStyle()
	current := os.Getenv("GTK_THEME")
	if theme := darkGTKTheme(style, current); theme != "" {
		// Setenv only fails for invalid names or values; the window
		// would just stay light, so the error is not worth handling.
		_ = os.Setenv("GTK_THEME", theme)
		return style, true
	}
	if current != "" {
		return style, isDarkThemeName(current) // the user picked the theme
	}
	return style, style.prefersDark || isDarkThemeName(style.gtkTheme)
}
