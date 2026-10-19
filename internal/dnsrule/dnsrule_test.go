package dnsrule

import (
	"net/netip"
	"reflect"
	"testing"

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

func TestCheck(t *testing.T) {
	// good is a network that qualifies; each case changes one thing.
	good := ztapi.Network{
		ID:             "8056c2e21c000001",
		Status:         ztapi.StatusOK,
		PortDeviceName: "zt3jn5bvfq",
		AllowDNS:       true,
		DNS:            ztapi.DNS{Domain: "pt.test", Servers: []string{"10.147.17.1"}},
	}
	goodWant := Want{Servers: addrs("10.147.17.1"), Domain: "pt.test"}

	tests := []struct {
		name       string
		change     func(n *ztapi.Network) // nil: use good as it is
		wantReason Reason
		want       Want // only checked when the network qualifies
	}{
		{name: "qualifies", wantReason: Qualifies, want: goodWant},
		{name: "allowDNS off", change: func(n *ztapi.Network) { n.AllowDNS = false }, wantReason: NotRequested},
		{
			name:       "allowDNS off wins over everything else",
			change:     func(n *ztapi.Network) { n.AllowDNS = false; n.Status = ztapi.StatusAccessDenied; n.DNS = ztapi.DNS{} },
			wantReason: NotRequested,
		},
		{name: "still joining", change: func(n *ztapi.Network) { n.Status = ztapi.StatusRequestingConfiguration }, wantReason: NotConnected},
		{name: "access denied", change: func(n *ztapi.Network) { n.Status = ztapi.StatusAccessDenied }, wantReason: NotConnected},
		{name: "no interface", change: func(n *ztapi.Network) { n.PortDeviceName = "" }, wantReason: NotConnected},
		{name: "no domain", change: func(n *ztapi.Network) { n.DNS.Domain = "" }, wantReason: NoDNS},
		{name: "no servers", change: func(n *ztapi.Network) { n.DNS.Servers = []string{} }, wantReason: NoDNS},
		{
			name:       "only invalid servers",
			change:     func(n *ztapi.Network) { n.DNS.Servers = []string{"not-an-ip", "0.0.0.0", "127.0.0.53"} },
			wantReason: NoDNS,
		},
		{name: "root domain refused", change: func(n *ztapi.Network) { n.DNS.Domain = "." }, wantReason: NoDNS},
		{name: "domain with a space refused", change: func(n *ztapi.Network) { n.DNS.Domain = "a.test b.test" }, wantReason: NoDNS},
		{name: "domain with an empty label refused", change: func(n *ztapi.Network) { n.DNS.Domain = "a..test" }, wantReason: NoDNS},
		{
			name:       "domain is normalized",
			change:     func(n *ztapi.Network) { n.DNS.Domain = " PT.Test. " },
			wantReason: Qualifies,
			want:       goodWant,
		},
		{
			name: "servers are cleaned up, order kept",
			change: func(n *ztapi.Network) {
				n.DNS.Servers = []string{"10.147.17.1", "junk", "::ffff:10.147.17.2", "10.147.17.1", "::", "127.0.0.54", "fe80::1%zt0", "2001:db8::53"}
			},
			wantReason: Qualifies,
			want:       Want{Servers: addrs("10.147.17.1", "10.147.17.2", "fe80::1", "2001:db8::53"), Domain: "pt.test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := good // structs are copied on assignment, so this is our own copy
			n.DNS.Servers = append([]string(nil), good.DNS.Servers...)
			if tt.change != nil {
				tt.change(&n)
			}
			got, reason := Check(n)
			if reason != tt.wantReason {
				t.Fatalf("Check() reason = %q, want %q", reason, tt.wantReason)
			}
			if reason == Qualifies && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Check() = %+v\nwant      %+v", got, tt.want)
			}
		})
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{in: "pt.test", want: "pt.test", wantOK: true},
		{in: "Home.ARPA.", want: "home.arpa", wantOK: true},
		{in: "under_score.test", want: "under_score.test", wantOK: true},
		{in: "", wantOK: false},
		{in: ".", wantOK: false},
		{in: "a..b", wantOK: false},
		{in: "bad!.test", wantOK: false},
	}
	for _, tt := range tests {
		got, ok := NormalizeDomain(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("NormalizeDomain(%q) = %q, %t; want %q, %t", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}
