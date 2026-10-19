package doctor

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rasikhq/penguintier/internal/dnssetup"
	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// ManualAuthorizeCommand copies the ZeroTier API token by hand, for when
// "Authorize Penguintier…" can't work (no polkit agent, for example).
// Only `cat` runs as root; `install` runs as the user, so the copy belongs
// to the user and is readable by nobody else (mode 600).
const ManualAuthorizeCommand = "sudo cat /var/lib/zerotier-one/authtoken.secret | install -m600 /dev/stdin ~/.zeroTierOneAuthToken"

// networkdFix tells systemd-networkd to leave ZeroTier's interfaces alone.
const networkdFix = `Tell systemd-networkd to leave ZeroTier interfaces alone: create
/etc/systemd/network/10-zerotier.network containing
  [Match]
  Name=zt*
  [Link]
  Unmanaged=yes
then run: sudo networkctl reload`

// Evaluate turns the facts into the report. It does no I/O.
func Evaluate(f Facts) Report {
	r := Report{
		SchemaVersion: SchemaVersion,
		Checks: []Check{
			versionCheck(f),
			serviceCheck(f),
			tokenCheck(f),
			authCheck(f),
			trayCheck(f),
			notifyCheck(f),
			clipboardCheck(f),
			polkitCheck(f),
			resolvedCheck(f),
			daemonCheck(f),
			dnsCheck(f),
			setupCheck(f),
		},
		Networks: networks(f),
	}
	r.OK = !r.Failed()
	return r
}

func versionCheck(f Facts) Check {
	return Check{ID: "penguintier.version", Title: "Penguintier", Status: Info, Summary: f.Version}
}

// serviceUp reports whether the ZeroTier service answered at all. An HTTP
// error (an old ZeroTier without /health) is still an answer.
func serviceUp(f Facts) bool {
	var httpErr *ztapi.HTTPError
	return f.HealthErr == nil || errors.As(f.HealthErr, &httpErr)
}

func serviceCheck(f Facts) Check {
	c := Check{ID: "zerotier.service", Title: "ZeroTier service", Details: map[string]any{"url": f.APIURL}}
	switch {
	case f.HealthErr == nil:
		c.Details["version"] = f.Health.Version
		c.Details["online"] = f.Health.Online
		c.Status, c.Summary = OK, fmt.Sprintf("ZeroTier %s, running at %s", f.Health.Version, f.APIURL)
		if !f.Health.Online {
			c.Status = Warn
			c.Summary += ", but offline"
			c.Fix = "The service can't reach ZeroTier's root servers. Check your internet connection and firewall (UDP port 9993)."
		}
	case serviceUp(f):
		c.Status, c.Summary = OK, "running at "+f.APIURL
	case errors.Is(f.HealthErr, ztapi.ErrNotInstalled):
		c.Status, c.Summary = Fail, "ZeroTier One is not installed"
		c.Fix = "Install it: https://www.zerotier.com/download/"
	case errors.Is(f.HealthErr, ztapi.ErrServiceDown):
		c.Status, c.Summary = Fail, "not running"
		c.Fix = "Start it: sudo systemctl enable --now zerotier-one"
	case errors.Is(f.HealthErr, ztapi.ErrTimeout):
		c.Status, c.Summary = Fail, "not responding"
		c.Fix = "Restart it: sudo systemctl restart zerotier-one"
	default:
		c.Status, c.Summary = Fail, f.HealthErr.Error()
	}
	if f.HealthErr != nil {
		c.Details["error"] = f.HealthErr.Error()
	}
	return c
}

