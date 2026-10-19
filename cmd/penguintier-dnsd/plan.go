package main

import (
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// This file is the daemon's brain, and it does no I/O at all: Desired and
// Plan only compute. That makes every decision easy to test with plain
// tables (plan_test.go). daemon.go does the reading and writing.

// Config is the DNS setup for one ZeroTier interface: the network's DNS
// servers, used only for names under its domain.
//
// It is also what the state file remembers for each link we applied (see
// state.go), which is why the fields have JSON tags. netip.Addr turns
// itself into a JSON string such as "10.147.17.1".
type Config struct {
	Network ztapi.NetworkID `json:"network"`
	Servers []netip.Addr    `json:"servers"`
	// Domain is lower case, without "~" or a trailing dot, e.g. "pt.test".
	Domain string `json:"domain"`
}

// Equal reports whether two configs are the same.
func (c Config) Equal(other Config) bool {
	return c.Network == other.Network && c.Domain == other.Domain && slices.Equal(c.Servers, other.Servers)
}

// domains is the domain list we give resolved: just the routing-only
// "~domain".
func (c Config) domains() []resolved.Domain {
	return []resolved.Domain{{Name: c.Domain, RoutingOnly: true}}
}

// Desired picks the networks whose DNS settings should be applied, and
// returns a Config for each, keyed by interface name ("ztabcdefgh").
//
// A network qualifies only when all of these hold:
//   - "Allow DNS Configuration" (allowDNS) is on,
//   - its status is OK,
//   - it has an interface,
//   - the controller sent a valid domain and at least one valid server.
func Desired(networks []ztapi.Network) map[string]Config {
	desired := map[string]Config{}
	for _, n := range networks {
		if !n.AllowDNS || n.Status != ztapi.StatusOK || n.PortDeviceName == "" {
			continue
		}
		domain, ok := normalizeDomain(n.DNS.Domain)
		if !ok {
			continue
		}
		servers := parseServers(n.DNS.Servers)
		if len(servers) == 0 {
			continue
		}
		desired[n.PortDeviceName] = Config{Network: n.ID, Servers: servers, Domain: domain}
	}
	return desired
}

// normalizeDomain lower-cases a domain, drops a trailing dot and checks it
// is a plain DNS name. "." (the root) is refused: "~." would send every
// query on the machine to the ZeroTier network's servers.
func normalizeDomain(s string) (string, bool) {
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

// parseServers turns the controller's server list into addresses, in
// order. It drops what resolved would refuse or merge, so what we set is
// exactly what resolved reports back: invalid text, 0.0.0.0 and ::,
// resolved's own stub addresses 127.0.0.53 and 127.0.0.54, and duplicates.
// Otherwise the daemon would see a difference on every check and write
// forever.
func parseServers(list []string) []netip.Addr {
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

// OpKind says what an Op does. It is a string so tests and logs read well.
type OpKind string

// The things Plan can ask for.
const (
	// OpRemember records Config as applied by us, in the state file.
	OpRemember OpKind = "remember"
	// OpSetDefaultRoute calls SetLinkDefaultRoute(false).
	OpSetDefaultRoute OpKind = "set-default-route"
	// OpSetDomains calls SetLinkDomains with the routing-only domain.
	OpSetDomains OpKind = "set-domains"
	// OpSetDNS calls SetLinkDNS with the servers.
	OpSetDNS OpKind = "set-dns"
	// OpRevert calls RevertLink.
	OpRevert OpKind = "revert"
	// OpForget removes the link from the state file. resolved is not
	// touched.
	OpForget OpKind = "forget"
	// OpLeaveAlone changes nothing: the link has DNS settings from someone
	// else. The daemon only logs it.
	OpLeaveAlone OpKind = "leave-alone"
)

// Op is one step for the daemon to carry out.
type Op struct {
	Kind OpKind
	// Link is the interface name. The daemon looks up its ifindex right
	// before each D-Bus call.
	Link string
	// Config is what to set or remember. Revert, forget and leave-alone
	// leave it empty.
	Config Config
}

// Plan compares what should be (desired) with what resolved reports
// (current) and what we set before (applied), and returns the steps to
// get there. All three maps are keyed by interface name.
//
// A link that is missing from current has no interface right now (or
// resolved has not seen it yet).
//
// The rules:
//
//   - We only change links we set ourselves (they are in applied), or
//     links with no DNS settings at all. A link with someone else's
//     settings is left alone. So if a future ZeroTier sets DNS itself, we
//     step aside.
//   - We write only what differs, because every write makes resolved
//     flush its cache.
//   - If our settings vanished (resolved restarted, NetworkManager reset
//     the link), we set them again.
//   - When a network stops qualifying, or is left, we revert the link and
//     forget it.
//
// The steps for one link are in order, and the daemon stops at the first
// one that fails (the rest of that link waits for the next check).
func Plan(desired map[string]Config, current map[string]resolved.LinkDNS, applied map[string]Config) []Op {
	var ops []Op
	for _, link := range linkNames(desired, applied) {
		// Reading a missing map key gives the zero value; the second
		// result ("ok") says whether the key was there.
		want, wanted := desired[link]
		had, ours := applied[link]
		cur, exists := current[link]

		switch {
		case !exists:
			// No interface (ZeroTier is restarting, or the network was
			// left). resolved forgets a link when its interface goes, so
			// there is nothing to revert.
			if ours && !wanted {
				ops = append(ops, Op{Kind: OpForget, Link: link})
			}
			// If it is still wanted, wait for the interface. A link that
			// was ours stays remembered: it comes back empty and is set
			// again.

		case !wanted:
			// It was ours and should not be any more: revert, unless
			// someone else has changed it since.
			if ownedBy(cur, had) {
				ops = append(ops, Op{Kind: OpRevert, Link: link})
			}
			ops = append(ops, Op{Kind: OpForget, Link: link})

		case !ours:
			// Not ours yet. Take it only if nobody has set anything.
			if !isEmpty(cur) {
				ops = append(ops, Op{Kind: OpLeaveAlone, Link: link})
				continue
			}
			// Remember first, then write. If we crash halfway, the next
			// run still knows the link is ours.
			ops = append(ops, Op{Kind: OpRemember, Link: link, Config: want})
			ops = append(ops, setOps(link, cur, want)...)

		default:
			// Ours and still wanted. If someone else has changed it,
			// step aside.
			if !ownedBy(cur, had, want) {
				ops = append(ops, Op{Kind: OpForget, Link: link}, Op{Kind: OpLeaveAlone, Link: link})
				continue
			}
			sets := setOps(link, cur, want)
			ops = append(ops, sets...)
			// A changed config is remembered only after every write
			// worked: if one fails, the old config still counts as ours
			// (see ownedBy), so the next check can finish the job.
			if !had.Equal(want) {
				ops = append(ops, Op{Kind: OpRemember, Link: link, Config: want})
			}
		}
	}
	return ops
}

// setOps returns the writes that turn cur into want, in a safe order:
// DefaultRoute off first, then the domain, then the servers. That way the
// link never has servers without its routing-only domain, which for a
// moment would send every DNS query on the machine to it.
func setOps(link string, cur resolved.LinkDNS, want Config) []Op {
	var ops []Op
	// An empty link gets all three: we always set DefaultRoute=false
	// explicitly on a full apply, even though resolved already reports
	// false for a link with no servers.
	if cur.DefaultRoute || isEmpty(cur) {
		ops = append(ops, Op{Kind: OpSetDefaultRoute, Link: link, Config: want})
	}
	if !domainsEqual(cur.Domains, want.domains()) {
		ops = append(ops, Op{Kind: OpSetDomains, Link: link, Config: want})
	}
	if !slices.Equal(cur.Servers, want.Servers) {
		ops = append(ops, Op{Kind: OpSetDNS, Link: link, Config: want})
	}
	return ops
}

// isEmpty reports whether a link has no DNS servers and no domains.
func isEmpty(cur resolved.LinkDNS) bool {
	return len(cur.Servers) == 0 && len(cur.Domains) == 0
}

// ownedBy reports whether everything on the link could have come from us:
// the servers are empty or match one of configs, and so are the domains.
// The parts are checked on their own because an update can stop halfway
// (new domain set, old servers still there).
//
// "configs ...Config" makes this a variadic function: it takes any number
// of Config arguments and sees them as a slice.
func ownedBy(cur resolved.LinkDNS, configs ...Config) bool {
	serversOK := len(cur.Servers) == 0
	domainsOK := len(cur.Domains) == 0
	for _, c := range configs {
		if slices.Equal(cur.Servers, c.Servers) {
			serversOK = true
		}
		if domainsEqual(cur.Domains, c.domains()) {
			domainsOK = true
		}
	}
	return serversOK && domainsOK
}

// domainsEqual compares domain lists in order, ignoring case and a
// trailing dot (resolved may report names a little differently from how
// they were set).
func domainsEqual(a, b []resolved.Domain) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].RoutingOnly != b[i].RoutingOnly {
			return false
		}
		nameA := strings.ToLower(strings.TrimSuffix(a[i].Name, "."))
		nameB := strings.ToLower(strings.TrimSuffix(b[i].Name, "."))
		if nameA != nameB {
			return false
		}
	}
	return true
}

// linkNames returns the keys of both maps, sorted, so the ops always come
// out in the same order.
func linkNames(a, b map[string]Config) []string {
	var names []string
	for name := range a {
		names = append(names, name)
	}
	for name := range b {
		if _, dup := a[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
