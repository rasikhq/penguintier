package resolved

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// Names on the bus, from org.freedesktop.resolve1(5).
const (
	// BusName is the name systemd-resolved owns on the system bus.
	BusName = "org.freedesktop.resolve1"
	// ManagerPath is the path of resolved's main object.
	ManagerPath = dbus.ObjectPath("/org/freedesktop/resolve1")
	// ManagerInterface has the SetLink* methods.
	ManagerInterface = "org.freedesktop.resolve1.Manager"
	// LinkInterface has the per-link properties (DNS, Domains, ...).
	LinkInterface = "org.freedesktop.resolve1.Link"
)

// Domain is one entry of a link's domain list. resolved has two kinds:
//
//   - a search domain (RoutingOnly false): single-label names such as
//     "nas" are tried as "nas.pt.test", and queries for *.pt.test go to
//     this link's DNS servers;
//   - a routing-only domain (RoutingOnly true, written "~pt.test" by
//     resolvectl): only the routing part. Queries for *.pt.test go to this
//     link's servers, and nothing else changes.
//
// Name has no "~" in front; RoutingOnly carries that.
type Domain struct {
	Name        string
	RoutingOnly bool
}

// String writes the domain the way resolvectl does: "~pt.test" or
// "pt.test".
func (d Domain) String() string {
	if d.RoutingOnly {
		return "~" + d.Name
	}
	return d.Name
}

// LinkDNS is a link's current DNS setup, as resolved reports it.
type LinkDNS struct {
	// Servers are the link's DNS servers, in order.
	Servers []netip.Addr
	// Domains are its search and routing-only domains.
	Domains []Domain
	// DefaultRoute says whether resolved also sends queries for names
	// outside Domains to this link's servers. When nobody has set it,
	// resolved reports what is in effect: false when the link has a
	// routing-only domain, true otherwise (if it has servers).
	DefaultRoute bool
}

// dnsAddress is one DNS server in D-Bus form, signature "(iay)": an
// address family (AF_INET or AF_INET6) and the address as 4 or 16 bytes.
// godbus encodes a Go struct as a D-Bus struct, field by field.
type dnsAddress struct {
	Family  int32
	Address []byte
}

// dbusDomain is one domain in D-Bus form, signature "(sb)".
type dbusDomain struct {
	Name        string
	RoutingOnly bool
}

// Client talks to systemd-resolved over one D-Bus connection.
//
// godbus connections are safe to use from several goroutines, but
// penguintier-dnsd uses a Client from one goroutine only.
type Client struct {
	conn *dbus.Conn
}

// Connect connects to the system bus. Like other D-Bus programs it uses
// $DBUS_SYSTEM_BUS_ADDRESS when that is set, which is handy for testing
// against a private bus.
func Connect() (*Client, error) {
	// ConnectSystemBus opens a private connection (not godbus's shared
	// one), so Close really closes it.
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the system D-Bus: %w", err)
	}
	return &Client{conn: conn}, nil
}

