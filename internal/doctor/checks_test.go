package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// healthy returns the facts of a machine where everything works: GNOME on
// Debian, one network with DNS applied and one without DNS.
func healthy() Facts {
	active := dnsstatus.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	return Facts{
		Version:       "v0.1.0",
		APIURL:        "http://127.0.0.1:9993",
		Health:        ztapi.Health{Online: true, Version: "1.16.2"},
		TokenPath:     "/home/u/.zeroTierOneAuthToken",
		UserTokenPath: "/home/u/.zeroTierOneAuthToken",
		TokenMode:     0o600,
		TokenOwner:    1000,
		MyUID:         1000,
		Node:          ztapi.NodeStatus{Address: "a1b2c3d4e5", Online: true, Version: "1.16.2"},
		Networks: []ztapi.Network{
			{ID: "c0ffee1234000001", Name: "home-lab", Status: ztapi.StatusOK, PortDeviceName: "zt34000001", AllowDNS: true},
			{ID: "c0ffee1234000002", Name: "office", Status: ztapi.StatusOK, PortDeviceName: "zt34000002"},
		},
		Desktop:      watcher.Desktop{Names: []string{"GNOME"}, SessionType: "wayland", OS: watcher.OSRelease{ID: "debian"}},
		Tray:         watcher.Status{Watcher: true, HostRegistered: true, Program: "gnome-shell"},
		Extension:    watcher.Extension{UUID: watcher.UbuntuExtensionUUID, Installed: true, Loaded: true, Enabled: true, Active: true},
		NotifyServer: "gnome-shell 48.7",
		Clipboard:    []ClipboardTool{{Name: "wl-copy", Installed: true, Display: "WAYLAND_DISPLAY", Usable: true}},
		Pkexec:       "/usr/bin/pkexec",
		PkexecSetuid: true,
		PolkitAgent:  "gnome-shell",
		Helper:       "/usr/libexec/penguintier/penguintier-token-helper",
		Resolved:     true,
		ResolvedUnit: active,
		DaemonUnit:   active,
		PathUnit:     dnsstatus.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"},
		DNS: map[ztapi.NetworkID]dnsstatus.State{
			"c0ffee1234000001": dnsstatus.Applied,
			"c0ffee1234000002": dnsstatus.NotRequested,
		},
	}
}

