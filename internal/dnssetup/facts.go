package dnssetup

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
)

// DefaultCheckNames are tried in order to prove that DNS works, before and
// after the switch. deb.debian.org is the default package mirror, which
// any Debian-family machine that installs packages can resolve.
var DefaultCheckNames = []string{"deb.debian.org", "debian.org"}

// EnvCheckNames overrides DefaultCheckNames (comma-separated). Only tests
// use it: pkexec clears the environment, so it can't reach the helper
// that way.
const EnvCheckNames = "PENGUINTIER_DNS_CHECK_NAMES"

// lookupTimeout bounds one lookup.
const lookupTimeout = 5 * time.Second

// Env is where Gather looks. The zero value is the real system.
type Env struct {
	// Root is put in front of file paths; tests use a temporary folder.
	Root string
	// BusAddress is the system bus to ask; "" is the real one.
	BusAddress string
	// Lookup resolves a name; nil means getent (see Lookup).
	Lookup func(ctx context.Context, name string) error
}

// Gather reads the facts Check needs. It changes nothing and needs no
// privileges, so the tray runs it before asking for a password.
func Gather(ctx context.Context, env Env) Facts {
	osr := readOSRelease(env.Root)
	f := Facts{
		OSFamily:     osr.Family(),
		OSName:       osr.PrettyName,
		SystemdPID1:  pid1IsSystemd(env.Root),
		AptAvailable: haveCommand("apt-get") && haveCommand("dpkg"),
		Packages:     dpkgStatus(filepath.Join(env.Root, "/var/lib/dpkg/status"), watchedPackages),
		ResolvConf:   inspectResolvConf(filepath.Join(env.Root, "/etc/resolv.conf")),
	}

	gatherBus(ctx, env.BusAddress, &f)

	lookup := env.Lookup
	if lookup == nil {
		lookup = Lookup
	}
	for _, name := range CheckNames() {
		if err := lookup(ctx, name); err != nil {
			f.LookupErr = err
			continue
		}
		f.LookupName, f.LookupErr = name, nil
		break
	}

	if m, err := (Store{Root: env.Root}).Load(); err == nil && m != nil {
		f.Backup = m.State
	}
	return f
}

// CheckNames returns the names to prove DNS with: $PENGUINTIER_DNS_CHECK_NAMES,
// or DefaultCheckNames.
func CheckNames() []string {
	if v := os.Getenv(EnvCheckNames); v != "" {
		var names []string
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		return names
	}
	return DefaultCheckNames
}

// Lookup resolves name the way programs do, through the C library's name
// service (getent ahosts uses /etc/nsswitch.conf and /etc/resolv.conf
// just like a browser would). Go's own resolver would read
// /etc/resolv.conf itself, and caches it, which could hide the switch.
func Lookup(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	// "--" ends getent's options, so a name can never be taken for one.
	if err := exec.CommandContext(ctx, "getent", "ahosts", "--", name).Run(); err != nil {
		return fmt.Errorf("%s does not resolve: %w", name, err)
	}
	return nil
}

// gatherBus reads what the system bus knows: NetworkManager's DNS setup,
// and whether NetworkManager, systemd-networkd and systemd-resolved run.
// Without a bus everything stays false, which Check refuses.
func gatherBus(ctx context.Context, address string, f *Facts) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var conn *dbus.Conn
	var err error
	if address == "" {
		conn, err = dbus.ConnectSystemBus()
	} else {
		conn, err = dbus.Connect(address)
	}
	if err != nil {
		return
	}
	defer conn.Close()

	hasOwner := func(name string) bool {
		var owned bool
		err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, name).Store(&owned)
		return err == nil && owned
	}
	f.NetworkManager = hasOwner("org.freedesktop.NetworkManager")
	if f.NetworkManager {
		dns := conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager/DnsManager")
		f.NMMode = stringProperty(ctx, dns, "org.freedesktop.NetworkManager.DnsManager", "Mode")
		f.NMRcManager = stringProperty(ctx, dns, "org.freedesktop.NetworkManager.DnsManager", "RcManager")
	}
	f.Networkd = hasOwner("org.freedesktop.network1")
	f.ResolvedRunning = hasOwner("org.freedesktop.resolve1")

	// resolved may run without its bus name (the Debian race), so also ask
	// systemd about the unit.
	probe := &dnsstatus.Probe{Address: address}
	defer probe.Close()
	if unit, err := probe.Unit(ctx, "systemd-resolved.service"); err == nil && unit.Running() {
		f.ResolvedRunning = true
	}
	if unit, err := probe.Unit(ctx, "systemd-networkd.service"); err == nil && unit.Running() {
		f.Networkd = true
	}
}

// stringProperty reads a string property, or "" if that fails. It never
// starts the service.
func stringProperty(ctx context.Context, obj dbus.BusObject, iface, name string) string {
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", dbus.FlagNoAutoStart, iface, name).Store(&v); err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// readOSRelease reads /etc/os-release under root.
func readOSRelease(root string) watcher.OSRelease {
	if root == "" {
		return watcher.ReadOSRelease()
	}
	return watcher.ParseOSRelease(readFile(filepath.Join(root, "/etc/os-release")))
}

// pid1IsSystemd reports whether process 1 is systemd.
func pid1IsSystemd(root string) bool {
	return strings.TrimSpace(string(readFile(filepath.Join(root, "/proc/1/comm")))) == "systemd"
}

// haveCommand reports whether a program is on $PATH.
func haveCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// readFile returns a file's content, or nothing if it can't be read.
func readFile(path string) []byte {
	data, err := os.ReadFile(path) //nolint:gosec // fixed system paths (under a test root)
	if err != nil {
		return nil
	}
	return data
}

// dpkgStatus reads dpkg's database (/var/lib/dpkg/status) and returns the
// state of each wanted package: "installed", "config-files" or
// "not-installed". The file is a list of blank-line separated stanzas such
// as "Package: systemd-resolved" ... "Status: install ok installed"; the
// state is the last word of Status.
func dpkgStatus(path string, wanted []string) map[string]string {
	states := map[string]string{}
	for _, p := range wanted {
		states[p] = "not-installed"
	}
	pkg := ""
	scanner := bufio.NewScanner(bytes.NewReader(readFile(path)))
	// Some fields (package descriptions) are long; allow 1 MiB lines.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			pkg = ""
		case strings.HasPrefix(line, "Package: "):
			pkg = strings.TrimSpace(strings.TrimPrefix(line, "Package: "))
		case strings.HasPrefix(line, "Status: "):
			if _, ok := states[pkg]; !ok {
				continue
			}
			fields := strings.Fields(line)
			state := fields[len(fields)-1]
			// A package listed for several architectures counts as
			// installed when any of them is.
			if states[pkg] != "installed" {
				states[pkg] = state
			}
		}
	}
	return states
}

// inspectResolvConf describes /etc/resolv.conf.
func inspectResolvConf(path string) ResolvConfInfo {
	var info ResolvConfInfo
	st, err := os.Lstat(path)
	if err != nil {
		return info // missing, or not readable: not there for our purposes
	}
	info.Exists = true
	if st.Mode()&fs.ModeSymlink != 0 {
		info.Symlink, _ = os.Readlink(path) //nolint:errcheck // an unreadable link still counts as a link
		if info.Symlink == "" {
			info.Symlink = "(unreadable)"
		}
		return info
	}
	first, _, _ := strings.Cut(string(readFile(path)), "\n")
	info.GeneratedByNM = strings.TrimSpace(first) == "# Generated by NetworkManager"
	return info
}