func tokenCheck(f Facts) Check {
	c := Check{ID: "zerotier.token", Title: "API token", Details: map[string]any{"path": f.TokenPath}}
	if f.TokenPath != "" && f.TokenErr == nil {
		c.Details["mode"] = fmt.Sprintf("%04o", f.TokenMode)
		c.Details["owner"] = f.TokenOwner
	}
	authorize := "Click “Authorize Penguintier…” in the tray menu, or run:\n" + ManualAuthorizeCommand

	var insecure *ztapi.InsecureTokenFileError
	var invalid *ztapi.InvalidTokenError
	switch {
	case f.TokenErr == nil && f.TokenPath == ztapi.SystemTokenPath:
		c.Status, c.Summary = OK, "read from "+f.TokenPath+" (you may read the service's own token)"
	case f.TokenErr == nil:
		c.Status, c.Summary = OK, fmt.Sprintf("%s (yours, mode %04o)", f.TokenPath, f.TokenMode)
	case errors.Is(f.TokenErr, ztapi.ErrTokenMissing):
		c.Status, c.Summary = Fail, "not found, so Penguintier can't use ZeroTier"
		c.Fix = authorize
	case errors.As(f.TokenErr, &insecure):
		c.Status, c.Summary = Fail, insecure.Error()
		if insecure.Fixable() {
			c.Fix = "Make it private: chmod 600 " + insecure.Path
		} else {
			c.Fix = "Replace it with your own copy. " + authorize
		}
	case errors.As(f.TokenErr, &invalid):
		c.Status, c.Summary = Fail, invalid.Error()
		c.Fix = "Copy the token again. " + authorize
	default:
		c.Status, c.Summary = Fail, f.TokenErr.Error()
		c.Fix = "Check $" + ztapi.EnvTokenFile + " if you set it. " + authorize
	}
	if f.TokenErr != nil {
		c.Details["error"] = f.TokenErr.Error()
	}
	return c
}

func authCheck(f Facts) Check {
	c := Check{ID: "zerotier.auth", Title: "API access"}
	switch {
	case !serviceUp(f):
		c.Status, c.Summary = Skip, "not checked, because the service is not running"
	case f.TokenErr != nil || errors.Is(f.NodeErr, ztapi.ErrTokenMissing):
		c.Status, c.Summary = Skip, "not checked, because there is no usable token"
	case f.NodeErr == nil:
		online := "online"
		if !f.Node.Online {
			online = "offline"
		}
		c.Status, c.Summary = OK, fmt.Sprintf("the token works (node %s, %s)", f.Node.Address, online)
		c.Details = map[string]any{"address": f.Node.Address, "version": f.Node.Version, "online": f.Node.Online}
	case errors.Is(f.NodeErr, ztapi.ErrUnauthorized):
		c.Status, c.Summary = Fail, "ZeroTier rejected the token (a reinstall makes a new one)"
		c.Fix = "Get a fresh copy: click “Authorize Penguintier…” in the tray menu, or run:\n" + ManualAuthorizeCommand
	default:
		c.Status, c.Summary = Fail, f.NodeErr.Error()
	}
	return c
}

func trayCheck(f Facts) Check {
	c := Check{
		ID: "desktop.tray", Title: "Tray host",
		Details: map[string]any{"desktop": f.Desktop, "watcher": f.Tray},
	}
	if f.Desktop.Is("GNOME") {
		c.Details["appindicatorExtension"] = f.Extension
	}
	switch {
	case f.TrayErr != nil:
		c.Status, c.Summary = Skip, "not checked ("+f.TrayErr.Error()+")"
		c.Fix = "Run doctor inside your desktop session."
	case f.Tray.Ready():
		host := f.Tray.Program
		if host == "" {
			host = "a tray host"
		}
		c.Status, c.Summary = OK, fmt.Sprintf("%s shows tray icons on %s", host, f.Desktop)
	default:
		advice := watcher.AdviceFor(f.Desktop, f.Extension)
		c.Status, c.Summary = Fail, fmt.Sprintf("no tray host on %s, so Penguintier's icon is invisible", f.Desktop)
		c.Fix = advice.Text()
	}
	return c
}

func notifyCheck(f Facts) Check {
	c := Check{ID: "desktop.notifications", Title: "Notifications"}
	if f.NotifyErr != nil {
		c.Status, c.Summary = Warn, "no notification service, so Penguintier's messages only go to its log"
		c.Details = map[string]any{"error": f.NotifyErr.Error()}
		return c
	}
	c.Status, c.Summary = OK, f.NotifyServer
	return c
}

