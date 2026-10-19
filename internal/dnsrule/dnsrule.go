// Package dnsrule decides which ZeroTier networks get their DNS settings
// applied, and what those settings are. penguintier-dnsd applies them; the
// tray's DNS row and `penguintier doctor` use the same rule to explain
// what the daemon does (or why it does nothing) for each network.
//
// It only computes: no I/O, so plain table tests cover it.
package dnsrule

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// Want is the DNS setup one network asks for: its DNS servers, used only
// for names under its domain.
type Want struct {
	Servers []netip.Addr
	// Domain is lower case, without "~" or a trailing dot, e.g. "pt.test".
	Domain string
}

// Reason says whether a network qualifies, and if not, why. It is a
// string so logs, tests and `penguintier doctor --json` read well.
type Reason string

// The reasons, in the order Check tests them.
const (
	// Qualifies: the daemon applies the network's DNS settings.
	Qualifies Reason = "qualifies"
	// NotRequested: "Allow DNS Configuration" (allowDNS) is off.
	NotRequested Reason = "not-requested"
	// NotConnected: the network's status is not OK, or it has no
	// interface yet.
	NotConnected Reason = "not-connected"
	// NoDNS: the network's controller sent no usable domain, or no usable
	// server.
	NoDNS Reason = "no-dns"
)

// Check tells whether network n qualifies, and returns its DNS setup when
// it does. A network qualifies only when all of these hold:
//   - "Allow DNS Configuration" (allowDNS) is on,
//   - its status is OK and it has an interface,
//   - the controller sent a valid domain and at least one valid server.
func Check(n ztapi.Network) (Want, Reason) {
	if !n.AllowDNS {
		return Want{}, NotRequested
	}
	if n.Status != ztapi.StatusOK || n.PortDeviceName == "" {
		return Want{}, NotConnected
	}
	domain, ok := NormalizeDomain(n.DNS.Domain)
	if !ok {
		return Want{}, NoDNS
	}
	servers := ParseServers(n.DNS.Servers)
	if len(servers) == 0 {
		return Want{}, NoDNS
	}
	return Want{Servers: servers, Domain: domain}, Qualifies
}

// NormalizeDomain lower-cases a domain, drops a trailing dot and checks it
// is a plain DNS name. "." (the root) is refused: "~." would send every
// query on the machine to the ZeroTier network's servers.
func NormalizeDomain(s string) (string, bool) {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if d == "" || len(d) > 253 {
		return "", false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for _, r := range label {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				return "", false
			}
		}
	}
	return d, true
}

// ParseServers turns the controller's server list into addresses, in
// order. It drops what resolved would refuse or merge, so what the daemon
// sets is exactly what resolved reports back: invalid text, 0.0.0.0 and
// ::, resolved's own stub addresses 127.0.0.53 and 127.0.0.54, and
// duplicates. Otherwise the daemon would see a difference on every check
// and write forever.
func ParseServers(list []string) []netip.Addr {
	var servers []netip.Addr
	for _, s := range list {
		addr, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			continue // not an IP address: skip it
		}
		addr = addr.Unmap().WithZone("")
		if addr.IsUnspecified() || addr == stubAddr || addr == proxyStubAddr {
			continue
		}
		if slices.Contains(servers, addr) {
			continue
		}
		servers = append(servers, addr)
	}
	return servers
}

// resolved's local stub resolvers. It refuses them as link DNS servers.
var (
	stubAddr      = netip.MustParseAddr("127.0.0.53")
	proxyStubAddr = netip.MustParseAddr("127.0.0.54")
)
