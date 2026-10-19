package dnsstatus

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/dnsrule"
	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// The units penguintier-dnsd runs as (packaging/systemd/).
const (
	DaemonUnit = "penguintier-dnsd.service"
	PathUnit   = "penguintier-dnsd.path"
)

// Names on the system bus, from org.freedesktop.systemd1(5) and
// org.freedesktop.network1(5).
const (
	systemdBusName   = "org.freedesktop.systemd1"
	systemdPath      = dbus.ObjectPath("/org/freedesktop/systemd1")
	systemdManager   = "org.freedesktop.systemd1.Manager"
	systemdUnit      = "org.freedesktop.systemd1.Unit"
	networkdBusName  = "org.freedesktop.network1"
	networkdPath     = dbus.ObjectPath("/org/freedesktop/network1")
	networkdManager  = "org.freedesktop.network1.Manager"
	networkdLink     = "org.freedesktop.network1.Link"
	propertiesGet    = "org.freedesktop.DBus.Properties.Get"
	nameHasOwnerCall = "org.freedesktop.DBus.NameHasOwner"
)

// readTimeout limits one Read. The services answer in milliseconds; this
// only matters if one hangs, and the tray must not hang with it.
const readTimeout = 2 * time.Second

// UnitState is a systemd unit's state, as `systemctl status` shows it.
type UnitState struct {
	// LoadState is "loaded", or "not-found" when the unit is not installed.
	LoadState string `json:"loadState"`
	// ActiveState is "active", "inactive", "failed", "activating", ...
	ActiveState string `json:"activeState"`
	// SubState is the unit type's own detail: "running", "waiting",
	// "dead", ...
	SubState string `json:"subState"`
}

// Running reports whether the unit is up.
func (u UnitState) Running() bool {
	return u.ActiveState == "active" || u.ActiveState == "reloading"
}

// Installed reports whether systemd found the unit file.
func (u UnitState) Installed() bool {
	return u.LoadState != "" && u.LoadState != "not-found"
}

// Probe reads the facts behind each network's DNS state from the system
// bus. The zero value is ready to use. It connects on first use and keeps
// the connections until Close.
//
// A Probe is not safe for use by several goroutines at once; in the tray
// only the controller goroutine uses it.
type Probe struct {
	// Address is the D-Bus address to connect to. Empty means the system
	// bus. Tests set it to a private bus.
	Address string
	// IfIndex looks up an interface's index. nil means resolved.IfIndex
	// (which reads /sys/class/net); tests replace it.
	IfIndex func(name string) (int, error)

	// Two connections: internal/resolved opens its own, and this package
	// needs one for systemd and systemd-networkd.
	conn     *dbus.Conn
	resolved *resolved.Client
}

// Close closes the probe's connections. It may be used again afterwards;
// it then connects again.
func (p *Probe) Close() {
	if p.conn != nil {
		_ = p.conn.Close() // nothing useful to do if closing fails
		p.conn = nil
	}
	if p.resolved != nil {
		_ = p.resolved.Close()
		p.resolved = nil
	}
}

// connect opens the connections, or opens them again when the bus went
// away (dbus-daemon restarted, for example).
func (p *Probe) connect() error {
	if p.conn != nil && !p.conn.Connected() {
		p.Close()
	}
	if p.resolved != nil {
		// Done is closed when resolved's connection is lost. A select with
		// a default case checks a channel without waiting.
		select {
		case <-p.resolved.Done():
			p.Close()
		default:
		}
	}
	if p.conn != nil && p.resolved != nil {
		return nil
	}
	p.Close() // start clean if only one of the two was open

	var err error
	if p.Address == "" {
		p.conn, err = dbus.ConnectSystemBus()
	} else {
		p.conn, err = dbus.Connect(p.Address)
	}
	if err != nil {
		p.conn = nil
		return fmt.Errorf("connect to the system D-Bus: %w", err)
	}
	if p.Address == "" {
		p.resolved, err = resolved.Connect()
	} else {
		p.resolved, err = resolved.ConnectAddress(p.Address)
	}
	if err != nil {
		p.Close()
		return err
	}
	return nil
}

// Read returns the DNS state of every network, by network ID.
//
// It only talks to D-Bus when at least one network qualifies (see
// internal/dnsrule), so it costs nothing for people who don't use
// ZeroTier DNS.
func (p *Probe) Read(ctx context.Context, networks []ztapi.Network) map[ztapi.NetworkID]State {
	states := map[ztapi.NetworkID]State{}
	var qualifying []ztapi.Network
	for _, n := range networks {
		if _, reason := dnsrule.Check(n); reason == dnsrule.Qualifies {
			qualifying = append(qualifying, n)
		} else {
			states[n.ID] = Evaluate(n, Facts{})
		}
	}
	if len(qualifying) == 0 {
		return states
	}

	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	base := Facts{}
	if running, err := p.ResolvedRunning(ctx); err != nil {
		base.Unreadable = true
	} else {
		base.ResolvedRunning = running
	}
	if !base.Unreadable && base.ResolvedRunning {
		// A unit we can't read counts as not running: Evaluate only says
		// so when the settings are not applied anyway.
		unit, _ := p.Unit(ctx, DaemonUnit) //nolint:errcheck // an error leaves unit empty, which means "not running"
		base.DaemonRunning = unit.Running()
	}

	for _, n := range qualifying {
		f := base // a copy: each network gets its own link facts
		if !f.Unreadable && f.ResolvedRunning {
			f.Link, f.LinkErr = p.link(ctx, n.PortDeviceName)
			f.Managed = p.managed(ctx, n.PortDeviceName)
		}
		states[n.ID] = Evaluate(n, f)
	}
	return states
}

