package watcher

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Desktop is which desktop and distribution Penguintier runs on.
type Desktop struct {
	// Names are $XDG_CURRENT_DESKTOP split at ":", e.g. ["ubuntu",
	// "GNOME"]. The spec allows several, most specific first.
	Names []string `json:"names"`
	// SessionType is $XDG_SESSION_TYPE: "wayland", "x11" or "tty".
	SessionType string `json:"sessionType,omitempty"`
	// OS is what /etc/os-release says.
	OS OSRelease `json:"os"`
}

// OSRelease holds the fields of /etc/os-release that matter here.
type OSRelease struct {
	ID         string   `json:"id"`               // e.g. "debian", "fedora"
	IDLike     []string `json:"idLike,omitempty"` // e.g. ["debian"] on Ubuntu
	PrettyName string   `json:"prettyName,omitempty"`
}

// DetectDesktop reads the environment and /etc/os-release.
func DetectDesktop() Desktop {
	d := Desktop{SessionType: os.Getenv("XDG_SESSION_TYPE"), OS: ReadOSRelease()}
	for _, name := range strings.Split(os.Getenv("XDG_CURRENT_DESKTOP"), ":") {
		if name = strings.TrimSpace(name); name != "" {
			d.Names = append(d.Names, name)
		}
	}
	return d
}

// Is reports whether name is one of the desktop's names, ignoring case.
func (d Desktop) Is(name string) bool {
	return slices.ContainsFunc(d.Names, func(n string) bool { return strings.EqualFold(n, name) })
}

// String is the desktop for humans, e.g. "ubuntu:GNOME (wayland)".
func (d Desktop) String() string {
	name := strings.Join(d.Names, ":")
	if name == "" {
		name = "unknown desktop"
	}
	if d.SessionType != "" {
		name += " (" + d.SessionType + ")"
	}
	return name
}

// osReleasePaths: /etc/os-release, or the vendor copy it usually links to.
var osReleasePaths = []string{"/etc/os-release", "/usr/lib/os-release"}

// ReadOSRelease reads the first os-release file that exists. A missing
// file gives an empty OSRelease.
func ReadOSRelease() OSRelease {
	for _, path := range osReleasePaths {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // fixed system paths
			return ParseOSRelease(data)
		}
	}
	return OSRelease{}
}

// ParseOSRelease reads an os-release file's KEY=value lines. Values may
// be in double or single quotes (os-release(5)).
func ParseOSRelease(data []byte) OSRelease {
	var o OSRelease
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		value = unquote(value)
		switch key {
		case "ID":
			o.ID = strings.ToLower(value)
		case "ID_LIKE":
			o.IDLike = strings.Fields(strings.ToLower(value))
		case "PRETTY_NAME":
			o.PrettyName = value
		}
	}
	return o
}

// unquote removes one pair of surrounding quotes and the backslashes of
// escaped characters inside double quotes.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(s)
	}
	return s
}

// Family is the distribution family that decides the package manager and
// package names: "debian" (also Ubuntu, Mint, ...), "fedora", "arch",
// "suse", or "" for anything else.
func (o OSRelease) Family() string {
	ids := append([]string{o.ID}, o.IDLike...)
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			return "debian"
		case "fedora", "rhel", "centos":
			return "fedora"
		case "arch":
			return "arch"
		case "suse", "opensuse":
			return "suse"
		}
		if strings.HasPrefix(id, "opensuse") {
			return "suse"
		}
	}
	return ""
}

// Advice is how to get tray icons showing on this desktop.
type Advice struct {
	// Problem says in one sentence what is missing.
	Problem string `json:"problem"`
	// Steps are the fix, in order.
	Steps []string `json:"steps"`
}

// Text is the advice as a short paragraph with numbered steps, for a
// notification or a terminal.
func (a Advice) Text() string {
	var b strings.Builder
	b.WriteString(a.Problem)
	for i, step := range a.Steps {
		fmt.Fprintf(&b, "\n%d. %s", i+1, step)
	}
	return b.String()
}

