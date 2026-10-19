package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// These tests run the real daemon code against:
//   - zt-mock's "online" scenario, where network c0ffee1234000001
//     ("home-lab", interface zt34000001) has allowDNS on, domain home.arpa
//     and server 10.147.17.1, and c0ffee1234000002 ("office",
//     zt34000002) has no DNS;
//   - a fake resolved on a private dbus-daemon, knowing those interfaces
//     as links 12 and 13;
//   - a fake /sys/class/net (fakeInterfaces).

const (
	homeLabID   = ztapi.NetworkID("c0ffee1234000001")
	homeLabLink = "zt34000001"
)

// wantApplyCalls is what a first apply of home-lab looks like on the bus.
var wantApplyCalls = []string{
	"SetLinkDefaultRoute 12 false",
	"SetLinkDomains 12 ~home.arpa",
	"SetLinkDNS 12 10.147.17.1",
}

type testEnv struct {
	bus       *resolvedtest.Bus
	fake      *resolvedtest.Fake // nil until startResolved
	mock      *ztmock.Server
	zt        *ztapi.Client // a second client, for the test to change flags
	ztURL     string
	requests  *atomic.Int64 // API requests the mock served
	ifaces    *fakeInterfaces
	statePath string
	logs      *syncBuffer
}

// newEnv sets everything up. withResolved says whether the fake resolved
// runs from the start.
func newEnv(t *testing.T, scenario string, withResolved bool) *testEnv {
	t.Helper()
	env := &testEnv{
		bus:       resolvedtest.StartTestBus(t),
		ifaces:    &fakeInterfaces{m: map[string]int{homeLabLink: 12, "zt34000002": 13}},
		statePath: filepath.Join(t.TempDir(), stateFileName),
		logs:      &syncBuffer{},
		requests:  &atomic.Int64{},
	}

	mock, err := ztmock.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	env.mock = mock
	// Count requests, to check that the daemon stays idle without resolved.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.requests.Add(1)
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	env.ztURL = server.URL
	env.zt = ztapi.New(server.URL, ztmock.Token)

	if withResolved {
		env.startResolved(t)
	}
	return env
}

