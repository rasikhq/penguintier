//go:build live

// The line above is a "build constraint": this file is only compiled with
// `go test -tags live` (`make live-test`). These tests talk to the real
// ZeroTier service on this machine, so normal test runs and CI skip them.

package ztapi

import (
	"context"
	"os"
	"testing"
	"time"
)

// liveClient returns a client for the local service, or skips the test if
// there is no token to use.
func liveClient(t *testing.T) *Client {
	t.Helper()
	c, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !c.HasToken() {
		t.Skip("no API token found; see \"Token helper and polkit policy\" in docs/testing.md to copy one")
	}
	return c
}

// TestLiveReadOnly calls every read-only endpoint and checks the replies
// look sane. It changes nothing.
func TestLiveReadOnly(t *testing.T) {
	ctx := context.Background()
	c := liveClient(t)
	t.Logf("API: %s", c.BaseURL())

	health, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	t.Logf("health: online=%v version=%s", health.Online, health.Version)

	status, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Address) != 10 {
		t.Errorf("node address %q is not 10 characters", status.Address)
	}
	if status.Version != health.Version {
		t.Errorf("/status version %q != /health version %q", status.Version, health.Version)
	}

	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatalf("Networks: %v", err)
	}
	t.Logf("%d networks joined", len(nets))
	for _, n := range nets {
		if !n.ID.Valid() {
			t.Errorf("network ID %q is not valid", n.ID)
		}
		if n.Status == StatusUnknown {
			t.Errorf("network %s has a status this code doesn't know", n.ID)
		}
		one, err := c.Network(ctx, n.ID)
		if err != nil {
			t.Errorf("Network(%s): %v", n.ID, err)
			continue
		}
		if one.ID != n.ID || one.Status != n.Status {
			t.Errorf("Network(%s) = %s %s, list says %s %s", n.ID, one.ID, one.Status, n.ID, n.Status)
		}
	}
}

// TestLiveJoinToggleLeave joins a throwaway network, toggles its flags and
// leaves it. It only runs when PENGUINTIER_LIVE_JOIN=1.
//
// The default network ID is this node's own address followed by "dead01":
// a network on the node's built-in controller that doesn't exist, so the
// join never leaves this machine and ends in NOT_FOUND. Set
// PENGUINTIER_LIVE_NETWORK to use another ID.
func TestLiveJoinToggleLeave(t *testing.T) {
	if os.Getenv("PENGUINTIER_LIVE_JOIN") != "1" {
		t.Skip("set PENGUINTIER_LIVE_JOIN=1 to join, change and leave a throwaway network")
	}
	ctx := context.Background()
	c := liveClient(t)

	id, err := throwawayNetwork(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Network(ctx, id); !IsNotFound(err) {
		t.Fatalf("network %s is already joined (or the check failed: %v); pick another ID with PENGUINTIER_LIVE_NETWORK", id, err)
	}
	t.Logf("throwaway network: %s", id)

	n, err := c.Join(ctx, id)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	// From here on, leave the network even if the test fails. t.Cleanup
	// functions run when the test ends, whatever happened.
	t.Cleanup(func() {
		if err := c.Leave(context.Background(), id); err != nil && !IsNotFound(err) {
			t.Errorf("cleanup: Leave(%s): %v", id, err)
		}
	})
	t.Logf("joined: status %s", n.Status)

	if !inList(ctx, t, c, id) {
		t.Errorf("network %s is not in the network list after Join", id)
	}

	// Give the controller a moment to answer (NOT_FOUND, normally).
	time.Sleep(2 * time.Second)
	if n, err = c.Network(ctx, id); err == nil {
		t.Logf("after 2s: status %s", n.Status)
	}

	steps := []map[Flag]bool{
		{FlagAllowGlobal: true},
		{FlagAllowGlobal: false, FlagAllowDNS: true},
		{FlagAllowDNS: false, FlagAllowDefault: true, FlagAllowManaged: false},
		{FlagAllowDefault: false, FlagAllowManaged: true},
	}
	for _, flags := range steps {
		n, err := c.SetFlags(ctx, id, flags)
		if err != nil {
			t.Fatalf("SetFlags(%v): %v", flags, err)
		}
		// Check both the reply and a fresh read.
		again, err := c.Network(ctx, id)
		if err != nil {
			t.Fatalf("Network after SetFlags: %v", err)
		}
		for f, want := range flags {
			if n.Flag(f) != want || again.Flag(f) != want {
				t.Errorf("after SetFlags(%v): %s is %v in the reply and %v on re-read, want %v",
					flags, f, n.Flag(f), again.Flag(f), want)
			}
		}
	}

	if err := c.Leave(ctx, id); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if _, err := c.Network(ctx, id); !IsNotFound(err) {
		t.Errorf("Network after Leave: err = %v, want not found", err)
	}
	if inList(ctx, t, c, id) {
		t.Errorf("network %s is still listed after Leave", id)
	}
	// Leaving a network that is not joined is fine (the service answers
	// 200 with {}).
	if err := c.Leave(ctx, id); err != nil {
		t.Errorf("second Leave: %v", err)
	}
}

// throwawayNetwork returns $PENGUINTIER_LIVE_NETWORK, or this node's
// address + "dead01".
func throwawayNetwork(ctx context.Context, c *Client) (NetworkID, error) {
	if s := os.Getenv("PENGUINTIER_LIVE_NETWORK"); s != "" {
		return ParseNetworkID(s)
	}
	status, err := c.Status(ctx)
	if err != nil {
		return "", err
	}
	return ParseNetworkID(status.Address + "dead01")
}

func inList(ctx context.Context, t *testing.T, c *Client, id NetworkID) bool {
	t.Helper()
	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatalf("Networks: %v", err)
	}
	for _, n := range nets {
		if n.ID == id {
			return true
		}
	}
	return false
}