// AdviceFor returns the fix for a missing tray host on desktop d. ext is
// the state of GNOME's AppIndicator extension; it only matters on GNOME.
func AdviceFor(d Desktop, ext Extension) Advice {
	switch {
	case d.Is("GNOME"):
		return gnomeAdvice(d.OS, ext)
	case d.Is("KDE"):
		return Advice{
			Problem: "Your Plasma panel has no system tray.",
			Steps:   []string{"Right-click the panel → Add Widgets… → add “System Tray”"},
		}
	case d.Is("XFCE"):
		return Advice{
			Problem: "Your Xfce panel has no tray for app icons.",
			Steps:   []string{"Right-click the panel → Panel → Add New Items… → add “Status Tray Plugin”"},
		}
	case d.Is("X-Cinnamon"), d.Is("Cinnamon"):
		return Advice{
			Problem: "Your Cinnamon panel shows no app indicators.",
			Steps: []string{
				"Right-click the panel → Applets → add “XApp Status Applet”",
				"Make sure “XApp Status Notifier Watcher” is on in Startup Applications",
			},
		}
	case d.Is("MATE"):
		return Advice{
			Problem: "MATE's notification area doesn't show app indicators by itself.",
			Steps: []string{
				"Install the indicator applet (Debian/Ubuntu: mate-indicator-applet)",
				"Right-click the panel → Add to Panel… → add “Indicator Applet Complete”",
			},
		}
	case d.Is("Budgie"):
		return Advice{
			Problem: "Your Budgie panel has no system tray.",
			Steps:   []string{"Budgie Desktop Settings → your panel → add the “System Tray” applet"},
		}
	case d.Is("LXQt"):
		return Advice{
			Problem: "Your LXQt panel has no tray for app icons.",
			Steps:   []string{"Right-click the panel → Manage Widgets → add “Status Notifier”"},
		}
	case d.Is("COSMIC"):
		return Advice{
			Problem: "Your COSMIC panel has no tray for app icons.",
			Steps:   []string{"Settings → Desktop → Panel → Configure panel applets → add the status area (tray) applet"},
		}
	case d.Is("sway"), d.Is("Hyprland"), d.Is("niri"), d.Is("river"), d.Is("wlroots"):
		return Advice{
			Problem: "Your bar has no system tray.",
			Steps:   []string{"Use a bar with a tray, such as waybar with “tray” in its modules"},
		}
	default:
		return Advice{
			Problem: "No system tray was found for Penguintier's icon.",
			Steps: []string{
				"Add a system tray that supports StatusNotifierItem to your panel or bar",
				"Run “penguintier doctor” in a terminal for details",
			},
		}
	}
}

// gnomeAdvice explains how to get the AppIndicator extension running.
func gnomeAdvice(o OSRelease, ext Extension) Advice {
	const problem = "GNOME shows tray icons only with the AppIndicator extension."
	enable := func(uuid string) string {
		return "Turn it on in the Extensions app, or run: gnome-extensions enable " + uuid
	}

	switch {
	case ext.Loaded && ext.Failed:
		return Advice{
			Problem: "GNOME's AppIndicator extension failed to start.",
			Steps: []string{
				"Update it (it may not support this GNOME version yet)",
				"See why: journalctl --user -b | grep -i appindicator",
			},
		}
	case ext.Loaded && ext.Enabled && !ext.Active:
		return Advice{
			Problem: "GNOME extensions are switched off.",
			Steps:   []string{"Switch on “Extensions” in the Extensions app, or run: gsettings set org.gnome.shell disable-user-extensions false"},
		}
	case ext.Loaded && !ext.Enabled:
		return Advice{
			Problem: "GNOME's AppIndicator extension is turned off.",
			Steps:   []string{enable(ext.UUID)},
		}
	case ext.Installed && !ext.Loaded:
		return Advice{
			Problem: "GNOME's AppIndicator extension is installed but not loaded yet.",
			Steps:   []string{"Log out and back in (GNOME finds new extensions at login)", enable(ext.UUID)},
		}
	}

	// Not installed: name the package for this distribution.
	install, uuid := "", UpstreamExtensionUUID
	switch o.Family() {
	case "debian":
		install, uuid = "sudo apt install gnome-shell-extension-appindicator", UbuntuExtensionUUID
	case "fedora":
		install = "sudo dnf install gnome-shell-extension-appindicator"
	case "arch":
		install = "sudo pacman -S gnome-shell-extension-appindicator"
	case "suse":
		install = "sudo zypper install gnome-shell-extension-appindicator"
	}
	if install == "" {
		return Advice{
			Problem: problem,
			Steps: []string{
				"Install “AppIndicator and KStatusNotifierItem Support” from https://extensions.gnome.org/extension/615/",
				enable(uuid),
			},
		}
	}
	return Advice{
		Problem: problem,
		Steps: []string{
			"Install it: " + install,
			"Log out and back in (GNOME finds new extensions at login)",
			enable(uuid),
		},
	}
}
