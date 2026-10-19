package main

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// addrs parses IP addresses for test tables; MustParseAddr panics on a
// typo, which is fine in a test.
func addrs(list ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range list {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func routing(name string) []resolved.Domain {
	return []resolved.Domain{{Name: name, RoutingOnly: true}}
}

func TestDesired(t *testing.T) {
	// good is a network that qualifies; each case changes one thing.
	good := ztapi.Network{
		ID:             "8056c2e21c000001",
		Status:         ztapi.StatusOK,
		PortDeviceName: "zt3jn5bvfq",
		AllowDNS:       true,
		DNS:            ztapi.DNS{Domain: "pt.test", Servers: []string{"10.147.17.1"}},
	}
	goodConfig := Config{Network: "8056c2e21c000001", Servers: addrs("10.147.17.1"), Domain: "pt.test"}

	tests := []struct {
		name   string
		change func(n *ztapi.Network) // nil: use good as it is
		want   map[string]Config
	}{
		{name: "qualifies", want: map[string]Config{"zt3jn5bvfq": goodConfig}},
		{name: "allowDNS off", change: func(n *ztapi.Network) { n.AllowDNS = false }},
		{name: "still joining", change: func(n *ztapi.Network) { n.Status = ztapi.StatusRequestingConfiguration }},
		{name: "access denied", change: func(n *ztapi.Network) { n.Status = ztapi.StatusAccessDenied }},
		{name: "no interface", change: func(n *ztapi.Network) { n.PortDeviceName = "" }},
		{name: "no domain", change: func(n *ztapi.Network) { n.DNS.Domain = "" }},
		{name: "no servers", change: func(n *ztapi.Network) { n.DNS.Servers = []string{} }},
		{name: "only invalid servers", change: func(n *ztapi.Network) { n.DNS.Servers = []string{"not-an-ip", "0.0.0.0", "127.0.0.53"} }},
		{name: "root domain refused", change: func(n *ztapi.Network) { n.DNS.Domain = "." }},
		{name: "domain with a space refused", change: func(n *ztapi.Network) { n.DNS.Domain = "a.test b.test" }},
		{name: "domain with an empty label refused", change: func(n *ztapi.Network) { n.DNS.Domain = "a..test" }},
		{
			name:   "domain is normalized",
			change: func(n *ztapi.Network) { n.DNS.Domain = " PT.Test. " },
			want:   map[string]Config{"zt3jn5bvfq": goodConfig},
		},
		{
			name: "servers are cleaned up, order kept",
			change: func(n *ztapi.Network) {
				n.DNS.Servers = []string{"10.147.17.1", "junk", "::ffff:10.147.17.2", "10.147.17.1", "::", "127.0.0.54", "fe80::1%zt0", "2001:db8::53"}
			},
			want: map[string]Config{"zt3jn5bvfq": {
				Network: "8056c2e21c000001",
				Servers: addrs("10.147.17.1", "10.147.17.2", "fe80::1", "2001:db8::53"),
				Domain:  "pt.test",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := good // structs are copied on assignment, so this is our own copy
			n.DNS.Servers = append([]string(nil), good.DNS.Servers...)
			if tt.change != nil {
				tt.change(&n)
			}
			got := Desired([]ztapi.Network{n})
			want := tt.want
			if want == nil {
				want = map[string]Config{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Desired() = %+v\nwant        %+v", got, want)
			}
		})
	}
}

func TestPlan(t *testing.T) {
	cfg := Config{Network: "8056c2e21c000001", Servers: addrs("10.147.17.1"), Domain: "pt.test"}
	// The same network after the controller changed its server or domain.
	newServers := Config{Network: cfg.Network, Servers: addrs("10.147.17.1", "10.147.17.2"), Domain: "pt.test"}
	newDomain := Config{Network: cfg.Network, Servers: cfg.Servers, Domain: "lab.test"}
	other := Config{Network: "8056c2e21c000002", Servers: addrs("2001:db8::53"), Domain: "v6.test"}

	// Link states as resolved reports them.
	empty := resolved.LinkDNS{}
	ours := resolved.LinkDNS{Servers: cfg.Servers, Domains: routing("pt.test")}
	foreign := resolved.LinkDNS{Servers: addrs("192.0.2.53"), Domains: []resolved.Domain{{Name: "corp.example"}}, DefaultRoute: true}

	// Shorthands for the expected ops.
	remember := func(link string, c Config) Op { return Op{Kind: OpRemember, Link: link, Config: c} }
	setRoute := func(link string, c Config) Op { return Op{Kind: OpSetDefaultRoute, Link: link, Config: c} }
	setDomains := func(link string, c Config) Op { return Op{Kind: OpSetDomains, Link: link, Config: c} }
	setDNS := func(link string, c Config) Op { return Op{Kind: OpSetDNS, Link: link, Config: c} }
	revert := func(link string) Op { return Op{Kind: OpRevert, Link: link} }
	forget := func(link string) Op { return Op{Kind: OpForget, Link: link} }
	leaveAlone := func(link string) Op { return Op{Kind: OpLeaveAlone, Link: link} }

	tests := []struct {
		name    string
		desired map[string]Config
		current map[string]resolved.LinkDNS
		applied map[string]Config
		want    []Op
	}{
		{
			name:    "new link with nothing set: remember first, then all three writes",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": empty},
			want:    []Op{remember("zt1", cfg), setRoute("zt1", cfg), setDomains("zt1", cfg), setDNS("zt1", cfg)},
		},
		{
			name:    "already applied: no writes",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": ours},
			applied: map[string]Config{"zt1": cfg},
		},
		{
			name:    "someone else's DNS on a link we never touched: leave it alone",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": foreign},
			want:    []Op{leaveAlone("zt1")},
		},
		{
			name:    "exactly our settings but not set by us (say, ZeroTier itself): leave it alone",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": ours},
			want:    []Op{leaveAlone("zt1")},
		},
		{
			name:    "interface not there yet: wait",
			desired: map[string]Config{"zt1": cfg},
		},
		{
			name:    "our interface gone while ZeroTier restarts: wait, keep remembering",
			desired: map[string]Config{"zt1": cfg},
			applied: map[string]Config{"zt1": cfg},
		},
		{
			name:    "drift: resolved restarted or NetworkManager reset the link",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": empty},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{setRoute("zt1", cfg), setDomains("zt1", cfg), setDNS("zt1", cfg)},
		},
		{
			name:    "drift: only DefaultRoute was switched on",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": {Servers: cfg.Servers, Domains: routing("pt.test"), DefaultRoute: true}},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{setRoute("zt1", cfg)},
		},
		{
			name:    "controller added a server: only SetLinkDNS",
			desired: map[string]Config{"zt1": newServers},
			current: map[string]resolved.LinkDNS{"zt1": ours},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{setDNS("zt1", newServers), remember("zt1", newServers)},
		},
		{
			name:    "controller changed the domain: only SetLinkDomains",
			desired: map[string]Config{"zt1": newDomain},
			current: map[string]resolved.LinkDNS{"zt1": ours},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{setDomains("zt1", newDomain), remember("zt1", newDomain)},
		},
		{
			name:    "an update that stopped halfway is finished, not mistaken for someone else's",
			desired: map[string]Config{"zt1": newDomain},
			current: map[string]resolved.LinkDNS{"zt1": {Servers: cfg.Servers, Domains: routing("lab.test")}},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{remember("zt1", newDomain)},
		},
		{
			name:    "a first apply that stopped halfway is finished",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": {Domains: routing("pt.test")}},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{setDNS("zt1", cfg)},
		},
		{
			name:    "domain case and trailing dot don't count as a difference",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": {Servers: cfg.Servers, Domains: routing("PT.test.")}},
			applied: map[string]Config{"zt1": cfg},
		},
		{
			name:    "someone else took over our link: forget it and step aside",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": foreign},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{forget("zt1"), leaveAlone("zt1")},
		},
		{
			name:    "someone else changed only the domain to a search domain: step aside",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": {Servers: cfg.Servers, Domains: []resolved.Domain{{Name: "pt.test"}}}},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{forget("zt1"), leaveAlone("zt1")},
		},
		{
			name:    "network stopped qualifying (allowDNS off, left, status not OK): revert",
			current: map[string]resolved.LinkDNS{"zt1": ours},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{revert("zt1"), forget("zt1")},
		},
		{
			name:    "not wanted and already empty: still revert, to clear DefaultRoute",
			current: map[string]resolved.LinkDNS{"zt1": empty},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{revert("zt1"), forget("zt1")},
		},
		{
			name:    "not wanted and the interface is gone: just forget",
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{forget("zt1")},
		},
		{
			name:    "not wanted and someone else changed it: forget without reverting",
			current: map[string]resolved.LinkDNS{"zt1": foreign},
			applied: map[string]Config{"zt1": cfg},
			want:    []Op{forget("zt1")},
		},
		{
			name:    "nothing to write, but the remembered config is out of date",
			desired: map[string]Config{"zt1": cfg},
			current: map[string]resolved.LinkDNS{"zt1": ours},
			applied: map[string]Config{"zt1": newServers},
			want:    []Op{remember("zt1", cfg)},
		},
		{
			name:    "links that are neither wanted nor ours are never looked at",
			current: map[string]resolved.LinkDNS{"eth0": foreign, "zt9": empty},
		},
		{
			name:    "shutdown (nothing desired) reverts every link, sorted by name",
			current: map[string]resolved.LinkDNS{"ztb": ours, "zta": {Servers: other.Servers, Domains: routing("v6.test")}},
			applied: map[string]Config{"ztb": cfg, "zta": other},
			want:    []Op{revert("zta"), forget("zta"), revert("ztb"), forget("ztb")},
		},
		{
			name:    "several links at once",
			desired: map[string]Config{"zta": other, "ztb": cfg, "ztc": cfg},
			current: map[string]resolved.LinkDNS{"zta": empty, "ztb": ours, "ztc": foreign, "ztd": ours},
			applied: map[string]Config{"ztb": cfg, "ztd": cfg},
			want: []Op{
				remember("zta", other), setRoute("zta", other), setDomains("zta", other), setDNS("zta", other),
				leaveAlone("ztc"),
				revert("ztd"), forget("ztd"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan(tt.desired, tt.current, tt.applied)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Plan() =\n  %+v\nwant\n  %+v", got, tt.want)
			}

			// Carry out the ops on a simulated resolved, then plan again:
			// the second plan must not write anything. That is the
			// "reconcile" promise: one round gets everything in shape.
			current, applied := simulate(got, tt.current, tt.applied)
			for _, op := range Plan(tt.desired, current, applied) {
				if op.Kind != OpLeaveAlone {
					t.Errorf("second Plan() still wants %+v", op)
				}
			}
		})
	}
}

// simulate applies ops to copies of current and applied the way the
// daemon and resolved would, and returns the copies.
func simulate(ops []Op, current map[string]resolved.LinkDNS, applied map[string]Config) (map[string]resolved.LinkDNS, map[string]Config) {
	cur := map[string]resolved.LinkDNS{}
	for k, v := range current {
		cur[k] = v
	}
	app := map[string]Config{}
	for k, v := range applied {
		app[k] = v
	}
	for _, op := range ops {
		link := cur[op.Link]
		switch op.Kind {
		case OpSetDefaultRoute:
			link.DefaultRoute = false
		case OpSetDomains:
			link.Domains = op.Config.domains()
		case OpSetDNS:
			link.Servers = op.Config.Servers
		case OpRevert:
			link = resolved.LinkDNS{}
		case OpRemember:
			app[op.Link] = op.Config
			continue
		case OpForget:
			delete(app, op.Link)
			continue
		case OpLeaveAlone:
			continue
		}
		cur[op.Link] = link
	}
	return cur, app
}
