package tray

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/platform/watcher"
)

// The two ways tray hosts read underscores, written the way the hosts do
// it, to check escapeLabel against. See labels.go.

// specDisplay is the dbusmenu spec (KDE Plasma, GTK-based hosts): "__"
// shows one "_", and every other "_" is hidden (it marks a shortcut key).
func specDisplay(label string) string {
	var b strings.Builder
	for i := 0; i < len(label); i++ {
		if label[i] != '_' {
			b.WriteByte(label[i])
			continue
		}
		if i+1 < len(label) && label[i+1] == '_' {
			b.WriteByte('_')
			i++ // skip the second underscore of the pair
		}
	}
	return b.String()
}

// gnomeMnemonic is the regular expression of GNOME's AppIndicator
// extension (dbusMenu.js, version 59): label.replace(/_([^_])/, '$1').
// Without the "g" flag, JavaScript replaces only the first match.
var gnomeMnemonic = regexp.MustCompile(`_[^_]`)

// gnomeDisplay is what GNOME Shell shows for a label.
func gnomeDisplay(label string) string {
	loc := gnomeMnemonic.FindStringIndex(label)
	if loc == nil {
		return label
	}
	return label[:loc[0]] + label[loc[0]+1:] // drop just the underscore
}

// TestUnderscoresNeedEscaping shows why escaping exists: unescaped, a
// network called "lab_01" shows as "lab01" on both kinds of host.
func TestUnderscoresNeedEscaping(t *testing.T) {
	if got := specDisplay("lab_01"); got != "lab01" {
		t.Errorf("spec host shows %q", got)
	}
	if got := gnomeDisplay("lab_01"); got != "lab01" {
		t.Errorf("GNOME shows %q", got)
	}
}

func TestEscapeLabel(t *testing.T) {
	tests := []struct {
		in        string
		wantSpec  string
		wantGNOME string
	}{
		{in: "", wantSpec: "", wantGNOME: ""},
		{in: "home-lab (c0ffee1234000001)", wantSpec: "home-lab (c0ffee1234000001)", wantGNOME: "home-lab (c0ffee1234000001)"},
		{in: "lab_01", wantSpec: "lab__01", wantGNOME: "lab__01"},
		{in: "some_new_state", wantSpec: "some__new__state", wantGNOME: "some__new_state"},
		{in: "_lead", wantSpec: "__lead", wantGNOME: "__lead"},
		{in: "trail_", wantSpec: "trail__", wantGNOME: "trail_"},
		{in: "a__b", wantSpec: "a____b", wantGNOME: "a___b"},
		{in: "___", wantSpec: "______", wantGNOME: "___"},
		{in: "café_☕_x", wantSpec: "café__☕__x", wantGNOME: "café__☕_x"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := escapeLabel(tc.in, labelStyleSpec); got != tc.wantSpec {
				t.Errorf("spec: escapeLabel(%q) = %q, want %q", tc.in, got, tc.wantSpec)
			}
			if got := escapeLabel(tc.in, labelStyleGNOME); got != tc.wantGNOME {
				t.Errorf("GNOME: escapeLabel(%q) = %q, want %q", tc.in, got, tc.wantGNOME)
			}
		})
	}
}

// TestEscapeLabelRoundTrip checks that each host shows exactly the
// original text, for odd labels and for every label of every golden menu.
func TestEscapeLabelRoundTrip(t *testing.T) {
	labels := []string{
		"", "_", "__", "___", "a_", "_a", "a_b_c", "a__b", "x_y__z___", "_ _ _",
		"tab\there", "café ☕_edition", "100%_&_<b>bold</b>",
	}
	for _, gc := range goldenCases() {
		var walk func(nodes []Node)
		walk = func(nodes []Node) {
			for _, n := range nodes {
				labels = append(labels, n.Label)
				walk(n.Children)
			}
		}
		walk(Build(goldenViewModel(t, gc)))
	}

	for _, label := range labels {
		if got := specDisplay(escapeLabel(label, labelStyleSpec)); got != label {
			t.Errorf("spec host shows %q for %q", got, label)
		}
		if got := gnomeDisplay(escapeLabel(label, labelStyleGNOME)); got != label {
			t.Errorf("GNOME shows %q for %q", got, label)
		}
	}
}

// TestHostStyle maps what watcher.Check found to a label style: GNOME
// Shell's own process gets the GNOME style, every other host the spec
// style, and no host at all means "ask again later".
func TestHostStyle(t *testing.T) {
	tests := []struct {
		name      string
		status    watcher.Status
		wantStyle labelStyle
		wantFound bool
	}{
		{name: "no host", status: watcher.Status{}, wantStyle: labelStyleSpec, wantFound: false},
		{name: "gnome-shell", status: watcher.Status{Watcher: true, HostRegistered: true, Program: "gnome-shell"}, wantStyle: labelStyleGNOME, wantFound: true},
		{name: "plasmashell", status: watcher.Status{Watcher: true, HostRegistered: true, Program: "plasmashell"}, wantStyle: labelStyleSpec, wantFound: true},
		{name: "program unknown", status: watcher.Status{Watcher: true}, wantStyle: labelStyleSpec, wantFound: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			style, found := hostStyle(tc.status)
			if style != tc.wantStyle || found != tc.wantFound {
				t.Errorf("hostStyle(%+v) = %v, %t; want %v, %t", tc.status, style, found, tc.wantStyle, tc.wantFound)
			}
		})
	}
}
