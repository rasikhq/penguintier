package tray

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// TestRefreshNoChangeNoCalls is the tray rule "update only what changed":
// a poll that finds nothing new must not touch the tray library at all.
func TestRefreshNoChangeNoCalls(t *testing.T) {
	for _, scenario := range ztmock.Scenarios {
		t.Run(scenario, func(t *testing.T) {
			env := newTestEnv(t, scenario)
			ctx := t.Context()

			env.c.refresh(ctx)
			if calls := env.backend.takeCalls(); len(calls) == 0 {
				t.Fatal("the first refresh made no calls; it should build the menu")
			}
			for i := 0; i < 3; i++ {
				env.c.refresh(ctx)
				if calls := env.backend.takeCalls(); len(calls) != 0 {
					t.Fatalf("refresh %d with nothing changed made calls:\n%s", i+2, strings.Join(calls, "\n"))
				}
			}
		})
	}
}

// TestRefreshUpdatesInPlace: a changed check mark is one Check call, not a
// rebuild.
func TestRefreshUpdatesInPlace(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)
	env.backend.takeCalls()

	// Change a setting behind the tray's back, like zerotier-cli would.
	if _, err := env.client.SetFlags(ctx, "c0ffee1234000001", map[ztapi.Flag]bool{ztapi.FlagAllowGlobal: true}); err != nil {
		t.Fatal(err)
	}
	env.c.refresh(ctx)
	calls := env.backend.takeCalls()
	want := []string{"Check Allow Global IPs"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if a := env.c.actions["net/c0ffee1234000001/flag/allowGlobal"]; a.Value {
		t.Errorf("after checking, a click should turn the flag off; action is %v", a)
	}
}

// TestRefreshRebuildsOnStructureChange: a network that disappears changes
// the menu's structure, so the menu is rebuilt.
func TestRefreshRebuildsOnStructureChange(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)
	env.backend.takeCalls()

	if err := env.client.Leave(ctx, "c0ffee1234000002"); err != nil {
		t.Fatal(err)
	}
	env.c.refresh(ctx)
	if calls := env.backend.takeCalls(); !slices.Contains(calls, "ResetMenu") {
		t.Fatalf("calls = %q, want a ResetMenu", calls)
	}
	if env.has("net/c0ffee1234000002") {
		t.Error("the left network is still in the menu")
	}
}

// TestRebuildStopsClickWatchers: rebuilding many times must not pile up
// click goroutines, and stopping ends them all.
func TestRebuildStopsClickWatchers(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)
	before := runtime.NumGoroutine()

	// Two view models with different structures: every render rebuilds.
	withNetworks := env.c.vm
	without := withNetworks
	without.Networks = nil
	for i := 0; i < 50; i++ {
		env.c.render(without)
		env.c.render(withNetworks)
	}
	if after := runtime.NumGoroutine(); after > before+5 {
		t.Errorf("goroutines grew from %d to %d after 100 rebuilds", before, after)
	}

	clickable := len(env.c.actions)
	env.c.stopClickWatchers()
	// stopClickWatchers waits for them, so they are gone now.
	if after := runtime.NumGoroutine(); after > before-clickable+5 {
		t.Errorf("%d goroutines left after stopping, want about %d", after, before-clickable)
	}
}

func TestClickCopy(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)

	env.c.handleClick(ctx, "address")
	env.c.handleClick(ctx, "net/c0ffee1234000001/address/1")
	env.c.handleClick(ctx, "net/c0ffee1234000001/route/1")
	want := []string{"a1b2c3d4e5", "fdc0:ffee:1234:0:199:93a1:b2c3:d4e5", "192.168.100.0/24"}
	if !slices.Equal(env.copied, want) {
		t.Errorf("copied %q, want %q", env.copied, want)
	}
	// Only "My Address" announces the copy.
	notes := env.notes.all()
	if len(notes) != 1 || notes[0].Summary != "Copied to clipboard" || notes[0].Body != "a1b2c3d4e5" {
		t.Errorf("notifications = %+v, want one 'Copied to clipboard'", notes)
	}
}

