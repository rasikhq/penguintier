package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
)

func TestParseOSRelease(t *testing.T) {
	data := []byte(`# a comment
PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian
VERSION_CODENAME='noble'
`)
	got := parseOSRelease(data)
	if got.ID != "ubuntu" || got.PrettyName != "Ubuntu 24.04.1 LTS" || len(got.IDLike) != 1 || got.IDLike[0] != "debian" {
		t.Errorf("parseOSRelease = %+v", got)
	}

	families := map[string]OSRelease{
		"debian": {ID: "debian"},
		"fedora": {ID: "fedora"},
		"arch":   {ID: "endeavouros", IDLike: []string{"arch"}},
		"suse":   {ID: "opensuse-tumbleweed", IDLike: []string{"opensuse", "suse"}},
		"":       {ID: "nixos"},
	}
	for want, o := range families {
		if got := o.Family(); got != want {
			t.Errorf("%+v.Family() = %q, want %q", o, got, want)
		}
	}
	if got := (OSRelease{ID: "linuxmint", IDLike: []string{"ubuntu", "debian"}}).Family(); got != "debian" {
		t.Errorf("Mint family = %q, want debian", got)
	}
}

func TestDetectDesktop(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	d := DetectDesktop()
	if !d.Is("gnome") || !d.Is("ubuntu") || d.Is("KDE") {
		t.Errorf("Is() wrong for %v", d.Names)
	}
	if got := d.String(); got != "ubuntu:GNOME (wayland)" {
		t.Errorf("String() = %q", got)
	}
}

