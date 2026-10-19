package ztmock

import (
	"fmt"
	"strconv"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// All fake network IDs belong to the made-up controller c0ffee1234, and all
// public IPs are from the ranges reserved for documentation (192.0.2.0/24,
// 198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32), so nothing here points
// at a real network or machine.

// scenarioNetworks returns the networks a scenario starts with.
func scenarioNetworks(scenario string) ([]ztapi.Network, error) {
	switch scenario {
	case Online:
		return []ztapi.Network{homeLab(), office()}, nil
	case ServiceDown, Unauthorized:
		// The client never gets this far, but if the scenario is switched
		// back the networks are there again.
		return []ztapi.Network{homeLab(), office()}, nil
	case SSORequired:
		return []ztapi.Network{homeLab(), ssoWaiting("c0ffee1234000003", "corp-sso"), ssoLoggedIn()}, nil
	case AccessDenied:
		return []ztapi.Network{homeLab(), accessDenied("c0ffee1234000005", "")}, nil
	case ManyNetworks:
		return manyNetworks(), nil
	case Joining:
		joining := baseNetwork("c0ffee1234000010", "")
		joining.Status = ztapi.StatusRequestingConfiguration
		joining.NetconfRevision = 0
		return []ztapi.Network{homeLab(), joining}, nil
	default:
		return nil, errUnknownScenario(scenario)
	}
}

// baseNetwork returns a healthy network with no addresses or routes yet.
// The scenario functions below change what they need.
func baseNetwork(id ztapi.NetworkID, name string) ztapi.Network {
	return ztapi.Network{
		ID:               id,
		Name:             name,
		Status:           ztapi.StatusOK,
		Type:             "PRIVATE",
		MAC:              macFor(id),
		MTU:              2800,
		PortDeviceName:   "zt" + string(id[8:]),
		NetconfRevision:  3,
		BroadcastEnabled: true,
		// ZeroTier's defaults for a new network: managed addresses on,
		// everything else off.
		AllowManaged: true,
		// The real service sends [] rather than null for empty lists.
		AssignedAddresses: []string{},
		Routes:            []ztapi.Route{},
		DNS:               ztapi.DNS{Servers: []string{}},
	}
}

func homeLab() ztapi.Network {
	n := baseNetwork("c0ffee1234000001", "home-lab")
	n.AssignedAddresses = []string{"10.147.17.5/24", "fdc0:ffee:1234:0:199:93a1:b2c3:d4e5/88"}
	n.Routes = []ztapi.Route{
		{Target: "10.147.17.0/24"},
		{Target: "192.168.100.0/24", Via: "10.147.17.1"},
	}
	n.DNS = ztapi.DNS{Domain: "home.arpa", Servers: []string{"10.147.17.1"}}
	n.AllowDNS = true
	return n
}

func office() ztapi.Network {
	n := baseNetwork("c0ffee1234000002", "office")
	n.AssignedAddresses = []string{"172.27.4.20/16"}
	n.Routes = []ztapi.Route{{Target: "172.27.0.0/16"}}
	return n
}

// ssoWaiting is a network that needs an SSO login before it works.
func ssoWaiting(id ztapi.NetworkID, name string) ztapi.Network {
	n := baseNetwork(id, name)
	n.Status = ztapi.StatusAuthenticationRequired
	n.SSOEnabled = true
	n.AuthenticationURL = "https://sso.example.com/authorize?nwid=" + string(id) + "&state=mock"
	return n
}

// ssoLoggedIn is an SSO network with a valid login. Its expiry time is set
// on every reply (see Server.withExpiry).
func ssoLoggedIn() ztapi.Network {
	n := baseNetwork("c0ffee1234000004", "partner-sso")
	n.SSOEnabled = true
	n.AuthenticationURL = "https://sso.example.com/authorize?nwid=c0ffee1234000004&state=mock"
	n.AssignedAddresses = []string{"10.80.0.12/16"}
	n.Routes = []ztapi.Route{{Target: "10.80.0.0/16"}}
	return n
}

// accessDenied is a private network whose controller has not authorized
// this node. No configuration ever arrived, so there is no name.
func accessDenied(id ztapi.NetworkID, name string) ztapi.Network {
	n := baseNetwork(id, name)
	n.Status = ztapi.StatusAccessDenied
	n.NetconfRevision = 0
	return n
}

func manyNetworks() []ztapi.Network {
	lab := baseNetwork("c0ffee1234000003", "lab_01") // underscores: menu labels may need escaping
	lab.AssignedAddresses = []string{"10.10.1.2/24"}
	lab.Routes = []ztapi.Route{{Target: "10.10.1.0/24"}}

	v6 := baseNetwork("c0ffee1234000004", "ipv6_only")
	v6.AssignedAddresses = []string{"fdc0:ffee:1234:0:499:93a1:b2c3:d4e5/88", "fcaa:bbcc:dda1:b2c3:d4e5::1/40"}
	v6.Routes = []ztapi.Route{{Target: "2001:db8:42::/48", Via: "fdc0:ffee:1234:0:499:9300:0:1"}}
	v6.DNS = ztapi.DNS{Domain: "v6.example", Servers: []string{"2001:db8:42::53"}}
	v6.AllowDNS = true

	requesting := baseNetwork("c0ffee1234000005", "")
	requesting.Status = ztapi.StatusRequestingConfiguration
	requesting.NetconfRevision = 0

	notFound := baseNetwork("c0ffee1234000007", "")
	notFound.Status = ztapi.StatusNotFound
	notFound.NetconfRevision = 0

	portError := baseNetwork("c0ffee1234000008", "tap_broken")
	portError.Status = ztapi.StatusPortError
	portError.PortError = -1

	tooOld := baseNetwork("c0ffee1234000009", "future-net")
	tooOld.Status = ztapi.StatusClientTooOld

	// A long name with spaces and non-ASCII characters, lots of routes,
	// a default route and every flag on.
	long := baseNetwork("c0ffee123400000b", "a very long network name that keeps going — café ☕ edition")
	long.Type = "PUBLIC"
	long.Bridge = true
	long.AllowGlobal = true
	long.AllowDefault = true
	long.AllowDNS = true
	long.AssignedAddresses = []string{"10.200.0.9/16", "203.0.113.9/32"}
	long.Routes = []ztapi.Route{{Target: "0.0.0.0/0", Via: "10.200.0.1"}, {Target: "10.200.0.0/16"}}
	for i := 1; i <= 8; i++ {
		long.Routes = append(long.Routes, ztapi.Route{
			Target: fmt.Sprintf("192.168.%d.0/24", i),
			Via:    "10.200.0.1",
			Metric: i,
		})
	}
	long.DNS = ztapi.DNS{Domain: "corp.example", Servers: []string{"10.200.0.53", "10.200.0.54"}}

	// A status this version doesn't know: the client decodes it as UNKNOWN.
	future := baseNetwork("c0ffee123400000c", "some_new_state")
	future.Status = ztapi.Status("SOME_FUTURE_STATUS")

	return []ztapi.Network{
		homeLab(),
		office(),
		lab,
		v6,
		requesting,
		accessDenied("c0ffee1234000006", "private_net"),
		notFound,
		portError,
		tooOld,
		ssoWaiting("c0ffee123400000a", "sso_corp"),
		long,
		future,
	}
}

// newJoinedNetwork is what a network looks like right after POST
// /network/{id}: no configuration from its controller yet.
func newJoinedNetwork(id ztapi.NetworkID, now time.Time) *mockNetwork {
	n := baseNetwork(id, "")
	n.Status = ztapi.StatusRequestingConfiguration
	n.NetconfRevision = 0
	return &mockNetwork{Network: n, joinedAt: now, configured: false}
}

// configure gives a joined network the configuration its controller would
// send. What that is depends on the scenario.
func configure(n *ztapi.Network, scenario string) {
	switch scenario {
	case AccessDenied:
		n.Status = ztapi.StatusAccessDenied
	case SSORequired:
		*n = ssoWaiting(n.ID, "")
	default:
		subnet := subnetFor(n.ID)
		n.Status = ztapi.StatusOK
		n.Name = "mock-" + string(n.ID[10:])
		n.NetconfRevision = 1
		n.AssignedAddresses = []string{subnet + ".5/24"}
		n.Routes = []ztapi.Route{{Target: subnet + ".0/24"}}
	}
}

// macFor makes a stable, locally administered MAC address from a network
// ID ("02" as the first byte marks it as locally administered).
func macFor(id ztapi.NetworkID) string {
	s := string(id)
	return "02:" + s[6:8] + ":" + s[8:10] + ":" + s[10:12] + ":" + s[12:14] + ":" + s[14:16]
}

// subnetFor makes a stable "10.x.y" prefix from the last 4 hex characters
// of a network ID.
func subnetFor(id ztapi.NetworkID) string {
	s := string(id)
	// ParseUint with base 16 reads hex; bitSize 8 limits it to 0-255. The
	// ID was validated, so these can't fail and the error is ignored.
	x, _ := strconv.ParseUint(s[12:14], 16, 8)
	y, _ := strconv.ParseUint(s[14:16], 16, 8)
	return fmt.Sprintf("10.%d.%d", x, y)
}

// peers returns the fake peer list: two root servers and one other node.
func peers(now time.Time) []ztapi.Peer {
	ms := now.UnixMilli()
	return []ztapi.Peer{
		{
			Address: "1111111111", Role: "PLANET", Latency: 42,
			Version: "-1.-1.-1", VersionMajor: -1, VersionMinor: -1, VersionRev: -1,
			Paths: []ztapi.PeerPath{
				{Address: "192.0.2.1/9993", Active: true, Preferred: true, LastSend: ms - 1000, LastReceive: ms - 900},
				{Address: "2001:db8::1/9993", Active: true, LastSend: ms - 1000, LastReceive: ms - 950},
			},
		},
		{
			Address: "2222222222", Role: "PLANET", Latency: 118,
			Version: "-1.-1.-1", VersionMajor: -1, VersionMinor: -1, VersionRev: -1,
			Paths: []ztapi.PeerPath{
				{Address: "198.51.100.2/9993", Active: true, Preferred: true, LastSend: ms - 2000, LastReceive: ms - 1900},
			},
		},
		{
			Address: "c0ffee1234", Role: "LEAF", Latency: 7,
			Version: "1.16.2", VersionMajor: 1, VersionMinor: 16, VersionRev: 2,
			Paths: []ztapi.PeerPath{
				{Address: "203.0.113.50/41641", Active: true, Preferred: true, LastSend: ms - 300, LastReceive: ms - 250},
			},
		},
	}
}
