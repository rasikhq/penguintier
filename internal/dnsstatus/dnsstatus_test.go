package dnsstatus

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// qualifying is a network whose DNS settings penguintier-dnsd applies.
var qualifying = ztapi.Network{
	ID:             "8056c2e21c000001",
	Name:           "selftest",
	Status:         ztapi.StatusOK,
	PortDeviceName: "zt3jn5bvfq",
	AllowDNS:       true,
	DNS:            ztapi.DNS{Domain: "pt.test", Servers: []string{"10.147.17.1"}},
}

// What resolved reports for the link once the daemon applied it, and
// what another program might have set.
var (
	appliedLink = resolved.LinkDNS{
		Servers: []netip.Addr{netip.MustParseAddr("10.147.17.1")},
		Domains: []resolved.Domain{{Name: "pt.test", RoutingOnly: true}},
	}
	foreignLink = resolved.LinkDNS{
		Servers:      []netip.Addr{netip.MustParseAddr("192.0.2.53")},
		Domains:      []resolved.Domain{{Name: "corp.example"}},
		DefaultRoute: true,
	}
)

func TestEvaluate(t *testing.T) {
	up := Facts{ResolvedRunning: true, DaemonRunning: true}

	tests := []struct {
		name    string
		network func(n *ztapi.Network) // nil: qualifying as it is
		facts   Facts
		want    State
	}{
		{name: "applied", facts: with(up, func(f *Facts) { f.Link = appliedLink }), want: Applied},
		{
			name: "applied, domain reported in upper case with a dot",
			facts: with(up, func(f *Facts) {
				f.Link = appliedLink
				f.Link.Domains = []resolved.Domain{{Name: "PT.test.", RoutingOnly: true}}
			}),
			want: Applied,
		},
		{name: "applied even if the daemon just stopped", facts: Facts{ResolvedRunning: true, Link: appliedLink}, want: Applied},
		{name: "resolved not running", facts: Facts{DaemonRunning: true}, want: WaitingForResolved},
		{name: "daemon not running", facts: Facts{ResolvedRunning: true}, want: DaemonNotRunning},
		{name: "link managed by networkd", facts: with(up, func(f *Facts) { f.Managed = true }), want: LinkManaged},
		{name: "managed wins over daemon off", facts: Facts{ResolvedRunning: true, Managed: true}, want: LinkManaged},
		{name: "someone else's settings", facts: with(up, func(f *Facts) { f.Link = foreignLink }), want: SetByOther},
		{name: "search domain instead of routing-only is not ours", facts: with(up, func(f *Facts) {
			f.Link = appliedLink
			f.Link.Domains = []resolved.Domain{{Name: "pt.test"}}
		}), want: SetByOther},
		{name: "empty link: the daemon is about to apply", facts: up, want: Pending},
		{name: "link not known yet", facts: with(up, func(f *Facts) { f.LinkErr = resolved.ErrNoSuchLink }), want: Pending},
		{name: "facts unreadable", facts: Facts{Unreadable: true}, want: Unknown},

		{name: "allowDNS off", network: func(n *ztapi.Network) { n.AllowDNS = false }, facts: up, want: NotRequested},
		{name: "not connected", network: func(n *ztapi.Network) { n.Status = ztapi.StatusAccessDenied }, facts: up, want: NotConnected},
		{name: "no DNS from the controller", network: func(n *ztapi.Network) { n.DNS = ztapi.DNS{} }, facts: up, want: NoDNS},
		{
			name:    "allowDNS off ignores the facts",
			network: func(n *ztapi.Network) { n.AllowDNS = false },
			facts:   Facts{Unreadable: true},
			want:    NotRequested,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := qualifying
			if tt.network != nil {
				tt.network(&n)
			}
			if got := Evaluate(n, tt.facts); got != tt.want {
				t.Errorf("Evaluate() = %q, want %q", got, tt.want)
			}
		})
	}
}

// with returns a copy of f changed by change.
func with(f Facts, change func(f *Facts)) Facts {
	change(&f)
	return f
}

func TestStateText(t *testing.T) {
	// Every state has its own text, and only the two faults are broken.
	states := []State{Applied, WaitingForResolved, DaemonNotRunning, LinkManaged, SetByOther, Pending, Unknown, NotRequested, NotConnected, NoDNS}
	seen := map[string]State{}
	for _, s := range states {
		text := s.Text()
		if other, dup := seen[text]; dup {
			t.Errorf("%q and %q both read %q", s, other, text)
		}
		seen[text] = s
		wantBroken := s == DaemonNotRunning || s == LinkManaged
		if s.Broken() != wantBroken {
			t.Errorf("%q.Broken() = %t, want %t", s, s.Broken(), wantBroken)
		}
	}
}

// fakeSystemd answers LoadUnit and serves each unit's state, like
// systemd's org.freedesktop.systemd1 does.
type fakeSystemd struct {
	conn  *dbus.Conn
	units map[string]UnitState // by unit name; others are "not-found"
}

// LoadUnit is called over D-Bus. godbus exports every method of the
// object; a method's last result must be a *dbus.Error (nil = success).
func (s *fakeSystemd) LoadUnit(name string) (dbus.ObjectPath, *dbus.Error) {
	state, ok := s.units[name]
	if !ok {
		state = UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
	}
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/systemd1/unit/u%d", len(name)))
	_, err := prop.Export(s.conn, path, prop.Map{
		systemdUnit: {
			"LoadState":   {Value: state.LoadState},
			"ActiveState": {Value: state.ActiveState},
			"SubState":    {Value: state.SubState},
		},
	})
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return path, nil
}

