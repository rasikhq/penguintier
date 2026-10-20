// This test file is in package ztmock_test (note the _test suffix): an
// "external" test package that sees only exported names, exactly like
// other packages using ztmock do.
package ztmock_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// fakeClock is a clock the test moves by hand. atomic.Int64 is safe to
// read from the server's goroutines while the test changes it.
type fakeClock struct{ ms atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ms.Store(1792400000000) // an arbitrary fixed start time
	return c
}

func (c *fakeClock) now() time.Time          { return time.UnixMilli(c.ms.Load()) }
func (c *fakeClock) advance(d time.Duration) { c.ms.Add(d.Milliseconds()) }

// start runs a scenario on an httptest server and returns the server and a
// client with the right token. If clock is not nil the server uses it; it
// is set before the server starts, so no request sees the real clock.
func start(t *testing.T, scenario string, clock *fakeClock) (*ztmock.Server, *ztapi.Client) {
	t.Helper()
	srv, err := ztmock.New(scenario)
	if err != nil {
		t.Fatalf("New(%q): %v", scenario, err)
	}
	if clock != nil {
		srv.Now = clock.now
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ztapi.New(ts.URL, ztmock.Token)
}

func TestScenarios(t *testing.T) {
	ctx := context.Background()

	// Every scenario must at least start.
	for _, name := range ztmock.Scenarios {
		if _, err := ztmock.New(name); err != nil {
			t.Errorf("New(%q): %v", name, err)
		}
	}
	if _, err := ztmock.New("bogus"); err == nil {
		t.Error(`New("bogus"): want an error`)
	}

	t.Run("online", func(t *testing.T) {
		_, c := start(t, ztmock.Online, nil)
		h, err := c.Health(ctx)
		if err != nil || !h.Online || h.Version != "1.16.2" {
			t.Errorf("Health = %+v, %v", h, err)
		}
		s, err := c.Status(ctx)
		if err != nil || s.Address != ztmock.NodeAddress || !s.Online {
			t.Errorf("Status = %+v, %v", s, err)
		}
		nets, err := c.Networks(ctx)
		if err != nil || len(nets) != 2 {
			t.Fatalf("Networks = %+v, %v; want 2", nets, err)
		}
		for _, n := range nets {
			if n.Status != ztapi.StatusOK || len(n.AssignedAddresses) == 0 {
				t.Errorf("network %s: status %q, addresses %v", n.ID, n.Status, n.AssignedAddresses)
			}
		}
		n, err := c.Network(ctx, nets[0].ID)
		if err != nil || n.ID != nets[0].ID {
			t.Errorf("Network(%s) = %+v, %v", nets[0].ID, n, err)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		srv, err := ztmock.New(ztmock.Online)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(srv)
		defer ts.Close()
		c := ztapi.New(ts.URL, "wrongwrongwrongwrongwron")
		if _, err := c.Status(ctx); !errors.Is(err, ztapi.ErrUnauthorized) {
			t.Errorf("Status err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("service-down", func(t *testing.T) {
		_, c := start(t, ztmock.ServiceDown, nil)
		if _, err := c.Health(ctx); !errors.Is(err, ztapi.ErrServiceDown) {
			t.Errorf("Health err = %v, want ErrServiceDown", err)
		}
		if _, err := c.Networks(ctx); !errors.Is(err, ztapi.ErrServiceDown) {
			t.Errorf("Networks err = %v, want ErrServiceDown", err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		_, c := start(t, ztmock.Unauthorized, nil)
		if _, err := c.Health(ctx); err != nil {
			t.Errorf("Health err = %v, want nil (no token needed)", err)
		}
		if _, err := c.Status(ctx); !errors.Is(err, ztapi.ErrUnauthorized) {
			t.Errorf("Status err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("sso-required", func(t *testing.T) {
		clock := newFakeClock()
		_, c := start(t, ztmock.SSORequired, clock)
		nets, err := c.Networks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var waiting, loggedIn int
		for _, n := range nets {
			if !n.SSOEnabled {
				continue
			}
			if !strings.HasPrefix(n.AuthenticationURL, "https://sso.example.com/") {
				t.Errorf("network %s: authenticationURL %q", n.ID, n.AuthenticationURL)
			}
			switch n.Status {
			case ztapi.StatusAuthenticationRequired:
				waiting++
			case ztapi.StatusOK:
				loggedIn++
				expiry, ok := n.AuthExpiry()
				if !ok || !expiry.Equal(clock.now().Add(8*time.Hour)) {
					t.Errorf("network %s: expiry %v, %v; want now + 8h", n.ID, expiry, ok)
				}
			}
		}
		if waiting != 1 || loggedIn != 1 {
			t.Errorf("%d networks waiting for SSO and %d logged in, want 1 and 1", waiting, loggedIn)
		}
	})

	t.Run("access-denied", func(t *testing.T) {
		_, c := start(t, ztmock.AccessDenied, nil)
		nets, err := c.Networks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if countStatus(nets, ztapi.StatusAccessDenied) != 1 {
			t.Errorf("networks = %+v, want one ACCESS_DENIED", nets)
		}
	})

	t.Run("many-networks", func(t *testing.T) {
		_, c := start(t, ztmock.ManyNetworks, nil)
		nets, err := c.Networks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(nets) != 12 {
			t.Errorf("got %d networks, want 12", len(nets))
		}
		// Every status, including the unknown fallback, appears.
		for _, s := range []ztapi.Status{
			ztapi.StatusOK, ztapi.StatusRequestingConfiguration, ztapi.StatusAccessDenied,
			ztapi.StatusNotFound, ztapi.StatusPortError, ztapi.StatusClientTooOld,
			ztapi.StatusAuthenticationRequired, ztapi.StatusUnknown,
		} {
			if countStatus(nets, s) == 0 {
				t.Errorf("no network with status %q", s)
			}
		}
		var underscore bool
		for _, n := range nets {
			underscore = underscore || strings.Contains(n.Name, "_")
		}
		if !underscore {
			t.Error("no network name with an underscore (the tray must test label escaping)")
		}
	})

	t.Run("joining", func(t *testing.T) {
		_, c := start(t, ztmock.Joining, nil)
		nets, err := c.Networks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if countStatus(nets, ztapi.StatusRequestingConfiguration) != 1 {
			t.Errorf("networks = %+v, want one REQUESTING_CONFIGURATION", nets)
		}
	})
}

func countStatus(nets []ztapi.Network, s ztapi.Status) int {
	count := 0
	for _, n := range nets {
		if n.Status == s {
			count++
		}
	}
	return count
}

func TestJoinSetLeave(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	_, c := start(t, ztmock.Online, clock)
	const id = ztapi.NetworkID("0123456789abcdef")

	if _, err := c.Network(ctx, id); !ztapi.IsNotFound(err) {
		t.Fatalf("Network before join: err = %v, want not found", err)
	}

	n, err := c.Join(ctx, id)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if n.ID != id || n.Status != ztapi.StatusRequestingConfiguration || n.Name != "" {
		t.Errorf("right after Join: %+v", n)
	}
	nets, _ := c.Networks(ctx)
	if len(nets) != 3 {
		t.Errorf("after Join: %d networks, want 3", len(nets))
	}

	// Two seconds later the controller has "answered".
	clock.advance(2 * time.Second)
	n, err = c.Network(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.Status != ztapi.StatusOK || n.Name != "mock-abcdef" || len(n.AssignedAddresses) != 1 {
		t.Errorf("after the delay: %+v", n)
	}

	n, err = c.SetFlags(ctx, id, map[ztapi.Flag]bool{ztapi.FlagAllowDNS: true, ztapi.FlagAllowManaged: false})
	if err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	if !n.AllowDNS || n.AllowManaged || n.AllowGlobal {
		t.Errorf("after SetFlags: managed %v global %v dns %v", n.AllowManaged, n.AllowGlobal, n.AllowDNS)
	}
	// The change sticks.
	if n, _ := c.Network(ctx, id); !n.AllowDNS {
		t.Error("allowDNS did not stick")
	}

	if err := c.Leave(ctx, id); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	// Leaving again is fine, like on the real service.
	if err := c.Leave(ctx, id); err != nil {
		t.Errorf("second Leave: %v", err)
	}
	if _, err := c.Network(ctx, id); !ztapi.IsNotFound(err) {
		t.Errorf("Network after Leave: err = %v, want not found", err)
	}
	nets, _ = c.Networks(ctx)
	if len(nets) != 2 {
		t.Errorf("after Leave: %d networks, want 2", len(nets))
	}
}

func TestSetFlagsJoins(t *testing.T) {
	// Like the real service, a POST with flags joins a network that is not
	// joined yet (this is how "Reconnect" works).
	ctx := context.Background()
	_, c := start(t, ztmock.Online, nil)
	saved := ztapi.SavedNetwork{Name: "old", AllowManaged: false, AllowGlobal: true}
	n, err := c.SetFlags(ctx, "0123456789abcdef", saved.Flags())
	if err != nil {
		t.Fatal(err)
	}
	if n.AllowManaged || !n.AllowGlobal || n.Status != ztapi.StatusRequestingConfiguration {
		t.Errorf("reconnected network = %+v", n)
	}
}

func TestJoiningScenarioTiming(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	_, c := start(t, ztmock.Joining, clock)
	const id = ztapi.NetworkID("0123456789abcdef")

	if _, err := c.Join(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Hidden for 5 seconds: the tray keeps showing "Joining…".
	if _, err := c.Network(ctx, id); !ztapi.IsNotFound(err) {
		t.Errorf("right after Join: err = %v, want not found", err)
	}
	clock.advance(5 * time.Second)
	n, err := c.Network(ctx, id)
	if err != nil || n.Status != ztapi.StatusRequestingConfiguration {
		t.Errorf("after 5s: %+v, %v; want REQUESTING_CONFIGURATION", n, err)
	}
	clock.advance(10 * time.Second)
	n, err = c.Network(ctx, id)
	if err != nil || n.Status != ztapi.StatusOK {
		t.Errorf("after 15s: %+v, %v; want OK", n, err)
	}
}

func TestJoinOutcomeDependsOnScenario(t *testing.T) {
	ctx := context.Background()
	tests := map[string]ztapi.Status{
		ztmock.AccessDenied: ztapi.StatusAccessDenied,
		ztmock.SSORequired:  ztapi.StatusAuthenticationRequired,
		ztmock.ManyNetworks: ztapi.StatusOK,
	}
	for scenario, want := range tests {
		t.Run(scenario, func(t *testing.T) {
			clock := newFakeClock()
			_, c := start(t, scenario, clock)
			if _, err := c.Join(ctx, "0123456789abcdef"); err != nil {
				t.Fatal(err)
			}
			clock.advance(time.Minute)
			n, err := c.Network(ctx, "0123456789abcdef")
			if err != nil || n.Status != want {
				t.Errorf("after joining: %+v, %v; want status %q", n, err, want)
			}
		})
	}
}

func TestSetScenario(t *testing.T) {
	ctx := context.Background()
	srv, c := start(t, ztmock.Online, nil)
	if _, err := c.Join(ctx, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	if err := srv.SetScenario(ztmock.ManyNetworks); err != nil {
		t.Fatal(err)
	}
	if srv.Scenario() != ztmock.ManyNetworks {
		t.Errorf("Scenario() = %q", srv.Scenario())
	}
	nets, err := c.Networks(ctx)
	if err != nil || len(nets) != 12 {
		t.Errorf("after switching: %d networks, %v; want 12 (the join is reset)", len(nets), err)
	}

	if err := srv.SetScenario("bogus"); err == nil {
		t.Error(`SetScenario("bogus"): want an error`)
	}
	if srv.Scenario() != ztmock.ManyNetworks {
		t.Error("a failed SetScenario changed the scenario")
	}
}

func TestBadRequests(t *testing.T) {
	srv, err := ztmock.New(ztmock.Online)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, path string
		want         int
	}{
		{"POST", "/network/C0FFEE1234000001", http.StatusBadRequest}, // upper case
		{"POST", "/network/nope", http.StatusBadRequest},
		{"GET", "/network/nope", http.StatusNotFound},
		{"GET", "/nothing-here", http.StatusNotFound},
		{"PUT", "/status", http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		req := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
		req.Header.Set("X-ZT1-Auth", ztmock.Token)
		// httptest.NewRecorder captures the reply without a real network.
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