func (env *testEnv) startResolved(t *testing.T) {
	t.Helper()
	fake, err := resolvedtest.NewFake(env.bus.Address, map[int]string{12: homeLabLink, 13: "zt34000002"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	env.fake = fake
}

// newDaemon makes a daemon the way main does, loading the state file.
func (env *testEnv) newDaemon(t *testing.T) *daemon {
	t.Helper()
	dns, err := resolved.ConnectAddress(env.bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dns.Close() })
	applied, err := loadState(env.statePath)
	if err != nil {
		t.Fatal(err)
	}
	d := newDaemon(ztapi.New(env.ztURL, ztmock.Token), dns, newLogger(env.logs, "debug", false), env.statePath, applied)
	d.ifIndex = env.ifaces.lookup
	return d
}

func (env *testEnv) setAllowDNS(t *testing.T, on bool) {
	t.Helper()
	if _, err := env.zt.SetFlags(context.Background(), homeLabID, map[ztapi.Flag]bool{ztapi.FlagAllowDNS: on}); err != nil {
		t.Fatal(err)
	}
}

// checkCalls compares the fake's write calls with want, then clears them.
func (env *testEnv) checkCalls(t *testing.T, want []string) {
	t.Helper()
	got := env.fake.Calls()
	if !slices.Equal(got, want) {
		t.Errorf("D-Bus writes = %q\nwant           %q", got, want)
	}
	env.fake.ResetCalls()
}

func (env *testEnv) savedLinks(t *testing.T) []string {
	t.Helper()
	links, err := loadState(env.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range links {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func mustCheck(t *testing.T, d *daemon) {
	t.Helper()
	if err := d.check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestDaemonAppliesOnceAndReverts(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	d := env.newDaemon(t)

	// First check: home-lab qualifies, office has no DNS.
	mustCheck(t, d)
	env.checkCalls(t, wantApplyCalls)
	if got := env.savedLinks(t); !slices.Equal(got, []string{homeLabLink}) {
		t.Errorf("state file links = %v, want [%s]", got, homeLabLink)
	}
	servers, domains, defaultRoute, explicit := env.fake.LinkState(12)
	if !slices.Equal(servers, []netip.Addr{netip.MustParseAddr("10.147.17.1")}) ||
		!slices.Equal(domains, []resolved.Domain{{Name: "home.arpa", RoutingOnly: true}}) ||
		defaultRoute || !explicit {
		t.Errorf("link 12 = %v %v defaultRoute=%v explicit=%v", servers, domains, defaultRoute, explicit)
	}

	// Nothing changed: no writes (each one would flush resolved's cache).
	mustCheck(t, d)
	mustCheck(t, d)
	env.checkCalls(t, nil)

	// Allow DNS switched off: revert and forget.
	env.setAllowDNS(t, false)
	mustCheck(t, d)
	env.checkCalls(t, []string{"RevertLink 12"})
	if got := env.savedLinks(t); len(got) != 0 {
		t.Errorf("state file links = %v, want none", got)
	}

	// And on again.
	env.setAllowDNS(t, true)
	mustCheck(t, d)
	env.checkCalls(t, wantApplyCalls)
}

func TestDaemonReappliesAfterDrift(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	d := env.newDaemon(t)
	mustCheck(t, d)
	env.fake.ResetCalls()

	// NetworkManager (or a resolved restart) wiped the link.
	env.fake.SetLinkState(12, nil, nil)
	mustCheck(t, d)
	env.checkCalls(t, wantApplyCalls)
}

func TestDaemonReappliesAfterInterfaceIsRecreated(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	d := env.newDaemon(t)
	mustCheck(t, d)
	env.fake.ResetCalls()

	// ZeroTier restarts: the interface goes away...
	env.ifaces.remove(homeLabLink)
	env.fake.RemoveLink(12)
	mustCheck(t, d)
	env.checkCalls(t, nil)
	if got := env.savedLinks(t); !slices.Equal(got, []string{homeLabLink}) {
		t.Errorf("state file links = %v, want it still remembered", got)
	}

	// ...and comes back with a new index.
	env.ifaces.set(homeLabLink, 20)
	env.fake.AddLink(20, homeLabLink)
	mustCheck(t, d)
	env.checkCalls(t, []string{
		"SetLinkDefaultRoute 20 false",
		"SetLinkDomains 20 ~home.arpa",
		"SetLinkDNS 20 10.147.17.1",
	})
}

func TestDaemonLeavesOthersAlone(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	// Someone else (a future ZeroTier, say) already set DNS on the link.
	env.fake.SetLinkState(12, []netip.Addr{netip.MustParseAddr("10.147.17.1")}, []resolved.Domain{{Name: "home.arpa"}})
	d := env.newDaemon(t)

	mustCheck(t, d)
	mustCheck(t, d)
	env.checkCalls(t, nil)
	if n := strings.Count(env.logs.String(), "leaving them alone"); n != 1 {
		t.Errorf("logged %q %d times, want once:\n%s", "leaving them alone", n, env.logs)
	}

	// Stopping must not revert it either.
	d.revertAll()
	env.checkCalls(t, nil)
}

func TestDaemonRecognizesItsLinksAfterRestart(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	first := env.newDaemon(t)
	mustCheck(t, first)
	env.fake.ResetCalls()

	// A new process (systemd restarted us) reads the state file.
	second := env.newDaemon(t)
	mustCheck(t, second)
	env.checkCalls(t, nil) // already applied: no writes, not "someone else's"
	second.revertAll()
	env.checkCalls(t, []string{"RevertLink 12"})
}

func TestDaemonManagedLink(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	env.fake.SetManaged(12, true)
	d := env.newDaemon(t)

	mustCheck(t, d)
	mustCheck(t, d)
	env.checkCalls(t, nil)
	logs := env.logs.String()
	if n := strings.Count(logs, "Link zt34000001 is managed."); n != 1 {
		t.Errorf("logged the managed error %d times, want once:\n%s", n, logs)
	}

	// networkd lets go of the link: the next check applies, and says so.
	env.fake.SetManaged(12, false)
	mustCheck(t, d)
	env.checkCalls(t, wantApplyCalls)
	if !strings.Contains(env.logs.String(), homeLabLink+": OK again") {
		t.Errorf("no recovery message in the logs:\n%s", env.logs)
	}
}

func TestDaemonIdlesWithoutResolved(t *testing.T) {
	env := newEnv(t, ztmock.Online, false)
	d := env.newDaemon(t)

	mustCheck(t, d)
	mustCheck(t, d)
	if n := env.requests.Load(); n != 0 {
		t.Errorf("made %d ZeroTier API requests while resolved was missing, want 0", n)
	}
	if n := strings.Count(env.logs.String(), "systemd-resolved is not running"); n != 1 {
		t.Errorf("logged 'not running' %d times, want once:\n%s", n, env.logs)
	}
}

func TestDaemonExitsOnUnauthorized(t *testing.T) {
	env := newEnv(t, ztmock.Unauthorized, true)
	d := env.newDaemon(t)
	err := d.check(context.Background())
	if !errors.Is(err, errFatal) || !errors.Is(err, ztapi.ErrUnauthorized) {
		t.Fatalf("check() = %v, want a fatal ErrUnauthorized", err)
	}
}

func TestDaemonKeepsSettingsWhileZeroTierIsDown(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	d := env.newDaemon(t)
	mustCheck(t, d)
	env.fake.ResetCalls()

	if err := env.mock.SetScenario(ztmock.ServiceDown); err != nil {
		t.Fatal(err)
	}
	mustCheck(t, d)
	mustCheck(t, d)
	env.checkCalls(t, nil)
	if n := strings.Count(env.logs.String(), "ZeroTier service:"); n != 1 {
		t.Errorf("logged the API error %d times, want once:\n%s", n, env.logs)
	}
}

// TestDaemonRun runs the whole loop: it starts idle, wakes up when
// resolved appears (the tick interval is an hour, so only the bus signal
// can wake it), and reverts on shutdown.
func TestDaemonRun(t *testing.T) {
	env := newEnv(t, ztmock.Online, false)
	d := env.newDaemon(t)
	d.interval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- d.run(ctx) }()

	// Let run do its first (idle) check and start watching, then start
	// resolved.
	waitFor(t, "the idle message", func() bool {
		return strings.Contains(env.logs.String(), "systemd-resolved is not running")
	})
	env.startResolved(t)
	waitFor(t, "the DNS settings to be applied", func() bool {
		return slices.Equal(env.fake.Calls(), wantApplyCalls)
	})
	env.fake.ResetCalls()

	cancel() // what SIGTERM does
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() = %v, want nil after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return 10 s after cancel")
	}
	env.checkCalls(t, []string{"RevertLink 12"})
	if got := env.savedLinks(t); len(got) != 0 {
		t.Errorf("state file links = %v after shutdown, want none", got)
	}
}

func TestDaemonRunStopsWhenTheBusGoesAway(t *testing.T) {
	env := newEnv(t, ztmock.Online, true)
	d := env.newDaemon(t)
	d.interval = time.Hour
	result := make(chan error, 1)
	go func() { result <- d.run(context.Background()) }()

	waitFor(t, "the first apply", func() bool { return len(env.fake.Calls()) == len(wantApplyCalls) })
	env.bus.Close() // dbus-daemon dies

	select {
	case err := <-result:
		if !errors.Is(err, errFatal) {
			t.Fatalf("run() = %v, want a fatal error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return 10 s after the bus went away")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeInterfaces stands in for /sys/class/net. The daemon goroutine and
// the test both use it, hence the lock.
type fakeInterfaces struct {
	mu sync.Mutex
	m  map[string]int
}

func (f *fakeInterfaces) lookup(name string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index, ok := f.m[name]
	if !ok {
		return 0, resolved.ErrNoInterface
	}
	return index, nil
}

func (f *fakeInterfaces) set(name string, index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[name] = index
}

func (f *fakeInterfaces) remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, name)
}

// syncBuffer is a bytes.Buffer that two goroutines can use at once: the
// daemon writes logs while the test reads them.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
