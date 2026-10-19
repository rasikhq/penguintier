package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/dnssetup"
	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// gatherTimeout bounds the whole report. Each part has its own, shorter
// timeout too (the API client 2 s, D-Bus reads 2 s).
const gatherTimeout = 15 * time.Second

// Facts is everything the checks look at, gathered by Gather. Errors are
// kept as values: a failed read is a fact too.
type Facts struct {
	Version string // Penguintier's version

	// The ZeroTier service.
	APIURL    string
	Health    ztapi.Health
	HealthErr error

	// The API token, as ztapi.FindToken found it.
	TokenPath     string // the file the token came from ("" if none)
	TokenErr      error
	TokenOwner    int         // the file's owner (user ID)
	TokenMode     fs.FileMode // the file's permission bits
	UserTokenPath string      // ~/.zeroTierOneAuthToken, for the fix
	MyUID         int

	// The API, with the token.
	Node        ztapi.NodeStatus
	NodeErr     error // ErrTokenMissing when there is no token
	Networks    []ztapi.Network
	NetworksErr error

	// The desktop.
	Desktop   watcher.Desktop
	Tray      watcher.Status
	TrayErr   error
	Extension watcher.Extension // GNOME only

	NotifyServer string // e.g. "gnome-shell 48.7"
	NotifyErr    error

	Clipboard []ClipboardTool

	// Authorizing.
	Pkexec       string // pkexec's path, "" if not installed
	PkexecSetuid bool   // pkexec only works with its setuid bit
	PolkitAgent  string // the authentication agent found running, "" if none
	Helper       string // penguintier-token-helper's path
	HelperErr    error

	// DNS.
	Resolved     bool // systemd-resolved is on the bus
	ResolvedErr  error
	ResolvedUnit dnsstatus.UnitState // systemd-resolved.service
	DaemonUnit   dnsstatus.UnitState // penguintier-dnsd.service
	PathUnit     dnsstatus.UnitState // penguintier-dnsd.path
	UnitErr      error
	DNS          map[ztapi.NetworkID]dnsstatus.State

	// "Enable DNS support…": whether this setup is supported (only read
	// while systemd-resolved is not running), and the last switch's
	// backup (nil when there was none).
	SetupVerdict *dnssetup.Verdict
	Switch       *dnssetup.Manifest
	SwitchErr    error
}

// ClipboardTool is one clipboard program and whether it can be used.
type ClipboardTool struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	// Display is the environment variable the tool needs:
	// WAYLAND_DISPLAY or DISPLAY.
	Display string `json:"display"`
	Usable  bool   `json:"usable"` // installed, and its display is there
}

// Options are what Gather needs from the program.
type Options struct {
	// Version is Penguintier's version.
	Version string
	// FindHelper finds a helper program the way the tray does
	// (tray.FindHelper).
	FindHelper func(name string) (string, error)
}

// Gather reads every fact. It never changes anything, never asks for a
// password, and works without root.
func Gather(ctx context.Context, opts Options) Facts {
	ctx, cancel := context.WithTimeout(ctx, gatherTimeout)
	defer cancel()

	f := Facts{Version: opts.Version, MyUID: os.Geteuid()}
	gatherZeroTier(ctx, &f)
	gatherDesktop(ctx, &f)
	gatherPolkit(&f, opts.FindHelper)
	gatherDNS(ctx, &f)
	return f
}

// gatherZeroTier reads the token, the service's health and the networks.
func gatherZeroTier(ctx context.Context, f *Facts) {
	f.UserTokenPath, _ = ztapi.UserTokenPath() //nolint:errcheck // without a home directory the fix just shows no path
	_, f.TokenPath, f.TokenErr = ztapi.FindToken()
	// An unsafe file is reported with its path in the error.
	var insecure *ztapi.InsecureTokenFileError
	if errors.As(f.TokenErr, &insecure) {
		f.TokenPath = insecure.Path
	}
	if f.TokenPath != "" {
		if info, err := os.Stat(f.TokenPath); err == nil {
			f.TokenMode = info.Mode().Perm()
			// info.Sys() holds the raw stat data; the type assertion
			// checks it is Linux's *syscall.Stat_t.
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				f.TokenOwner = int(st.Uid)
			}
		}
	}

	client, err := ztapi.Discover()
	if err != nil {
		// The token file is unusable, but /health needs no token.
		urls, urlErr := ztapi.APIURLs()
		if urlErr != nil {
			f.HealthErr = urlErr
			return
		}
		client = ztapi.New(urls[0], "")
	}
	f.APIURL = client.BaseURL()
	f.Health, f.HealthErr = client.Health(ctx)

	if !client.HasToken() {
		f.NodeErr = ztapi.ErrTokenMissing
		return
	}
	f.Node, f.NodeErr = client.Status(ctx)
	if f.NodeErr == nil {
		f.Networks, f.NetworksErr = client.Networks(ctx)
	}
}