func TestClickCopyWithoutClipboard(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.copyText = func(context.Context, string) error { return errors.New("no tool") }
	env.c.refresh(ctx)

	env.c.handleClick(ctx, "net/c0ffee1234000001/id")
	notes := env.notes.all()
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "c0ffee1234000001") {
		t.Errorf("notifications = %+v, want one showing the network ID", notes)
	}
}

func TestClickSetFlag(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)

	env.c.handleClick(ctx, "net/c0ffee1234000002/flag/allowDNS")
	env.c.refresh(ctx)
	if !env.shown(t, "net/c0ffee1234000002/flag/allowDNS").Checked {
		t.Error("Allow DNS Configuration is not checked after clicking it")
	}
	n, err := env.client.Network(ctx, "c0ffee1234000002")
	if err != nil || !n.AllowDNS {
		t.Errorf("service has allowDNS = %v (err %v), want true", n.AllowDNS, err)
	}
}

// TestClickSetFlagLeftNetwork: a click on a network that was left in the
// meantime must not join it again (SetFlags would).
func TestClickSetFlagLeftNetwork(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)
	if err := env.client.Leave(ctx, "c0ffee1234000002"); err != nil {
		t.Fatal(err)
	}
	env.c.vm.Networks = env.c.vm.Networks[:1] // the tray noticed the leave

	env.c.handleClick(ctx, "net/c0ffee1234000002/flag/allowDNS") // still queued from the old menu
	if _, err := env.client.Network(ctx, "c0ffee1234000002"); !ztapi.IsNotFound(err) {
		t.Errorf("the left network was joined again (err %v)", err)
	}
}

func TestDisconnectReconnectForget(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	const id = ztapi.NetworkID("c0ffee1234000001") // home-lab, with allowDNS on
	env.c.refresh(ctx)

	// Disconnect: settings saved, network left, listed under Saved.
	env.c.handleClick(ctx, "net/"+id.String()+"/disconnect")
	env.c.refresh(ctx)
	saved, err := env.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := ztapi.SavedNetwork{Name: "home-lab", AllowManaged: true, AllowDNS: true}
	if saved[id] != want {
		t.Errorf("saved %+v, want %+v", saved[id], want)
	}
	if env.has("net/" + id.String()) {
		t.Error("the network is still listed as joined")
	}
	if !env.has("saved/" + id.String()) {
		t.Error("the network is not listed under Saved Networks")
	}

	// Reconnect: joined again with the saved settings.
	env.c.handleClick(ctx, "saved/"+id.String()+"/reconnect")
	n, err := env.client.Network(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !n.AllowDNS || !n.AllowManaged {
		t.Errorf("reconnected with allowDNS=%v allowManaged=%v, want both true", n.AllowDNS, n.AllowManaged)
	}
	env.c.refresh(ctx)
	if env.has("saved/" + id.String()) {
		t.Error("a joined network is still listed under Saved Networks")
	}

	// Forget: gone from the saved store.
	env.c.handleClick(ctx, "net/"+id.String()+"/disconnect")
	env.c.refresh(ctx)
	env.c.handleClick(ctx, "saved/"+id.String()+"/forget")
	env.c.refresh(ctx)
	saved, err = env.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved[id]; ok {
		t.Error("the network is still saved after Forget")
	}
	if env.has("saved/" + id.String()) {
		t.Error("the network is still listed under Saved Networks after Forget")
	}
}

// TestJoin goes through the Join dialog and the "Joining…" row.
func TestJoin(t *testing.T) {
	env := newTestEnv(t, ztmock.Joining) // the mock hides new joins for 5 s
	ctx := t.Context()
	const id = ztapi.NetworkID("c0ffee12340000f0")
	env.c.refresh(ctx)

	env.askReply = joinResult{id: id, ok: true}
	env.c.handleClick(ctx, "join")
	env.c.handleClick(ctx, "join") // a second click while the dialog is open is ignored
	env.c.handleJoinResult(ctx, <-env.c.joinResults)
	env.c.refresh(ctx)
	if !env.has("joining/" + id.String()) {
		t.Fatal("no Joining… row after joining")
	}

	env.clock.Advance(6 * time.Second)
	env.c.refresh(ctx)
	if env.has("joining/" + id.String()) {
		t.Error("the Joining… row stayed after the network appeared")
	}
	if !env.has("net/" + id.String()) {
		t.Error("the joined network is not in the menu")
	}
	select {
	case res := <-env.c.joinResults:
		t.Errorf("a second Join dialog ran: %+v", res)
	default:
	}
}

func TestJoinCancelledOrFailed(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)

	env.askReply = joinResult{ok: false} // Cancel
	env.c.handleClick(ctx, "join")
	env.c.handleJoinResult(ctx, <-env.c.joinResults)
	if len(env.c.joining) != 0 || len(env.notes.all()) != 0 {
		t.Errorf("cancel joined %v or notified %+v", env.c.joining, env.notes.all())
	}

	env.askReply = joinResult{err: errors.New("no dialog program")}
	env.c.handleClick(ctx, "join")
	env.c.handleJoinResult(ctx, <-env.c.joinResults)
	if notes := env.notes.all(); len(notes) != 1 || notes[0].Summary != "Cannot join a network" {
		t.Errorf("notifications = %+v, want one error", notes)
	}
}

