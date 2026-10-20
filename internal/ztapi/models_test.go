package ztapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readFixture returns a file from testdata/. t.Helper marks this as a
// helper, so a failure is reported at the caller's line.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestParseNetworkID(t *testing.T) {
	tests := []struct {
		in      string
		want    NetworkID
		wantErr bool
	}{
		{in: "8056c2e21c000001", want: "8056c2e21c000001"},
		{in: "8056C2E21C000001", want: "8056c2e21c000001"},
		{in: "  8056c2e21c000001\n", want: "8056c2e21c000001"},
		{in: "", wantErr: true},
		{in: "8056c2e21c00000", wantErr: true},   // 15 characters
		{in: "8056c2e21c0000011", wantErr: true}, // 17 characters
		{in: "8056c2e21c00000g", wantErr: true},  // not hex
		{in: "../status/000000", wantErr: true},
		{in: "8056c2e2 1c00001", wantErr: true}, // space inside
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseNetworkID(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseNetworkID(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNetworkID(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseNetworkID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNetworkIDValid(t *testing.T) {
	// Valid is stricter than ParseNetworkID: no trimming, no upper case.
	tests := map[NetworkID]bool{
		"8056c2e21c000001":   true,
		"8056C2E21C000001":   false,
		" 8056c2e21c000001":  false,
		"8056c2e21c00000":    false,
		"":                   false,
		"zzzzzzzzzzzzzzzz":   false,
		"0123456789abcdef":   true,
		"0123456789abcdef00": false,
	}
	// Ranging over a map gives each key and value (in random order).
	for id, want := range tests {
		if got := id.Valid(); got != want {
			t.Errorf("NetworkID(%q).Valid() = %v, want %v", id, got, want)
		}
	}
}

func TestStatusUnmarshal(t *testing.T) {
	tests := []struct {
		json string
		want Status
	}{
		{`"OK"`, StatusOK},
		{`"REQUESTING_CONFIGURATION"`, StatusRequestingConfiguration},
		{`"ACCESS_DENIED"`, StatusAccessDenied},
		{`"NOT_FOUND"`, StatusNotFound},
		{`"PORT_ERROR"`, StatusPortError},
		{`"CLIENT_TOO_OLD"`, StatusClientTooOld},
		{`"AUTHENTICATION_REQUIRED"`, StatusAuthenticationRequired},
		{`"SOMETHING_NEW"`, StatusUnknown},
		{`""`, StatusUnknown},
		{`"ok"`, StatusUnknown}, // the service always sends upper case
	}
	for _, tc := range tests {
		t.Run(tc.json, func(t *testing.T) {
			var got Status
			if err := json.Unmarshal([]byte(tc.json), &got); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.json, err)
			}
			if got != tc.want {
				t.Errorf("Unmarshal(%s) = %q, want %q", tc.json, got, tc.want)
			}
		})
	}

	// A status that is not a string at all is a real error.
	var s Status
	if err := json.Unmarshal([]byte(`42`), &s); err == nil {
		t.Error("Unmarshal(42) into Status: want an error")
	}
}

func TestParseFlag(t *testing.T) {
	tests := []struct {
		in      string
		want    Flag
		wantErr bool
	}{
		{in: "allowManaged", want: FlagAllowManaged},
		{in: "allowGlobal", want: FlagAllowGlobal},
		{in: "allowDefault", want: FlagAllowDefault},
		{in: "allowDNS", want: FlagAllowDNS},
		{in: "allowdns", want: FlagAllowDNS},
		{in: "ALLOWGLOBAL", want: FlagAllowGlobal},
		{in: "dns", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseFlag(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseFlag(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseFlag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFlagValid(t *testing.T) {
	for _, f := range AllFlags {
		if !f.Valid() {
			t.Errorf("%q.Valid() = false", f)
		}
	}
	for _, f := range []Flag{"allowdns", "AllowDNS", "", "allowEverything"} {
		if f.Valid() {
			t.Errorf("%q.Valid() = true, want false", f)
		}
	}
}

func TestNetworkFlag(t *testing.T) {
	n := Network{AllowManaged: true, AllowGlobal: false, AllowDefault: true, AllowDNS: false}
	want := map[Flag]bool{
		FlagAllowManaged: true,
		FlagAllowGlobal:  false,
		FlagAllowDefault: true,
		FlagAllowDNS:     false,
		Flag("bogus"):    false,
	}
	for f, w := range want {
		if got := n.Flag(f); got != w {
			t.Errorf("Flag(%q) = %v, want %v", f, got, w)
		}
	}
}

func TestNetworkAuthExpiry(t *testing.T) {
	if _, ok := (Network{}).AuthExpiry(); ok {
		t.Error("AuthExpiry of a network without SSO: ok = true, want false")
	}
	n := Network{AuthenticationExpiryTime: 1792432800000}
	got, ok := n.AuthExpiry()
	want := time.Date(2026, time.October, 19, 18, 0, 0, 0, time.UTC)
	if !ok || !got.Equal(want) {
		t.Errorf("AuthExpiry() = %v, %v; want %v, true", got, ok, want)
	}
}

// TestRealFixtures decodes replies captured from ZeroTier One 1.16.2 (with
// IDs, IPs and MACs replaced by fake ones; see docs/testing.md). The node
// had no networks, so the network files come from joining a network that
// doesn't exist on the node's own controller: it shows NOT_FOUND.
func TestRealFixtures(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		var s NodeStatus
		mustDecode(t, readFixture(t, "1.16.2/status.json"), &s)
		if s.Address != "a1b2c3d4e5" || !s.Online || s.Version != "1.16.2" || s.VersionMinor != 16 {
			t.Errorf("status = %+v", s)
		}
		if !s.TCPFallbackActive || s.PlanetWorldID != 149604618 {
			t.Errorf("tcpFallbackActive %v, planetWorldId %d", s.TCPFallbackActive, s.PlanetWorldID)
		}
		settings := s.Config.Settings
		if settings.PrimaryPort != 9993 || settings.SecondaryPort == 0 || len(settings.ListeningOn) == 0 || len(settings.SurfaceAddresses) == 0 {
			t.Errorf("settings = %+v", settings)
		}
	})

	t.Run("network list", func(t *testing.T) {
		var nets []Network
		mustDecode(t, readFixture(t, "1.16.2/network.json"), &nets)
		if len(nets) != 1 {
			t.Fatalf("got %d networks, want 1", len(nets))
		}
		n := nets[0]
		if n.ID != "a1b2c3d4e5dead01" || n.Status != StatusNotFound || n.Type != "PRIVATE" || n.MTU != 2800 {
			t.Errorf("network = %+v", n)
		}
		if !n.AllowManaged || n.AllowGlobal || n.AllowDefault || n.AllowDNS {
			t.Errorf("flags are not ZeroTier's defaults: %+v", n)
		}
		// Empty lists arrive as [], not null.
		if n.AssignedAddresses == nil || n.Routes == nil || n.DNS.Servers == nil {
			t.Errorf("empty lists decoded as nil: %+v", n)
		}
	})

	t.Run("network right after join", func(t *testing.T) {
		var n Network
		mustDecode(t, readFixture(t, "1.16.2/network-requesting-configuration.json"), &n)
		if n.Status != StatusRequestingConfiguration || n.Name != "" || n.NetconfRevision != 0 {
			t.Errorf("network = %+v", n)
		}
	})

	t.Run("no networks", func(t *testing.T) {
		var nets []Network
		mustDecode(t, readFixture(t, "1.16.2/network-empty.json"), &nets)
		if nets == nil || len(nets) != 0 {
			t.Errorf("networks = %#v, want an empty, non-nil slice", nets)
		}
	})
}

// TestHandmadeFixtures decodes the hand-written fixtures, which cover the
// cases a single real node rarely shows: SSO, access denied, IPv6, several
// networks, unknown fields and `"via": null`.
func TestHandmadeFixtures(t *testing.T) {
	t.Run("sso", func(t *testing.T) {
		var n Network
		mustDecode(t, readFixture(t, "handmade/network-sso.json"), &n)
		if n.Status != StatusAuthenticationRequired || !n.SSOEnabled {
			t.Errorf("status %q, ssoEnabled %v; want AUTHENTICATION_REQUIRED, true", n.Status, n.SSOEnabled)
		}
		if n.AuthenticationURL != "https://sso.example.com/authorize?nwid=c0ffee1234000003&state=abc123" {
			t.Errorf("authenticationURL = %q", n.AuthenticationURL)
		}
		if _, ok := n.AuthExpiry(); !ok {
			t.Error("AuthExpiry: ok = false, want true")
		}
	})

	t.Run("access denied", func(t *testing.T) {
		var n Network
		mustDecode(t, readFixture(t, "handmade/network-access-denied.json"), &n)
		if n.Status != StatusAccessDenied || n.Name != "" || len(n.AssignedAddresses) != 0 {
			t.Errorf("got status %q, name %q, addresses %v; want ACCESS_DENIED, no name, no addresses",
				n.Status, n.Name, n.AssignedAddresses)
		}
		if n.SSOEnabled || n.AuthenticationURL != "" {
			t.Error("SSO fields are set, but the JSON has none")
		}
	})

	t.Run("ipv6", func(t *testing.T) {
		var n Network
		mustDecode(t, readFixture(t, "handmade/network-ipv6.json"), &n)
		if len(n.AssignedAddresses) != 3 || n.AssignedAddresses[0] != "fdc0:ffee:1234:0:499:93a1:b2c3:d4e5/88" {
			t.Errorf("assignedAddresses = %v", n.AssignedAddresses)
		}
		wantRoutes := []Route{
			{Target: "2001:db8:42::/64", Via: ""}, // "via": null
			{Target: "2001:db8:99::/48", Via: "2001:db8:42::1", Metric: 100},
		}
		if len(n.Routes) != len(wantRoutes) {
			t.Fatalf("routes = %+v, want %+v", n.Routes, wantRoutes)
		}
		for i := range wantRoutes {
			if n.Routes[i] != wantRoutes[i] {
				t.Errorf("route %d = %+v, want %+v", i, n.Routes[i], wantRoutes[i])
			}
		}
		if n.DNS.Domain != "v6.example" || len(n.DNS.Servers) != 2 {
			t.Errorf("dns = %+v", n.DNS)
		}
		if !n.AllowDNS || !n.AllowGlobal || n.AllowDefault || !n.AllowManaged {
			t.Errorf("flags = managed %v global %v default %v dns %v", n.AllowManaged, n.AllowGlobal, n.AllowDefault, n.AllowDNS)
		}
	})

	t.Run("multiple networks with unknown fields", func(t *testing.T) {
		var nets []Network
		mustDecode(t, readFixture(t, "handmade/networks-multiple.json"), &nets)
		if len(nets) != 3 {
			t.Fatalf("got %d networks, want 3", len(nets))
		}
		if nets[0].ID != "c0ffee1234000002" || nets[0].Name != "office" {
			t.Errorf("first network = %s %q", nets[0].ID, nets[0].Name)
		}
		if nets[1].Routes[0].Via != "" || nets[1].Routes[1].Via != "10.147.17.1" {
			t.Errorf("routes = %+v", nets[1].Routes)
		}
		if nets[2].Status != StatusUnknown {
			t.Errorf("unknown status decoded as %q, want %q", nets[2].Status, StatusUnknown)
		}
		for _, n := range nets {
			if !n.ID.Valid() {
				t.Errorf("network ID %q is not valid", n.ID)
			}
		}
	})
}

// mustDecode decodes JSON or fails the test.
func mustDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