// fakeNetworkd answers GetLinkByIndex, like systemd-networkd's
// org.freedesktop.network1 does.
//
// godbus runs each incoming call in its own goroutine, while the test
// changes states from the test goroutine, so a mutex guards states.
type fakeNetworkd struct {
	conn   *dbus.Conn
	mu     sync.Mutex
	states map[int32]string // ifindex → AdministrativeState
}

func (n *fakeNetworkd) setState(index int32, state string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.states[index] = state
}

func (n *fakeNetworkd) GetLinkByIndex(index int32) (string, dbus.ObjectPath, *dbus.Error) {
	n.mu.Lock()
	state, ok := n.states[index]
	n.mu.Unlock()
	if !ok {
		return "", "", dbus.NewError("org.freedesktop.network1.NoSuchLink", []any{"no such link"})
	}
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/network1/link/_3%d", index))
	if _, err := prop.Export(n.conn, path, prop.Map{networkdLink: {"AdministrativeState": {Value: state}}}); err != nil {
		return "", "", dbus.MakeFailedError(err)
	}
	return "zt3jn5bvfq", path, nil
}

// connectTestBus opens a connection to the private bus for a fake
// service; it is closed when the test ends.
func connectTestBus(t *testing.T, address string) *dbus.Conn {
	t.Helper()
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// serve exports obj under iface at path on conn, then takes busName, so
// clients only see the name once the object is there.
func serve(t *testing.T, conn *dbus.Conn, busName string, path dbus.ObjectPath, iface string, obj any) {
	t.Helper()
	if err := conn.Export(obj, path, iface); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("take %s: reply %v, err %v", busName, reply, err)
	}
}

// TestProbeRead reads every fact over a private bus, from a fake
// resolved, systemd and networkd.
func TestProbeRead(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	const ifindex = 12

	probe := &Probe{
		Address: bus.Address,
		IfIndex: func(name string) (int, error) {
			if name == qualifying.PortDeviceName {
				return ifindex, nil
			}
			return 0, fmt.Errorf("interface %s: %w", name, resolved.ErrNoInterface)
		},
	}
	t.Cleanup(probe.Close)
	off := qualifying
	off.ID, off.AllowDNS = "8056c2e21c000002", false
	networks := []ztapi.Network{qualifying, off}

	check := func(step string, want State) {
		t.Helper()
		states := probe.Read(t.Context(), networks)
		if states[qualifying.ID] != want || states[off.ID] != NotRequested {
			t.Errorf("%s: states = %v, want %s for %s and %s for %s", step, states, want, qualifying.ID, NotRequested, off.ID)
		}
	}

	// No resolved on the bus yet.
	check("no resolved", WaitingForResolved)

	fake, err := resolvedtest.NewFake(bus.Address, map[int]string{ifindex: qualifying.PortDeviceName})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	// Resolved runs, but systemd doesn't know the daemon.
	check("no daemon unit", DaemonNotRunning)

	systemd := &fakeSystemd{conn: connectTestBus(t, bus.Address), units: map[string]UnitState{
		DaemonUnit: {LoadState: "loaded", ActiveState: "active", SubState: "running"},
	}}
	serve(t, systemd.conn, systemdBusName, systemdPath, systemdManager, systemd)
	check("daemon running, link empty", Pending)

	unit, err := probe.Unit(t.Context(), DaemonUnit)
	if err != nil || !unit.Running() || !unit.Installed() {
		t.Errorf("Unit(%s) = %+v, %v; want loaded and active", DaemonUnit, unit, err)
	}
	if unit, err := probe.Unit(t.Context(), "missing.service"); err != nil || unit.Installed() {
		t.Errorf("Unit(missing.service) = %+v, %v; want not-found", unit, err)
	}

	fake.SetLinkState(ifindex, appliedLink.Servers, appliedLink.Domains)
	check("applied", Applied)

	fake.SetLinkState(ifindex, nil, nil)
	networkd := &fakeNetworkd{conn: connectTestBus(t, bus.Address), states: map[int32]string{ifindex: "configured"}}
	serve(t, networkd.conn, networkdBusName, networkdPath, networkdManager, networkd)
	check("managed by networkd", LinkManaged)

	networkd.setState(ifindex, "unmanaged")
	check("networkd runs but leaves the link alone", Pending)

	fake.Close() // resolved stops
	check("resolved stopped", WaitingForResolved)
}

// TestProbeReadNoQualifyingNetworks: without a network that asks for DNS,
// Read never touches the bus (here: there is none to touch).
func TestProbeReadNoQualifyingNetworks(t *testing.T) {
	probe := &Probe{Address: "unix:path=/nonexistent/bus"}
	off := qualifying
	off.AllowDNS = false
	states := probe.Read(t.Context(), []ztapi.Network{off})
	if states[off.ID] != NotRequested {
		t.Errorf("state = %q, want %q", states[off.ID], NotRequested)
	}
	if probe.conn != nil {
		t.Error("Read connected to the bus although no network qualifies")
	}
}

// TestProbeReadNoBus: an unreachable bus gives Unknown, not a hang or a
// crash.
func TestProbeReadNoBus(t *testing.T) {
	probe := &Probe{Address: "unix:path=/nonexistent/bus"}
	states := probe.Read(t.Context(), []ztapi.Network{qualifying})
	if states[qualifying.ID] != Unknown {
		t.Errorf("state = %q, want %q", states[qualifying.ID], Unknown)
	}
	if _, err := probe.ResolvedRunning(t.Context()); err == nil {
		t.Error("ResolvedRunning without a bus: no error")
	}
	if _, err := probe.Unit(t.Context(), DaemonUnit); err == nil {
		t.Error("Unit without a bus: no error")
	}
}