// TestJoiningRowTimesOut: a join that never shows up is dropped after
// joinTimeout.
func TestJoiningRowTimesOut(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.joining["c0ffee12340000f1"] = env.clock.Now()
	env.c.refresh(ctx)
	if !env.has("joining/c0ffee12340000f1") {
		t.Fatal("no Joining… row")
	}
	env.clock.Advance(joinTimeout + time.Second)
	env.c.refresh(ctx)
	if env.has("joining/c0ffee12340000f1") {
		t.Error("the Joining… row is still there after the timeout")
	}
}

// TestSSOLogin: one notification per network that needs a login, and its
// action opens the network's current login page.
func TestSSOLogin(t *testing.T) {
	env := newTestEnv(t, ztmock.SSORequired)
	ctx := t.Context()
	const url = "https://sso.example.com/authorize?nwid=c0ffee1234000003&state=mock"

	env.c.refresh(ctx)
	env.c.refresh(ctx)
	notes := env.notes.all()
	if len(notes) != 1 {
		t.Fatalf("got %d notifications after two polls, want 1: %+v", len(notes), notes)
	}
	if !slices.ContainsFunc(notes[0].Actions, func(a notify.Action) bool { return a.Key == notify.DefaultAction }) {
		t.Errorf("the login notification has no default action: %+v", notes[0].Actions)
	}

	env.c.handleNotificationAction(ctx, notify.ActionEvent{ID: 1, Key: "open-login"})
	env.c.handleNotificationAction(ctx, notify.ActionEvent{ID: 99, Key: "open-login"}) // not ours
	env.c.handleClick(ctx, "login/c0ffee1234000003")
	env.c.handleClick(ctx, "net/c0ffee1234000003/sso-open")
	if want := []string{url, url, url}; !slices.Equal(env.opened, want) {
		t.Errorf("opened %q, want %q", env.opened, want)
	}
}

func TestAuthorize(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	working := env.c.connect
	env.c.connect = func() (*ztapi.Client, error) {
		// Like ztapi.Discover with no token file yet.
		return ztapi.New(env.client.BaseURL(), ""), nil
	}
	env.c.refresh(ctx)
	if env.c.vm.Service != ServiceNeedsAuth || !env.has("authorize") {
		t.Fatalf("service %v, want ServiceNeedsAuth with an Authorize entry", env.c.vm.Service)
	}

	// Authorize fails: the user is told, nothing else changes.
	env.hooks.authorizeErr = errors.New("dismissed")
	env.c.handleClick(ctx, "authorize")
	env.c.handleAuthResult(ctx, <-env.c.authResults)
	if notes := env.notes.all(); len(notes) != 1 {
		t.Errorf("notifications = %+v, want one error", notes)
	}

	// Authorize works: the next poll makes a new client with the token.
	env.hooks.authorizeErr = nil
	env.c.connect = working
	env.c.handleClick(ctx, "authorize")
	env.c.handleAuthResult(ctx, <-env.c.authResults)
	env.c.refresh(ctx)
	if env.c.vm.Service != ServiceOK {
		t.Errorf("service %v after authorizing, want ServiceOK", env.c.vm.Service)
	}
}

