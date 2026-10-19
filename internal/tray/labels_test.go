package tray

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
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

// TestTrayHostStyle runs trayHostStyle against a private D-Bus daemon.
// The test process itself owns the tray host name there, so the host's
// program name is this test binary: not gnome-shell, so spec style.
func TestTrayHostStyle(t *testing.T) {
	address := startBus(t)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, found := trayHostStyle(conn); found {
		t.Fatal("found a tray host on an empty bus")
	}

	reply, err := conn.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own the watcher name: reply %v, err %v", reply, err)
	}
	style, found := trayHostStyle(conn)
	if !found || style != labelStyleSpec {
		t.Errorf("trayHostStyle = %v, %v; want spec style, found", style, found)
	}
}

// startBus runs a private dbus-daemon for one test and returns its
// address. The test is skipped when dbus-daemon is not installed.
// (internal/platform/notify's tests have the same helper.)
func startBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}

	// A socket path may be at most 108 bytes, and t.TempDir() can be
	// longer, so the socket goes in a short directory under /tmp.
	dir, err := os.MkdirTemp("/tmp", "pt-bus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	config := `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=` + filepath.Join(dir, "bus") + `</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow receive_sender="*"/>
    <allow own="*"/>
  </policy>
</busconfig>
`
	configPath := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), daemon, "--config-file="+configPath, "--nofork", "--print-address")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read dbus-daemon address: %v", err)
	}
	return strings.TrimSpace(line)
}