func clipboardCheck(f Facts) Check {
	c := Check{ID: "desktop.clipboard", Title: "Clipboard", Details: map[string]any{"tools": f.Clipboard}}
	var usable []string
	for _, t := range f.Clipboard {
		if t.Usable {
			usable = append(usable, t.Name)
		}
	}
	if len(usable) == 0 {
		c.Status, c.Summary = Warn, "no clipboard tool for this session, so copied text is shown in a notification instead"
		c.Fix = "Install wl-clipboard (Wayland) or xclip (X11)."
		return c
	}
	c.Status, c.Summary = OK, strings.Join(usable, ", ")
	return c
}

func polkitCheck(f Facts) Check {
	c := Check{
		ID: "polkit", Title: "Authorizing",
		Details: map[string]any{"pkexec": f.Pkexec, "agent": f.PolkitAgent, "helper": f.Helper},
	}
	byHand := "Or authorize by hand:\n" + ManualAuthorizeCommand
	switch {
	case f.Pkexec == "":
		c.Status, c.Summary = Warn, "pkexec is not installed, so “Authorize Penguintier…” can't work"
		c.Fix = "Install pkexec. It comes in the pkexec package on Debian and Ubuntu, and in polkit on Fedora. " + byHand
	case !f.PkexecSetuid:
		c.Status, c.Summary = Warn, f.Pkexec+" is not setuid root, so it can't ask for a password"
		c.Fix = "Reinstall pkexec. " + byHand
	case f.HelperErr != nil:
		c.Status, c.Summary = Warn, "penguintier-token-helper not found"
		c.Fix = "Reinstall the penguintier package. " + byHand
	case f.PolkitAgent == "":
		c.Status, c.Summary = Warn, "no polkit agent found running, so password prompts may not appear"
		c.Fix = "Start your desktop's polkit authentication agent. " + byHand
	default:
		c.Status, c.Summary = OK, fmt.Sprintf("pkexec, agent %s, helper %s", f.PolkitAgent, f.Helper)
	}
	return c
}

// countDNS counts the networks in each DNS state.
func countDNS(f Facts) map[dnsstatus.State]int {
	counts := map[dnsstatus.State]int{}
	for _, state := range f.DNS {
		counts[state]++
	}
	return counts
}

func resolvedCheck(f Facts) Check {
	c := Check{ID: "dns.resolved", Title: "systemd-resolved"}
	switch {
	case f.ResolvedErr != nil:
		c.Status, c.Summary = Skip, "not checked: "+f.ResolvedErr.Error()
		return c
	case f.Resolved:
		c.Status, c.Summary = OK, "running"
		return c
	}

	c.Summary = "not running"
	if !f.ResolvedUnit.Installed() {
		c.Summary = "not installed"
	}
	if countDNS(f)[dnsstatus.WaitingForResolved] == 0 {
		c.Status = Info
		c.Summary += " (no network asks for DNS)"
		return c
	}
	c.Status = Warn
	c.Summary += ", so ZeroTier DNS settings can't be applied"
	if f.SetupVerdict != nil && f.SetupVerdict.OK {
		c.Fix = "Click “Enable DNS support…” in the tray menu. It backs up the files it changes, " +
			"and undoes the switch by itself if names stop resolving."
		return c
	}
	// Penguintier won't switch this setup itself: say why, and how by hand.
	c.Fix = "Switching the computer's DNS to systemd-resolved by hand changes how every program looks up names, " +
		"so keep a copy of /etc/resolv.conf and /etc/nsswitch.conf first."
	if f.SetupVerdict != nil {
		c.Fix = f.SetupVerdict.Text()
	}
	return c
}