func TestAdviceFor(t *testing.T) {
	gnome := func(id string) Desktop { return Desktop{Names: []string{"GNOME"}, OS: OSRelease{ID: id}} }
	tests := []struct {
		name     string
		desktop  Desktop
		ext      Extension
		contains []string // every one must appear in Text()
	}{
		{
			name:     "GNOME on Debian, extension missing",
			desktop:  gnome("debian"),
			contains: []string{"sudo apt install gnome-shell-extension-appindicator", "Log out and back in", "gnome-extensions enable ubuntu-appindicators@ubuntu.com"},
		},
		{
			name:     "GNOME on Fedora, extension missing",
			desktop:  gnome("fedora"),
			contains: []string{"sudo dnf install gnome-shell-extension-appindicator", "gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com"},
		},
		{
			name:     "GNOME elsewhere: extensions.gnome.org",
			desktop:  gnome("nixos"),
			contains: []string{"https://extensions.gnome.org/extension/615/", "appindicatorsupport@rgcjonas.gmail.com"},
		},
		{
			name:     "installed after login",
			desktop:  gnome("debian"),
			ext:      Extension{UUID: UbuntuExtensionUUID, Installed: true},
			contains: []string{"not loaded yet", "Log out and back in", "gnome-extensions enable ubuntu-appindicators@ubuntu.com"},
		},
		{
			name:     "turned off",
			desktop:  gnome("fedora"),
			ext:      Extension{UUID: UpstreamExtensionUUID, Installed: true, Loaded: true},
			contains: []string{"turned off", "gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com"},
		},
		{
			name:     "extensions switched off as a whole",
			desktop:  gnome("debian"),
			ext:      Extension{UUID: UbuntuExtensionUUID, Installed: true, Loaded: true, Enabled: true},
			contains: []string{"disable-user-extensions false"},
		},
		{
			name:     "failed to start",
			desktop:  gnome("debian"),
			ext:      Extension{UUID: UbuntuExtensionUUID, Installed: true, Loaded: true, Enabled: true, Failed: true},
			contains: []string{"failed to start", "journalctl"},
		},
		{name: "KDE", desktop: Desktop{Names: []string{"KDE"}}, contains: []string{"System Tray"}},
		{name: "sway", desktop: Desktop{Names: []string{"sway"}}, contains: []string{"waybar"}},
		{name: "unknown", desktop: Desktop{}, contains: []string{"penguintier doctor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := AdviceFor(tt.desktop, tt.ext).Text()
			for _, want := range tt.contains {
				if !strings.Contains(text, want) {
					t.Errorf("advice lacks %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestAdviceText(t *testing.T) {
	a := Advice{Problem: "No tray.", Steps: []string{"Do this", "Then that"}}
	if got, want := a.Text(), "No tray.\n1. Do this\n2. Then that"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestWaitMissing(t *testing.T) {
	const grace, interval = 50 * time.Millisecond, 5 * time.Millisecond

	// Missing all the time: true after about the grace period.
	start := time.Now()
	if !WaitMissing(t.Context(), func() bool { return false }, grace, interval) {
		t.Fatal("WaitMissing = false with no host")
	}
	if took := time.Since(start); took < grace {
		t.Errorf("returned after %v, before the %v grace period", took, grace)
	}

	// A host that is there: waits until the context ends.
	ctx, cancel := context.WithTimeout(t.Context(), 4*grace)
	defer cancel()
	if WaitMissing(ctx, func() bool { return true }, grace, interval) {
		t.Error("WaitMissing = true with a host")
	}

	// A host that blinks back in time resets the count. ready runs on this
	// goroutine only, but atomic keeps the race detector happy if that
	// ever changes.
	var calls atomic.Int32
	blinking := func() bool { return calls.Add(1)%5 == 0 } // back every 25 ms < grace
	ctx2, cancel2 := context.WithTimeout(t.Context(), 4*grace)
	defer cancel2()
	if WaitMissing(ctx2, blinking, grace, interval) {
		t.Error("WaitMissing = true although the host keeps coming back")
	}
}

func TestNoticeMarker(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if NoticeShown("login1:20") {
		t.Fatal("NoticeShown before RememberNoticeShown")
	}
	if err := RememberNoticeShown("login1:20"); err != nil {
		t.Fatal(err)
	}
	if !NoticeShown("login1:20") {
		t.Error("NoticeShown = false in the same login")
	}
	if NoticeShown("login1:21") {
		t.Error("NoticeShown = true in the next login")
	}
	if NoticeShown("") || RememberNoticeShown("") == nil {
		t.Error("an empty login key should never count as shown")
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	if NoticeShown("login1:20") || RememberNoticeShown("login1:20") == nil {
		t.Error("without XDG_RUNTIME_DIR the marker should be unavailable")
	}
}

// TestLoginKey: logind's display session when there is one, else the
// session bus's ID.
func TestLoginKey(t *testing.T) {
	bus := resolvedtest.StartTestBus(t) // plays both the session and the system bus
	client, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// No logind on this bus: the bus's own ID.
	key := loginKey(t.Context(), client, bus.Address)
	if !strings.HasPrefix(key, "bus:") || len(key) <= len("bus:") {
		t.Errorf("without logind: key %q, want bus:<id>", key)
	}

	// A fake logind whose user has graphical session 20.
	logind, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logind.Close() })
	display := struct {
		ID   string
		Path dbus.ObjectPath
	}{"20", "/org/freedesktop/login1/session/_320"}
	if _, err := prop.Export(logind, login1UserSelf, prop.Map{login1UserIface: {"Display": {Value: display}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := logind.RequestName(login1Name, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if key := loginKey(t.Context(), client, bus.Address); key != "login1:20" {
		t.Errorf("with logind: key %q, want login1:20", key)
	}
}

// TestCheck runs Check against a fake watcher on a private bus.
func TestCheck(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	client, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	status, err := Check(t.Context(), client)
	if err != nil || status.Ready() || status.Watcher {
		t.Fatalf("no watcher: Check = %+v, %v; want not ready, no error", status, err)
	}

	watcherConn, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcherConn.Close() })
	props, err := prop.Export(watcherConn, watcherPath, prop.Map{
		WatcherName: {"IsStatusNotifierHostRegistered": {Value: false, Writable: true, Emit: prop.EmitFalse}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply, err := watcherConn.RequestName(WatcherName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("take %s: %v %v", WatcherName, reply, err)
	}

	status, err = Check(t.Context(), client)
	if err != nil || !status.Watcher || status.HostRegistered || status.Ready() {
		t.Errorf("watcher without host: Check = %+v, %v", status, err)
	}
	if status.Program == "" {
		t.Error("Program is empty; want the test binary's name")
	}

	props.SetMust(WatcherName, "IsStatusNotifierHostRegistered", true)
	if status, err = Check(t.Context(), client); err != nil || !status.Ready() {
		t.Errorf("watcher with host: Check = %+v, %v; want ready", status, err)
	}
}

// fakeShell answers GetExtensionInfo like GNOME Shell does.
type fakeShell struct {
	infos map[string]map[string]dbus.Variant
}

func (s *fakeShell) GetExtensionInfo(uuid string) (map[string]dbus.Variant, *dbus.Error) {
	if info, ok := s.infos[uuid]; ok {
		return info, nil
	}
	return map[string]dbus.Variant{}, nil // GNOME Shell's answer for unknown extensions
}

func TestCheckExtension(t *testing.T) {
	// The extension folders: one empty user folder and one system folder.
	userDir, systemDir := t.TempDir(), t.TempDir()
	old := extensionDirs
	extensionDirs = func() []string { return []string{userDir, systemDir} }
	t.Cleanup(func() { extensionDirs = old })

	bus := resolvedtest.StartTestBus(t)
	client, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// No GNOME Shell, nothing on disk.
	if ext := CheckExtension(t.Context(), client); ext.Installed || ext.UUID != "" {
		t.Errorf("nothing installed: %+v", ext)
	}

	// On disk only (installed after login).
	if err := os.MkdirAll(filepath.Join(systemDir, UpstreamExtensionUUID), 0o755); err != nil {
		t.Fatal(err)
	}
	if ext := CheckExtension(t.Context(), client); !ext.Installed || ext.Loaded || ext.UUID != UpstreamExtensionUUID {
		t.Errorf("on disk only: %+v", ext)
	}

	// GNOME Shell knows the Ubuntu one, turned off.
	shellConn, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shellConn.Close() })
	shell := &fakeShell{infos: map[string]map[string]dbus.Variant{
		UbuntuExtensionUUID: {"enabled": dbus.MakeVariant(false), "state": dbus.MakeVariant(float64(2))},
	}}
	if err := shellConn.Export(shell, shellPath, shellExtensionsIface); err != nil {
		t.Fatal(err)
	}
	if _, err := shellConn.RequestName(shellName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	ext := CheckExtension(t.Context(), client)
	want := Extension{UUID: UbuntuExtensionUUID, Installed: true, Loaded: true}
	if ext != want {
		t.Errorf("known and off: %+v, want %+v", ext, want)
	}
}
