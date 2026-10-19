package tray

import (
	"fmt"

	"github.com/rasikhq/penguintier/assets"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// IconState is which tray icon is shown. The values are part of the icon
// file names: assets/tray/penguintier-<state>.png.
type IconState string

// The three icon states.
const (
	// IconOnline: the service runs and every network is fine.
	IconOnline IconState = "online"
	// IconOffline: the service is down or missing, or the node is offline.
	IconOffline IconState = "offline"
	// IconAttention: the user has to do something: authorize Penguintier,
	// log in to a network, ask a network admin for access, ...
	IconAttention IconState = "attention"
)

// allIconStates lists every state, for loading the icons.
var allIconStates = []IconState{IconOnline, IconOffline, IconAttention}

// Icon returns the icon state for vm.
func (vm ViewModel) Icon() IconState {
	switch vm.Service {
	case ServiceDown, ServiceNotInstalled:
		return IconOffline
	case ServiceNeedsAuth, ServiceError:
		return IconAttention
	}
	if !vm.Online {
		return IconOffline
	}
	for _, n := range vm.Networks {
		if needsAttention(n.Status) {
			return IconAttention
		}
	}
	return IconOnline
}

// Tooltip returns a one-line summary for the tray icon's tooltip. (GNOME
// does not show tray tooltips; KDE and most panels do.)
func (vm ViewModel) Tooltip() string {
	if vm.Service != ServiceOK {
		return "Penguintier: " + vm.Problem
	}
	if !vm.Online {
		return "Penguintier: ZeroTier is offline"
	}
	if len(vm.Networks) == 0 {
		return "Penguintier: online, no networks joined"
	}

	text := "Penguintier: online, " + plural(len(vm.Networks), "network", "networks")
	bad := 0
	for _, n := range vm.Networks {
		if needsAttention(n.Status) {
			bad++
		}
	}
	switch {
	case bad == 1:
		text += ", 1 needs attention"
	case bad > 1:
		text += fmt.Sprintf(", %d need attention", bad)
	}
	return text
}

// needsAttention reports whether a network status needs the user to act.
// REQUESTING_CONFIGURATION is a normal step of joining, and UNKNOWN is a
// status from a newer ZeroTier we can't judge, so neither counts.
func needsAttention(s ztapi.Status) bool {
	switch s {
	case ztapi.StatusAccessDenied, ztapi.StatusAuthenticationRequired, ztapi.StatusNotFound,
		ztapi.StatusPortError, ztapi.StatusClientTooOld:
		return true
	default:
		return false
	}
}

// plural returns "1 network" or "3 networks".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// loadIcons reads the three PNG icons that are built into the program.
// They are PNGs because the tray protocol sends the icon as pixels; the
// tray host scales them to the panel size.
func loadIcons() (map[IconState][]byte, error) {
	icons := map[IconState][]byte{}
	for _, state := range allIconStates {
		name := "tray/penguintier-" + string(state) + ".png"
		data, err := assets.Tray.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("load tray icon %s: %w", name, err)
		}
		icons[state] = data
	}
	return icons, nil
}
