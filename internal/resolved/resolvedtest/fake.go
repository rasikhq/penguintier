package resolvedtest

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/resolved"
)

// Fake is a fake systemd-resolved. It owns org.freedesktop.resolve1 on a
// (private) bus and answers the calls internal/resolved makes, keeping the
// per-link settings in memory. It copies resolved's behavior where our
// code depends on it:
//
//   - unknown ifindex: org.freedesktop.resolve1.NoSuchLink
//   - a link managed by systemd-networkd: org.freedesktop.resolve1.LinkBusy
//   - DefaultRoute reports the explicit setting, or else what is in
//     effect (false when the link has a routing-only domain)
//
// Use one Fake per test. It is safe for concurrent use: godbus runs every
// incoming method call in its own goroutine.
type Fake struct {
	conn *dbus.Conn

	// mu guards everything below. sync.Mutex is Go's lock: Lock() waits
	// until no one else holds it, and "defer f.mu.Unlock()" releases it
	// when the method returns.
	mu         sync.Mutex
	links      map[int32]*fakeLink
	calls      []string
	denyWrites bool
}

type fakeLink struct {
	name         string
	managed      bool
	servers      []netip.Addr
	domains      []resolved.Domain
	defaultRoute *bool // nil: never set, so the effective value is reported
}

// dnsAddress and dbusDomain are the D-Bus forms "(iay)" and "(sb)", the
// same as in internal/resolved.
type dnsAddress struct {
	Family  int32
	Address []byte
}

type dbusDomain struct {
	Name        string
	RoutingOnly bool
}

// NewFake connects to the bus at address, exports the Manager object and
// takes the name org.freedesktop.resolve1. links (ifindex to interface
// name, may be nil) are known from the start, before clients can see the
// name. Close stops it, which looks to clients like resolved stopping.
func NewFake(address string, links map[int]string) (*Fake, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("fake resolved: connect: %w", err)
	}
	f := &Fake{conn: conn, links: map[int32]*fakeLink{}}
	for ifindex, name := range links {
		f.AddLink(ifindex, name)
	}

	// Export makes the exported methods of a Go value callable over
	// D-Bus. godbus works out each method's D-Bus signature from the Go
	// parameter types.
	if err := conn.Export(manager{f}, resolved.ManagerPath, resolved.ManagerInterface); err != nil {
		_ = conn.Close() // already failing; nothing more to do with this error
		return nil, fmt.Errorf("fake resolved: export manager: %w", err)
	}
	reply, err := conn.RequestName(resolved.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		_ = conn.Close()
		return nil, fmt.Errorf("fake resolved: request name %s: reply %v, %w", resolved.BusName, reply, err)
	}
	return f, nil
}

// Close disconnects the fake. Clients see resolve1 lose its owner.
func (f *Fake) Close() {
	_ = f.conn.Close() // closing a test connection; nothing to report
}

// AddLink makes the fake know a link, with nothing configured.
func (f *Fake) AddLink(ifindex int, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[index32(ifindex)] = &fakeLink{name: name}
	if err := f.conn.Export(linkProps{f, index32(ifindex)}, linkPath(ifindex), "org.freedesktop.DBus.Properties"); err != nil {
		panic(err) // only fails for an invalid path, which linkPath never makes
	}
}

// RemoveLink forgets a link, like resolved does when the interface goes
// away.
func (f *Fake) RemoveLink(ifindex int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.links, index32(ifindex))
	_ = f.conn.Export(nil, linkPath(ifindex), "org.freedesktop.DBus.Properties") // nil un-exports
}

// SetManaged marks a link as managed by systemd-networkd: every write to it
// then fails with LinkBusy.
func (f *Fake) SetManaged(ifindex int, managed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.links[index32(ifindex)]; l != nil {
		l.managed = managed
	}
}

// DenyWrites makes every write fail with AccessDenied, as when the polkit
// rule is missing.
func (f *Fake) DenyWrites(deny bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denyWrites = deny
}