// ResolvedRunning reports whether systemd-resolved is on the bus.
func (p *Probe) ResolvedRunning(ctx context.Context) (bool, error) {
	if err := p.connect(); err != nil {
		return false, err
	}
	return p.resolved.Available(ctx)
}

// Unit reads a systemd unit's state. A unit that is not installed has
// LoadState "not-found".
func (p *Probe) Unit(ctx context.Context, name string) (UnitState, error) {
	if err := p.connect(); err != nil {
		return UnitState{}, err
	}
	// LoadUnit returns the unit's object path, loading the unit file first
	// if systemd has not yet. Any user may call it; it changes nothing.
	var path dbus.ObjectPath
	err := p.conn.Object(systemdBusName, systemdPath).CallWithContext(ctx, systemdManager+".LoadUnit", 0, name).Store(&path)
	if err != nil {
		return UnitState{}, fmt.Errorf("ask systemd about %s: %w", name, err)
	}
	unit := p.conn.Object(systemdBusName, path)
	var state UnitState
	if err := getProperty(ctx, unit, systemdUnit, "LoadState", &state.LoadState); err != nil {
		return UnitState{}, fmt.Errorf("read the state of %s: %w", name, err)
	}
	if err := getProperty(ctx, unit, systemdUnit, "ActiveState", &state.ActiveState); err != nil {
		return UnitState{}, fmt.Errorf("read the state of %s: %w", name, err)
	}
	if err := getProperty(ctx, unit, systemdUnit, "SubState", &state.SubState); err != nil {
		return UnitState{}, fmt.Errorf("read the state of %s: %w", name, err)
	}
	return state, nil
}

// link reads what systemd-resolved has for the interface.
func (p *Probe) link(ctx context.Context, device string) (resolved.LinkDNS, error) {
	index, err := p.ifIndex(device)
	if err != nil {
		return resolved.LinkDNS{}, err
	}
	return p.resolved.Link(ctx, index)
}

// managed reports whether systemd-networkd manages the interface. Then
// systemd-resolved takes DNS settings for it from networkd only.
//
// This copies resolved's own test: networkd's "administrative state" of
// the link is something other than pending, initialized or unmanaged.
// When networkd is not running, nothing is managed.
func (p *Probe) managed(ctx context.Context, device string) bool {
	// Ask the bus first, and never let a call start networkd: on a
	// NetworkManager system, starting networkd could take over interfaces.
	var running bool
	err := p.conn.BusObject().CallWithContext(ctx, nameHasOwnerCall, 0, networkdBusName).Store(&running)
	if err != nil || !running {
		return false
	}
	index, err := p.ifIndex(device)
	if err != nil {
		return false
	}

	var name string
	var path dbus.ObjectPath
	// GetLinkByIndex wants an int32 ("i"); a plain Go int would be sent as
	// an int64. Real interface indexes are small.
	call := p.conn.Object(networkdBusName, networkdPath).CallWithContext(ctx, networkdManager+".GetLinkByIndex",
		dbus.FlagNoAutoStart, int32(index)) //nolint:gosec // interface indexes always fit in an int32
	if err := call.Store(&name, &path); err != nil {
		return false // networkd doesn't know the link: not managed
	}
	var state string
	if err := getProperty(ctx, p.conn.Object(networkdBusName, path), networkdLink, "AdministrativeState", &state); err != nil {
		return false
	}
	switch state {
	case "", "pending", "initialized", "unmanaged":
		return false
	default:
		return true // "configuring", "configured", "failed", "linger"
	}
}

// ifIndex looks up an interface index with p.IfIndex or resolved.IfIndex.
func (p *Probe) ifIndex(device string) (int, error) {
	if p.IfIndex != nil {
		return p.IfIndex(device)
	}
	return resolved.IfIndex(device)
}

// getProperty reads one D-Bus property into into (a pointer). It never
// starts the service it asks.
func getProperty(ctx context.Context, obj dbus.BusObject, iface, name string, into any) error {
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, propertiesGet, dbus.FlagNoAutoStart, iface, name).Store(&v); err != nil {
		return err
	}
	if err := v.Store(into); err != nil {
		return fmt.Errorf("property %s.%s (%s): %w", iface, name, v.Signature(), err)
	}
	return nil
}