// gatherDesktop reads the tray host, notification and clipboard facts.
func gatherDesktop(ctx context.Context, f *Facts) {
	f.Desktop = watcher.DetectDesktop()
	for _, t := range []struct{ name, display string }{
		{"wl-copy", "WAYLAND_DISPLAY"}, {"xclip", "DISPLAY"}, {"xsel", "DISPLAY"},
	} {
		_, err := exec.LookPath(t.name)
		tool := ClipboardTool{Name: t.name, Installed: err == nil, Display: t.display}
		tool.Usable = tool.Installed && os.Getenv(t.display) != ""
		f.Clipboard = append(f.Clipboard, tool)
	}

	// A private connection, closed when we are done.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		f.TrayErr = fmt.Errorf("connect to the session bus: %w", err)
		f.NotifyErr = f.TrayErr
		return
	}
	defer conn.Close()

	f.Tray, f.TrayErr = watcher.Check(ctx, conn)
	if f.Desktop.Is("GNOME") {
		f.Extension = watcher.CheckExtension(ctx, conn)
	}

	// GetServerInformation returns name, vendor, version and spec version.
	var name, vendor, ver, spec string
	err = conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx,
		"org.freedesktop.Notifications.GetServerInformation", 0).Store(&name, &vendor, &ver, &spec)
	if err != nil {
		f.NotifyErr = err
	} else {
		f.NotifyServer = name + " " + ver
	}
}

// polkitAgents are the process names (as in /proc/<pid>/comm, which keeps
// only the first 15 characters) of known polkit authentication agents.
// Several desktop shells have one built in.
var polkitAgents = []string{
	"gnome-shell",     // GNOME (built in)
	"cinnamon",        // Cinnamon (built in)
	"polkit-gnome-au", // polkit-gnome-authentication-agent-1
	"polkit-kde-auth", // polkit-kde-authentication-agent-1 (KDE Plasma)
	"polkit-mate-aut", // polkit-mate-authentication-agent-1
	"xfce-polkit",
	"lxpolkit",
	"lxqt-policykit-", // lxqt-policykit-agent
	"budgie-polkit-d", // budgie-polkit-dialog
	"cosmic-osd",      // COSMIC (built in)
	"hyprpolkitagent",
	"io.elementary.d", // io.elementary.desktop.agent-polkit
	"soteria",
	"ukui-polkit",
}

// gatherPolkit looks for pkexec, a polkit agent and the token helper.
func gatherPolkit(f *Facts, findHelper func(string) (string, error)) {
	if path, err := exec.LookPath("pkexec"); err == nil {
		f.Pkexec = path
		if info, err := os.Stat(path); err == nil {
			f.PkexecSetuid = info.Mode()&fs.ModeSetuid != 0
		}
	}
	f.PolkitAgent = findPolkitAgent("/proc", f.MyUID)
	if findHelper != nil {
		f.Helper, f.HelperErr = findHelper("penguintier-token-helper")
	}
}

// findPolkitAgent returns the name of a known polkit agent running as uid,
// or "". There is no D-Bus call that tells an ordinary user whether an
// agent is registered, so this looks at the running processes instead.
func findPolkitAgent(proc string, uid int) string {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue // not a process directory
		}
		info, err := e.Info()
		if err != nil {
			continue // the process has just ended
		}
		// A /proc/<pid> directory belongs to the process's user.
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(proc, e.Name(), "comm")) //nolint:gosec // a /proc path built from a number
		if err != nil {
			continue
		}
		name := string(comm)
		if len(name) > 0 && name[len(name)-1] == '\n' {
			name = name[:len(name)-1]
		}
		for _, agent := range polkitAgents {
			if name == agent {
				return name
			}
		}
	}
	return ""
}

// gatherDNS reads systemd-resolved's and penguintier-dnsd's state and each
// network's DNS status.
func gatherDNS(ctx context.Context, f *Facts) {
	probe := &dnsstatus.Probe{}
	defer probe.Close()

	f.Resolved, f.ResolvedErr = probe.ResolvedRunning(ctx)
	if f.ResolvedErr != nil {
		f.UnitErr = f.ResolvedErr
		return
	}
	// Read all three units even if one fails, and keep every error.
	var errs []error
	var err error
	f.ResolvedUnit, err = probe.Unit(ctx, "systemd-resolved.service")
	errs = append(errs, err)
	f.DaemonUnit, err = probe.Unit(ctx, dnsstatus.DaemonUnit)
	errs = append(errs, err)
	f.PathUnit, err = probe.Unit(ctx, dnsstatus.PathUnit)
	errs = append(errs, err)
	// errors.Join returns nil when every error is nil.
	f.UnitErr = errors.Join(errs...)
	if f.NetworksErr == nil && len(f.Networks) > 0 {
		f.DNS = probe.Read(ctx, f.Networks)
	}
	f.Switch, f.SwitchErr = dnssetup.Store{}.Load()
	if !f.Resolved {
		v := dnssetup.Check(dnssetup.Gather(ctx, dnssetup.Env{}))
		f.SetupVerdict = &v
	}
}