func TestStartAtLoginAboutQuit(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx := t.Context()
	env.c.refresh(ctx)

	env.c.handleClick(ctx, "start-at-login")
	env.c.refresh(ctx)
	if !env.shown(t, "start-at-login").Checked {
		t.Error("Start at Login is not checked after clicking it")
	}

	env.c.handleClick(ctx, "about") // no UI: falls back to a notification
	if notes := env.notes.all(); len(notes) != 1 || notes[0].Summary != "Penguintier v9.9.9" {
		t.Errorf("notifications = %+v, want the About fallback", notes)
	}

	if quit := env.c.handleClick(ctx, "quit"); !quit {
		t.Error("Quit did not ask to quit")
	}
	if quit := env.c.handleClick(ctx, "no-such-entry"); quit {
		t.Error("an unknown entry asked to quit")
	}
}

// TestRun drives the real loop: clicks arrive through the library's
// channels, and Quit ends it with every click goroutine stopped.
func TestRun(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		env.c.run(ctx)
		close(done)
	}()

	address := waitForItem(t, env.backend, "My Address: a1b2c3d4e5")
	address.clicked <- struct{}{}
	waitFor(t, func() bool {
		return len(env.notes.all()) == 1
	}, "the copy notification")

	quit := waitForItem(t, env.backend, "Quit")
	quit.clicked <- struct{}{}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after Quit")
	}
	if env.c.stopClicks != nil {
		t.Error("click watchers were not stopped")
	}
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Errorf("goroutines grew from %d to %d", before, after)
	}
}

// TestRunStopsOnCancel: cancelling the context ends run.
func TestRunStopsOnCancel(t *testing.T) {
	env := newTestEnv(t, ztmock.Online)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		env.c.run(ctx)
		close(done)
	}()
	waitForItem(t, env.backend, "Quit")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

// TestLabelsAreEscaped: the controller hands escaped labels to the
// library, in the style of the tray host it found.
func TestLabelsAreEscaped(t *testing.T) {
	env := newTestEnv(t, ztmock.ManyNetworks)
	env.c.detectHost = func() (labelStyle, bool) { return labelStyleGNOME, true }
	env.c.refresh(t.Context())
	if env.backend.find("some__new_state (c0ffee123400000c)") == nil {
		t.Error("no GNOME-escaped entry for some_new_state")
	}
}

// TestHostStyleChangeRebuilds: when the tray host starts after us and
// wants another label style, the menu is rebuilt with it.
func TestHostStyleChangeRebuilds(t *testing.T) {
	env := newTestEnv(t, ztmock.ManyNetworks)
	ctx := t.Context()
	env.c.detectHost = func() (labelStyle, bool) { return labelStyleSpec, false }
	env.c.refresh(ctx)
	if env.backend.find("lab__01 (c0ffee1234000003)") == nil {
		t.Fatal("no spec-escaped entry before a host is found")
	}

	env.c.detectHost = func() (labelStyle, bool) { return labelStyleGNOME, true }
	env.backend.takeCalls()
	env.c.refresh(ctx)
	if calls := env.backend.takeCalls(); !slices.Contains(calls, "ResetMenu") {
		t.Fatalf("calls = %q, want a rebuild", calls)
	}
	if env.backend.find("some__new_state (c0ffee123400000c)") == nil {
		t.Error("no GNOME-escaped entry after the host was found")
	}
}

// waitForItem waits until the menu has an entry with this label.
func waitForItem(t *testing.T, b *fakeBackend, label string) *fakeItem {
	t.Helper()
	var item *fakeItem
	waitFor(t, func() bool {
		item = b.find(label)
		return item != nil
	}, "menu entry "+label)
	return item
}

// waitFor polls cond for up to 5 seconds.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