// SetLinkState changes a link's settings directly, as another program
// (NetworkManager, a future ZeroTier) would. Pass nil to clear a part.
// The DefaultRoute setting goes back to "never set".
func (f *Fake) SetLinkState(ifindex int, servers []netip.Addr, domains []resolved.Domain) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.links[index32(ifindex)]; l != nil {
		l.servers = slices.Clone(servers)
		l.domains = slices.Clone(domains)
		l.defaultRoute = nil
	}
}

// LinkState returns a link's settings. defaultRouteSet says whether
// DefaultRoute was set explicitly; defaultRoute is the effective value.
func (f *Fake) LinkState(ifindex int) (servers []netip.Addr, domains []resolved.Domain, defaultRoute, defaultRouteSet bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.links[index32(ifindex)]
	if l == nil {
		return nil, nil, false, false
	}
	return slices.Clone(l.servers), slices.Clone(l.domains), l.effectiveDefaultRoute(), l.defaultRoute != nil
}

// Calls returns the write calls made so far, in order, such as
// "SetLinkDNS 12 10.147.17.1" or "RevertLink 12". Reads are not listed.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// ResetCalls empties the list Calls returns.
func (f *Fake) ResetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// effectiveDefaultRoute copies resolved's rule: an explicit setting wins;
// otherwise a link with servers is a default route unless it has a
// routing-only domain.
func (l *fakeLink) effectiveDefaultRoute() bool {
	if l.defaultRoute != nil {
		return *l.defaultRoute
	}
	if len(l.servers) == 0 {
		return false
	}
	for _, d := range l.domains {
		if d.RoutingOnly {
			return false
		}
	}
	return true
}

// index32 converts an ifindex to the int32 D-Bus uses. Test ifindexes
// are small; a value that doesn't fit is a bug in the test.
func index32(ifindex int) int32 {
	if ifindex < 0 || ifindex > math.MaxInt32 {
		panic(fmt.Sprintf("ifindex %d out of range", ifindex))
	}
	return int32(ifindex)
}

// linkPath builds a link's object path the way resolved does: the ifindex
// is escaped as a bus label, and a leading digit d becomes "_3d".
func linkPath(ifindex int) dbus.ObjectPath {
	return dbus.ObjectPath("/org/freedesktop/resolve1/link/_3" + strconv.Itoa(ifindex))
}

// writableLink finds a link for a write call, or returns the error
// resolved would send. The caller holds f.mu.
func (f *Fake) writableLink(ifindex int32) (*fakeLink, *dbus.Error) {
	l := f.links[ifindex]
	if l == nil {
		return nil, dbus.NewError("org.freedesktop.resolve1.NoSuchLink", []any{fmt.Sprintf("Link %d not known", ifindex)})
	}
	if f.denyWrites {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.AccessDenied", []any{"Access denied"})
	}
	if l.managed {
		return nil, dbus.NewError("org.freedesktop.resolve1.LinkBusy", []any{fmt.Sprintf("Link %s is managed.", l.name)})
	}
	return l, nil
}

// manager is exported at /org/freedesktop/resolve1. Each method with a
// *dbus.Error as its last result becomes a D-Bus method; a non-nil
// *dbus.Error is sent back as an error reply.
type manager struct {
	f *Fake
}

func (m manager) GetLink(ifindex int32) (dbus.ObjectPath, *dbus.Error) {
	m.f.mu.Lock()
	defer m.f.mu.Unlock()
	if m.f.links[ifindex] == nil {
		return "", dbus.NewError("org.freedesktop.resolve1.NoSuchLink", []any{fmt.Sprintf("Link %d not known", ifindex)})
	}
	return linkPath(int(ifindex)), nil
}

func (m manager) SetLinkDNS(ifindex int32, addrs []dnsAddress) *dbus.Error {
	m.f.mu.Lock()
	defer m.f.mu.Unlock()
	l, dbusErr := m.f.writableLink(ifindex)
	if dbusErr != nil {
		return dbusErr
	}
	var servers []netip.Addr
	text := ""
	for _, a := range addrs {
		addr, err := parseAddress(a)
		if err != nil {
			return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{err.Error()})
		}
		servers = append(servers, addr)
		text += " " + addr.String()
	}
	l.servers = servers
	m.f.calls = append(m.f.calls, fmt.Sprintf("SetLinkDNS %d%s", ifindex, text))
	return nil
}