// check returns the report's check with this ID.
func check(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q", id)
	return Check{}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		change     func(f *Facts)
		id         string // the check to look at
		wantStatus Status
		wantFix    string // must appear in the check's Fix
		wantFailed bool   // the report as a whole
	}{
		{name: "healthy", id: "dns.networks", wantStatus: OK},
		{
			name:       "token missing",
			change:     func(f *Facts) { f.TokenPath, f.TokenErr, f.NodeErr = "", ztapi.ErrTokenMissing, ztapi.ErrTokenMissing },
			id:         "zerotier.token",
			wantStatus: Fail,
			wantFix:    ManualAuthorizeCommand,
			wantFailed: true,
		},
		{
			name:       "token missing skips the API check",
			change:     func(f *Facts) { f.TokenPath, f.TokenErr, f.NodeErr = "", ztapi.ErrTokenMissing, ztapi.ErrTokenMissing },
			id:         "zerotier.auth",
			wantStatus: Skip,
			wantFailed: true,
		},
		{
			name: "token readable by others",
			change: func(f *Facts) {
				f.TokenErr = &ztapi.InsecureTokenFileError{Path: f.TokenPath, Mode: 0o644, OwnerUID: 1000, WantUID: 1000}
			},
			id:         "zerotier.token",
			wantStatus: Fail,
			wantFix:    "chmod 600 /home/u/.zeroTierOneAuthToken",
			wantFailed: true,
		},
		{
			name:       "token rejected",
			change:     func(f *Facts) { f.NodeErr = fmt.Errorf("GET /status: %w", ztapi.ErrUnauthorized) },
			id:         "zerotier.auth",
			wantStatus: Fail,
			wantFix:    ManualAuthorizeCommand,
			wantFailed: true,
		},
		{
			name:       "service down",
			change:     func(f *Facts) { f.HealthErr = ztapi.ErrServiceDown },
			id:         "zerotier.service",
			wantStatus: Fail,
			wantFix:    "systemctl enable --now zerotier-one",
			wantFailed: true,
		},
		{
			name:       "ZeroTier not installed",
			change:     func(f *Facts) { f.HealthErr = ztapi.ErrNotInstalled },
			id:         "zerotier.service",
			wantStatus: Fail,
			wantFix:    "zerotier.com/download",
			wantFailed: true,
		},
		{
			name:       "node offline",
			change:     func(f *Facts) { f.Health.Online = false },
			id:         "zerotier.service",
			wantStatus: Warn,
		},
		{
			name: "GNOME without the extension",
			change: func(f *Facts) {
				f.Tray = watcher.Status{}
				f.Extension = watcher.Extension{}
			},
			id:         "desktop.tray",
			wantStatus: Fail,
			wantFix:    "sudo apt install gnome-shell-extension-appindicator",
			wantFailed: true,
		},
		{
			name:       "GNOME with the extension turned off",
			change:     func(f *Facts) { f.Tray = watcher.Status{}; f.Extension.Enabled, f.Extension.Active = false, false },
			id:         "desktop.tray",
			wantStatus: Fail,
			wantFix:    "gnome-extensions enable ubuntu-appindicators@ubuntu.com",
			wantFailed: true,
		},
		{
			name:       "no session bus",
			change:     func(f *Facts) { f.TrayErr = errors.New("no session bus") },
			id:         "desktop.tray",
			wantStatus: Skip,
		},
		{name: "no notification service", change: func(f *Facts) { f.NotifyErr = errors.New("none") }, id: "desktop.notifications", wantStatus: Warn},
		{
			name:       "no clipboard tool",
			change:     func(f *Facts) { f.Clipboard = []ClipboardTool{{Name: "wl-copy", Display: "WAYLAND_DISPLAY"}} },
			id:         "desktop.clipboard",
			wantStatus: Warn,
			wantFix:    "wl-clipboard",
		},
		{name: "no pkexec", change: func(f *Facts) { f.Pkexec = "" }, id: "polkit", wantStatus: Warn, wantFix: ManualAuthorizeCommand},
		{name: "no polkit agent", change: func(f *Facts) { f.PolkitAgent = "" }, id: "polkit", wantStatus: Warn, wantFix: ManualAuthorizeCommand},
		{
			name: "no resolved, a network asks for DNS",
			change: func(f *Facts) {
				f.Resolved, f.ResolvedUnit = false, dnsstatus.UnitState{LoadState: "not-found"}
				f.DNS["c0ffee1234000001"] = dnsstatus.WaitingForResolved
			},
			id:         "dns.resolved",
			wantStatus: Warn,
			wantFix:    "sudo apt install systemd-resolved",
		},
		{
			name: "resolved installed but stopped",
			change: func(f *Facts) {
				f.Resolved, f.ResolvedUnit.ActiveState = false, "inactive"
				f.DNS["c0ffee1234000001"] = dnsstatus.WaitingForResolved
			},
			id:         "dns.resolved",
			wantStatus: Warn,
			wantFix:    "systemctl enable --now systemd-resolved",
		},
		{
			name: "no resolved, nobody needs it",
			change: func(f *Facts) {
				f.Resolved = false
				f.DNS["c0ffee1234000001"] = dnsstatus.NotRequested
			},
			id:         "dns.resolved",
			wantStatus: Info,
		},
		{
			name: "daemon not running",
			change: func(f *Facts) {
				f.DaemonUnit.ActiveState = "inactive"
				f.DNS["c0ffee1234000001"] = dnsstatus.DaemonNotRunning
			},
			id:         "dns.daemon",
			wantStatus: Fail,
			wantFix:    "systemctl enable --now penguintier-dnsd.path",
			wantFailed: true,
		},
		{
			name:       "daemon not installed",
			change:     func(f *Facts) { f.DaemonUnit = dnsstatus.UnitState{LoadState: "not-found", ActiveState: "inactive"} },
			id:         "dns.daemon",
			wantStatus: Warn,
		},
		{
			name:       "link managed by networkd",
			change:     func(f *Facts) { f.DNS["c0ffee1234000001"] = dnsstatus.LinkManaged },
			id:         "dns.networks",
			wantStatus: Fail,
			wantFix:    "Unmanaged=yes",
			wantFailed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := healthy()
			if tt.change != nil {
				tt.change(&f)
			}
			r := Evaluate(f)
			c := check(t, r, tt.id)
			if c.Status != tt.wantStatus {
				t.Errorf("%s: status %q (%s), want %q", tt.id, c.Status, c.Summary, tt.wantStatus)
			}
			if !strings.Contains(c.Fix, tt.wantFix) {
				t.Errorf("%s: fix %q lacks %q", tt.id, c.Fix, tt.wantFix)
			}
			if r.Failed() != tt.wantFailed || r.OK == tt.wantFailed {
				t.Errorf("Failed() = %t, OK = %t; want failed %t", r.Failed(), r.OK, tt.wantFailed)
			}
		})
	}
}