func daemonCheck(f Facts) Check {
	c := Check{
		ID: "dns.daemon", Title: "DNS daemon",
		Details: map[string]any{"service": f.DaemonUnit, "path": f.PathUnit},
	}
	switch {
	case f.UnitErr != nil:
		c.Status, c.Summary = Skip, "not checked: "+f.UnitErr.Error()
	case !f.DaemonUnit.Installed():
		c.Status, c.Summary = Warn, "penguintier-dnsd is not installed (it comes with the penguintier package)"
	case f.DaemonUnit.Running():
		c.Status, c.Summary = OK, "penguintier-dnsd.service is active"
	case countDNS(f)[dnsstatus.DaemonNotRunning] > 0:
		c.Status, c.Summary = Fail, "penguintier-dnsd.service is "+f.DaemonUnit.ActiveState+", so ZeroTier DNS is not applied"
		c.Fix = "Start it: sudo systemctl enable --now penguintier-dnsd.path penguintier-dnsd.service\nRead its log: journalctl -u penguintier-dnsd"
	case f.DaemonUnit.ActiveState == "failed":
		c.Status, c.Summary = Warn, "penguintier-dnsd.service failed"
		c.Fix = "See why: journalctl -u penguintier-dnsd\nRestart it: sudo systemctl restart penguintier-dnsd.service"
	default:
		c.Status = Info
		c.Summary = fmt.Sprintf("penguintier-dnsd.service is %s, and penguintier-dnsd.path is %s (%s)",
			f.DaemonUnit.ActiveState, f.PathUnit.ActiveState, f.PathUnit.SubState)
	}
	return c
}

func dnsCheck(f Facts) Check {
	c := Check{ID: "dns.networks", Title: "Network DNS"}
	if f.NetworksErr != nil || len(f.Networks) == 0 || f.DNS == nil {
		c.Status, c.Summary = Skip, "no networks to check"
		return c
	}
	counts := countDNS(f)
	c.Details = map[string]any{"counts": counts}

	// "2 applied, 1 not requested", in a fixed order.
	var parts []string
	for state, n := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", n, state.Text()))
	}
	sort.Strings(parts)
	c.Summary = strings.Join(parts, ", ")

	switch {
	case counts[dnsstatus.LinkManaged] > 0:
		c.Status, c.Fix = Fail, networkdFix
	case counts[dnsstatus.DaemonNotRunning] > 0:
		c.Status, c.Fix = Fail, "See “DNS daemon” above."
	case counts[dnsstatus.WaitingForResolved] > 0:
		c.Status, c.Fix = Warn, "See “systemd-resolved” above."
	case counts[dnsstatus.Unknown] > 0:
		c.Status = Warn
	default:
		c.Status = OK
	}
	return c
}

// setupCheck reports the state of "Enable DNS support…"'s last switch.
func setupCheck(f Facts) Check {
	c := Check{ID: "dns.setup", Title: "DNS switch", Details: map[string]any{"backup": dnssetup.BackupDir}}
	undo := "pkexec " + dnssetup.HelperPath + " undo"
	switch {
	case f.SwitchErr != nil:
		c.Status, c.Summary = Warn, f.SwitchErr.Error()
		return c
	case f.Switch == nil:
		c.Status, c.Summary = Info, "never used"
		return c
	}
	m := f.Switch
	c.Details["state"] = m.State
	c.Details["updated"] = m.Updated
	when := m.Updated.Local().Format("2 Jan 2006 15:04")
	switch m.State {
	case dnssetup.StateInProgress, dnssetup.StateUndoFailed:
		c.Status = Fail
		c.Summary = "the switch to systemd-resolved started on " + when + " did not finish"
		c.Fix = "Put the previous DNS files back: " + undo
	case dnssetup.StateEnabled:
		c.Status = Info
		c.Summary = "switched to systemd-resolved on " + when + ", with the previous files kept in " + dnssetup.BackupDir
		c.Fix = "To switch back: " + undo
	case dnssetup.StateRolledBack:
		c.Status, c.Summary = Info, "the switch on "+when+" was undone automatically because "+m.Message
	default:
		c.Status, c.Summary = Info, "switched back on "+when
	}
	return c
}

// networks lists the joined networks with their DNS status.
func networks(f Facts) []Network {
	var list []Network
	for _, n := range f.Networks {
		state := f.DNS[n.ID]
		list = append(list, Network{
			ID: n.ID.String(), Name: n.Name, Status: string(n.Status), Device: n.PortDeviceName,
			AllowDNS: n.AllowDNS, DNSDomain: n.DNS.Domain, DNS: state, DNSText: state.Text(),
		})
	}
	return list
}