// ConnectAddress connects to the bus at a D-Bus address such as
// "unix:path=/tmp/test-bus". Tests use it with a private dbus-daemon.
func ConnectAddress(address string) (*Client, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("connect to D-Bus at %s: %w", address, err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the connection. Channels from WatchOwner are closed too.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Done returns a channel that is closed when the bus connection is lost
// (for example when dbus-daemon restarts). A Client can't reconnect;
// penguintier-dnsd exits and systemd starts it again.
//
// A "<-chan struct{}" is a receive-only channel that never carries values;
// closing it is the signal. That is the same pattern ctx.Done() uses.
func (c *Client) Done() <-chan struct{} {
	return c.conn.Context().Done()
}

// Available reports whether systemd-resolved is on the bus right now. It
// asks the bus itself (NameHasOwner), so it never starts resolved and
// needs no permission.
func (c *Client) Available(ctx context.Context) (bool, error) {
	var hasOwner bool
	err := c.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, BusName).Store(&hasOwner)
	if err != nil {
		return false, fmt.Errorf("ask D-Bus whether %s is running: %w", BusName, err)
	}
	return hasOwner, nil
}

// manager returns resolved's Manager object, the target of every call
// below.
func (c *Client) manager() dbus.BusObject {
	return c.conn.Object(BusName, ManagerPath)
}

// callManager calls one Manager method and maps its error.
//
// dbus.FlagNoAutoStart tells the bus not to start resolved for this call
// ("D-Bus activation"). If resolved is installed but stopped, an admin
// stopped it, and we should not bring it back. We get ErrNotRunning
// instead.
func (c *Client) callManager(ctx context.Context, method string, ifindex int, args ...any) error {
	index, err := toInt32(ifindex)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	// Put the ifindex in front of the other arguments.
	all := append([]any{index}, args...)
	call := c.manager().CallWithContext(ctx, ManagerInterface+"."+method, dbus.FlagNoAutoStart, all...)
	return mapError(fmt.Sprintf("%s link %d", method, ifindex), call.Err)
}

// SetLinkDNS replaces the link's DNS servers. An empty list removes them.
//
// Polkit action: org.freedesktop.resolve1.set-dns-servers.
func (c *Client) SetLinkDNS(ctx context.Context, ifindex int, servers []netip.Addr) error {
	// make([]T, 0, n) makes an empty slice with room for n items. Empty
	// rather than nil, so godbus still knows the element type.
	addrs := make([]dnsAddress, 0, len(servers))
	for _, s := range servers {
		a, err := toDBusAddress(s)
		if err != nil {
			return fmt.Errorf("SetLinkDNS link %d: %w", ifindex, err)
		}
		addrs = append(addrs, a)
	}
	return c.callManager(ctx, "SetLinkDNS", ifindex, addrs)
}

// SetLinkDomains replaces the link's search and routing-only domains. An
// empty list removes them.
//
// Polkit action: org.freedesktop.resolve1.set-domains.
func (c *Client) SetLinkDomains(ctx context.Context, ifindex int, domains []Domain) error {
	list := make([]dbusDomain, 0, len(domains))
	for _, d := range domains {
		list = append(list, dbusDomain(d)) // same fields, so a plain conversion works
	}
	return c.callManager(ctx, "SetLinkDomains", ifindex, list)
}

// SetLinkDefaultRoute sets whether the link's DNS servers also answer
// names outside its domains. penguintier-dnsd always sets false: a
// ZeroTier network should only get the queries for its own domain.
//
// Polkit action: org.freedesktop.resolve1.set-default-route.
func (c *Client) SetLinkDefaultRoute(ctx context.Context, ifindex int, enable bool) error {
	return c.callManager(ctx, "SetLinkDefaultRoute", ifindex, enable)
}

// RevertLink drops everything set over D-Bus for the link (servers,
// domains, DefaultRoute and the rest), back to resolved's defaults.
//
// Polkit action: org.freedesktop.resolve1.revert.
func (c *Client) RevertLink(ctx context.Context, ifindex int) error {
	return c.callManager(ctx, "RevertLink", ifindex)
}

// Link reads the link's current DNS servers, domains and DefaultRoute.
// Reading needs no permission. If resolved does not know the link, the
// error wraps ErrNoSuchLink.
func (c *Client) Link(ctx context.Context, ifindex int) (LinkDNS, error) {
	op := fmt.Sprintf("read link %d", ifindex)
	index, err := toInt32(ifindex)
	if err != nil {
		return LinkDNS{}, fmt.Errorf("%s: %w", op, err)
	}

	// 1. Ask the Manager for the link's object path.
	var path dbus.ObjectPath
	call := c.manager().CallWithContext(ctx, ManagerInterface+".GetLink", dbus.FlagNoAutoStart, index)
	if err := call.Store(&path); err != nil {
		return LinkDNS{}, mapError(op, err)
	}

	// 2. Read all its properties in one call. The reply is a map from
	// property name to a Variant: a D-Bus value that carries its own type
	// signature, a bit like `unknown` in TypeScript until you check it.
	var props map[string]dbus.Variant
	call = c.conn.Object(BusName, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", dbus.FlagNoAutoStart, LinkInterface)
	if err := call.Store(&props); err != nil {
		return LinkDNS{}, mapError(op, err)
	}
	return parseLinkProps(op, props)
}

// parseLinkProps decodes the three properties we care about. A property
// that is missing keeps its zero value: very old systemd versions have no
// DefaultRoute, for example.
func parseLinkProps(op string, props map[string]dbus.Variant) (LinkDNS, error) {
	var link LinkDNS

	// The "v, ok := m[key]" form tells a missing key (ok == false) apart
	// from a key holding a zero value.
	if v, ok := props["DNS"]; ok {
		var addrs []dnsAddress
		// Store copies the variant's value into addrs, converting D-Bus
		// structs into our Go struct. It fails if the types don't match.
		if err := v.Store(&addrs); err != nil {
			return LinkDNS{}, fmt.Errorf("%s: DNS property (%s): %w", op, v.Signature(), err)
		}
		for _, a := range addrs {
			if addr, ok := fromDBusAddress(a); ok {
				link.Servers = append(link.Servers, addr)
			}
		}
	}
	if v, ok := props["Domains"]; ok {
		var domains []dbusDomain
		if err := v.Store(&domains); err != nil {
			return LinkDNS{}, fmt.Errorf("%s: Domains property (%s): %w", op, v.Signature(), err)
		}
		for _, d := range domains {
			link.Domains = append(link.Domains, Domain(d))
		}
	}
	if v, ok := props["DefaultRoute"]; ok {
		if err := v.Store(&link.DefaultRoute); err != nil {
			return LinkDNS{}, fmt.Errorf("%s: DefaultRoute property (%s): %w", op, v.Signature(), err)
		}
	}
	return link, nil
}

// toInt32 converts an ifindex to the int32 ("i") resolved wants. A plain
// Go int would be sent as int64 ("x") and rejected. Real ifindexes are
// small positive numbers; anything else is refused.
func toInt32(ifindex int) (int32, error) {
	if ifindex <= 0 || ifindex > math.MaxInt32 {
		return 0, fmt.Errorf("invalid interface index %d", ifindex)
	}
	return int32(ifindex), nil
}

// toDBusAddress converts an IP address into resolved's (family, bytes)
// form.
func toDBusAddress(a netip.Addr) (dnsAddress, error) {
	if !a.IsValid() {
		return dnsAddress{}, errors.New("invalid DNS server address")
	}
	// Unmap turns "::ffff:10.0.0.1" into plain "10.0.0.1". WithZone("")
	// drops an IPv6 zone such as "%zt0": the link already says where the
	// server is, and the D-Bus form has no room for a zone.
	a = a.Unmap().WithZone("")
	if a.Is4() {
		b := a.As4() // a [4]byte array; b[:] makes a slice of it
		return dnsAddress{Family: syscall.AF_INET, Address: b[:]}, nil
	}
	b := a.As16()
	return dnsAddress{Family: syscall.AF_INET6, Address: b[:]}, nil
}

// fromDBusAddress converts resolved's form back into an IP address. ok is
// false for an unknown family or a wrong length.
func fromDBusAddress(a dnsAddress) (addr netip.Addr, ok bool) {
	switch {
	case a.Family == syscall.AF_INET && len(a.Address) == 4:
		return netip.AddrFrom4([4]byte(a.Address)), true
	case a.Family == syscall.AF_INET6 && len(a.Address) == 16:
		return netip.AddrFrom16([16]byte(a.Address)), true
	default:
		return netip.Addr{}, false
	}
}