// TestHealthyReport: a healthy machine has no warnings at all.
func TestHealthyReport(t *testing.T) {
	r := Evaluate(healthy())
	for _, c := range r.Checks {
		if c.Status != OK && c.Status != Info {
			t.Errorf("%s: %s (%s)", c.ID, c.Status, c.Summary)
		}
	}
}

// TestCheckIDs: the IDs are part of the JSON output, so they are fixed.
// Add new ones at the end; never rename one.
func TestCheckIDs(t *testing.T) {
	want := []string{
		"penguintier.version", "zerotier.service", "zerotier.token", "zerotier.auth",
		"desktop.tray", "desktop.notifications", "desktop.clipboard", "polkit",
		"dns.resolved", "dns.daemon", "dns.networks",
	}
	r := Evaluate(healthy())
	var got []string
	for _, c := range r.Checks {
		got = append(got, c.ID)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("check IDs:\n%q\nwant\n%q", got, want)
	}
}

func TestWriteJSON(t *testing.T) {
	f := healthy()
	f.TokenPath, f.TokenErr, f.NodeErr = "", ztapi.ErrTokenMissing, ztapi.ErrTokenMissing
	var buf bytes.Buffer
	if err := Evaluate(f).WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}

	// Decode into a loose map: this is how a script sees the output.
	var out struct {
		SchemaVersion int  `json:"schemaVersion"`
		OK            bool `json:"ok"`
		Checks        []struct {
			ID, Status, Fix string
		} `json:"checks"`
		Networks []struct {
			ID  string `json:"id"`
			DNS string `json:"dns"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if out.SchemaVersion != 1 || out.OK {
		t.Errorf("schemaVersion %d, ok %t; want 1, false", out.SchemaVersion, out.OK)
	}
	if len(out.Networks) != 2 || out.Networks[0].DNS != "applied" {
		t.Errorf("networks = %+v", out.Networks)
	}
	if !strings.Contains(buf.String(), "| install -m600 /dev/stdin") {
		t.Errorf("the manual command is escaped or missing:\n%s", buf.String())
	}
}

func TestWriteText(t *testing.T) {
	f := healthy()
	f.TokenPath, f.TokenErr, f.NodeErr = "", ztapi.ErrTokenMissing, ztapi.ErrTokenMissing
	var buf bytes.Buffer
	if err := Evaluate(f).WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	for _, want := range []string{
		"  FAIL  API token ",
		// Fix lines start under the summaries: 2 + 4 + 2 spaces, the
		// longest title ("systemd-resolved"), 2 spaces.
		"\n" + strings.Repeat(" ", 2+4+2+len("systemd-resolved")+2) + ManualAuthorizeCommand + "\n",
		"  skip  API access ",
		"  home-lab (c0ffee1234000001)  OK  zt34000001  DNS: applied\n",
		"  office (c0ffee1234000002)    OK  zt34000002  DNS: not requested",
		"\n1 problem found.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
}

func TestFindPolkitAgent(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, comm string) {
		dir := filepath.Join(proc, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100", "bash")
	write("self", "gnome-shell") // not a number: skipped
	me := os.Geteuid()           // the temporary directories belong to us
	if got := findPolkitAgent(proc, me); got != "" {
		t.Errorf("found %q among bash only", got)
	}
	write("200", "polkit-kde-auth")
	if got := findPolkitAgent(proc, me); got != "polkit-kde-auth" {
		t.Errorf("found %q, want polkit-kde-auth", got)
	}
	if got := findPolkitAgent(proc, me+1); got != "" {
		t.Errorf("found %q for another user", got)
	}
}