func (m manager) SetLinkDomains(ifindex int32, list []dbusDomain) *dbus.Error {
	m.f.mu.Lock()
	defer m.f.mu.Unlock()
	l, dbusErr := m.f.writableLink(ifindex)
	if dbusErr != nil {
		return dbusErr
	}
	var domains []resolved.Domain
	text := ""
	for _, d := range list {
		if d.Name == "" {
			return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Invalid search domain"})
		}
		domain := resolved.Domain(d)
		domains = append(domains, domain)
		text += " " + domain.String()
	}
	l.domains = domains
	m.f.calls = append(m.f.calls, fmt.Sprintf("SetLinkDomains %d%s", ifindex, text))
	return nil
}

func (m manager) SetLinkDefaultRoute(ifindex int32, enable bool) *dbus.Error {
	m.f.mu.Lock()
	defer m.f.mu.Unlock()
	l, dbusErr := m.f.writableLink(ifindex)
	if dbusErr != nil {
		return dbusErr
	}
	l.defaultRoute = &enable
	m.f.calls = append(m.f.calls, fmt.Sprintf("SetLinkDefaultRoute %d %t", ifindex, enable))
	return nil
}

func (m manager) RevertLink(ifindex int32) *dbus.Error {
	m.f.mu.Lock()
	defer m.f.mu.Unlock()
	l, dbusErr := m.f.writableLink(ifindex)
	if dbusErr != nil {
		return dbusErr
	}
	l.servers, l.domains, l.defaultRoute = nil, nil, nil
	m.f.calls = append(m.f.calls, fmt.Sprintf("RevertLink %d", ifindex))
	return nil
}

// linkProps answers org.freedesktop.DBus.Properties calls for one link.
type linkProps struct {
	f       *Fake
	ifindex int32
}

func (p linkProps) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != resolved.LinkInterface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	l := p.f.links[p.ifindex]
	if l == nil {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownObject", []any{"link is gone"})
	}
	// Empty (not nil) slices, so the variants still carry the right
	// signature, like resolved's empty arrays.
	addrs := make([]dnsAddress, 0, len(l.servers))
	for _, s := range l.servers {
		addrs = append(addrs, toAddress(s))
	}
	domains := make([]dbusDomain, 0, len(l.domains))
	for _, d := range l.domains {
		domains = append(domains, dbusDomain(d))
	}
	return map[string]dbus.Variant{
		"Ifindex":      dbus.MakeVariant(p.ifindex),
		"DNS":          dbus.MakeVariant(addrs),
		"Domains":      dbus.MakeVariant(domains),
		"DefaultRoute": dbus.MakeVariant(l.effectiveDefaultRoute()),
		// A property we don't read, to make sure extra ones are ignored.
		"LLMNR": dbus.MakeVariant("yes"),
	}, nil
}

func (p linkProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	all, dbusErr := p.GetAll(iface)
	if dbusErr != nil {
		return dbus.Variant{}, dbusErr
	}
	v, ok := all[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
	}
	return v, nil
}

func parseAddress(a dnsAddress) (netip.Addr, error) {
	switch {
	case a.Family == syscall.AF_INET && len(a.Address) == 4:
		return netip.AddrFrom4([4]byte(a.Address)), nil
	case a.Family == syscall.AF_INET6 && len(a.Address) == 16:
		return netip.AddrFrom16([16]byte(a.Address)), nil
	default:
		return netip.Addr{}, errors.New("invalid address")
	}
}

func toAddress(addr netip.Addr) dnsAddress {
	if addr.Is4() {
		b := addr.As4()
		return dnsAddress{Family: syscall.AF_INET, Address: b[:]}
	}
	b := addr.As16()
	return dnsAddress{Family: syscall.AF_INET6, Address: b[:]}
}
