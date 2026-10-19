// These tests run against resolvedtest.Fake on a private dbus-daemon, so
// they need no systemd-resolved and never touch the real system bus.
//
// The package name "resolved_test" (not "resolved") makes this an external
// test package: it sees only the exported API, like real callers do. It
// also avoids an import cycle, since resolvedtest imports resolved.
package resolved_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
)

// setup starts a private bus with a fake resolved knowing link 12
// ("zt3jn5bvfq") and returns a client connected to that bus.
func setup(t *testing.T) (*resolved.Client, *resolvedtest.Fake, *resolvedtest.Bus) {
	t.Helper()
	bus := resolvedtest.StartTestBus(t)
	fake := startFake(t, bus)
	client, err := resolved.ConnectAddress(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, fake, bus
}

func startFake(t *testing.T, bus *resolvedtest.Bus) *resolvedtest.Fake {
	t.Helper()
	fake, err := resolvedtest.NewFake(bus.Address, map[int]string{12: "zt3jn5bvfq"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	return fake
}

// testContext gives each test a deadline, so a hung D-Bus call fails the
// test instead of hanging it.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestSetAndReadLink(t *testing.T) {
	client, fake, _ := setup(t)
	ctx := testContext(t)

	servers := []netip.Addr{netip.MustParseAddr("10.147.17.1"), netip.MustParseAddr("2001:db8:42::53")}
	domains := []resolved.Domain{{Name: "pt.test", RoutingOnly: true}}

	if err := client.SetLinkDefaultRoute(ctx, 12, false); err != nil {
		t.Fatalf("SetLinkDefaultRoute: %v", err)
	}
	if err := client.SetLinkDomains(ctx, 12, domains); err != nil {
		t.Fatalf("SetLinkDomains: %v", err)
	}
	if err := client.SetLinkDNS(ctx, 12, servers); err != nil {
		t.Fatalf("SetLinkDNS: %v", err)
	}

	wantCalls := []string{
		"SetLinkDefaultRoute 12 false",
		"SetLinkDomains 12 ~pt.test",
		"SetLinkDNS 12 10.147.17.1 2001:db8:42::53",
	}
	if got := fake.Calls(); !slices.Equal(got, wantCalls) {
		t.Errorf("calls = %q, want %q", got, wantCalls)
	}

	link, err := client.Link(ctx, 12)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !slices.Equal(link.Servers, servers) {
		t.Errorf("Servers = %v, want %v", link.Servers, servers)
	}
	if !slices.Equal(link.Domains, domains) {
		t.Errorf("Domains = %v, want %v", link.Domains, domains)
	}
	if link.DefaultRoute {
		t.Error("DefaultRoute = true, want false")
	}

	if err := client.RevertLink(ctx, 12); err != nil {
		t.Fatalf("RevertLink: %v", err)
	}
	link, err = client.Link(ctx, 12)
	if err != nil {
		t.Fatalf("Link after revert: %v", err)
	}
	if len(link.Servers) != 0 || len(link.Domains) != 0 || link.DefaultRoute {
		t.Errorf("after revert: %+v, want an empty link", link)
	}
}

func TestEffectiveDefaultRoute(t *testing.T) {
	client, fake, _ := setup(t)
	ctx := testContext(t)
	server := []netip.Addr{netip.MustParseAddr("10.0.0.53")}

	// Servers and only a search domain: resolved makes the link a
	// default route unless told otherwise.
	fake.SetLinkState(12, server, []resolved.Domain{{Name: "corp.example"}})
	if link, err := client.Link(ctx, 12); err != nil || !link.DefaultRoute {
		t.Errorf("search domain only: DefaultRoute = %v (err %v), want true", link.DefaultRoute, err)
	}

	// A routing-only domain switches that off.
	fake.SetLinkState(12, server, []resolved.Domain{{Name: "corp.example", RoutingOnly: true}})
	if link, err := client.Link(ctx, 12); err != nil || link.DefaultRoute {
		t.Errorf("routing-only domain: DefaultRoute = %v (err %v), want false", link.DefaultRoute, err)
	}
}

func TestErrors(t *testing.T) {
	client, fake, _ := setup(t)
	ctx := testContext(t)
	fake.AddLink(13, "ztmanaged")
	fake.SetManaged(13, true)
	server := []netip.Addr{netip.MustParseAddr("10.0.0.53")}

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"unknown link: write", func() error { return client.SetLinkDNS(ctx, 99, server) }, resolved.ErrNoSuchLink},
		{"unknown link: read", func() error { _, err := client.Link(ctx, 99); return err }, resolved.ErrNoSuchLink},
		{"managed link: DNS", func() error { return client.SetLinkDNS(ctx, 13, server) }, resolved.ErrLinkManaged},
		{"managed link: revert", func() error { return client.RevertLink(ctx, 13) }, resolved.ErrLinkManaged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}

	// Reading a managed link still works: only writes are refused.
	if _, err := client.Link(ctx, 13); err != nil {
		t.Errorf("Link(managed) = %v, want no error", err)
	}

	t.Run("access denied", func(t *testing.T) {
		fake.DenyWrites(true)
		defer fake.DenyWrites(false)
		if err := client.SetLinkDefaultRoute(ctx, 12, false); !errors.Is(err, resolved.ErrAccessDenied) {
			t.Errorf("error = %v, want ErrAccessDenied", err)
		}
	})

	t.Run("bad arguments are refused before the call", func(t *testing.T) {
		if err := client.SetLinkDNS(ctx, 12, []netip.Addr{{}}); err == nil {
			t.Error("SetLinkDNS with an invalid address: no error")
		}
		if err := client.RevertLink(ctx, 0); err == nil {
			t.Error("RevertLink(0): no error")
		}
		if got := fake.Calls(); len(got) != 0 {
			t.Errorf("calls = %q, want none", got)
		}
	})
}

func TestNotRunning(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	client, err := resolved.ConnectAddress(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := testContext(t)

	ok, err := client.Available(ctx)
	if err != nil || ok {
		t.Fatalf("Available() = %v, %v; want false, nil", ok, err)
	}
	if err := client.SetLinkDNS(ctx, 12, nil); !errors.Is(err, resolved.ErrNotRunning) {
		t.Errorf("SetLinkDNS without resolved: %v, want ErrNotRunning", err)
	}
	if _, err := client.Link(ctx, 12); !errors.Is(err, resolved.ErrNotRunning) {
		t.Errorf("Link without resolved: %v, want ErrNotRunning", err)
	}
}

func TestWatchOwner(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	client, err := resolved.ConnectAddress(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := testContext(t)

	changes, err := client.WatchOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// resolved starts...
	fake, err := resolvedtest.NewFake(bus.Address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := receive(t, changes); !got {
		t.Error("after start: got false, want true")
	}
	if ok, err := client.Available(ctx); err != nil || !ok {
		t.Errorf("Available() = %v, %v; want true", ok, err)
	}

	// ...and stops.
	fake.Close()
	if got := receive(t, changes); got {
		t.Error("after stop: got true, want false")
	}

	// Closing the client closes the channel.
	_ = client.Close()
	select {
	case _, open := <-changes:
		if open {
			t.Error("got a value after Close, want the channel closed")
		}
	case <-time.After(5 * time.Second):
		t.Error("channel not closed 5 s after Close")
	}
}

func receive(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("no owner change within 5 s")
		return false
	}
}
