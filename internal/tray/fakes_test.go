package tray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// TestMain runs before the tests of this package. It silences the log, so
// expected warnings (a failed copy, a refused token, ...) don't clutter
// the test output, then runs the tests (m.Run also parses -update).
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// fakeBackend stands in for fyne.io/systray. It records every call as a
// line of text, and keeps the entries so tests can "click" them.
//
// The controller calls it from its goroutine while a test may read it from
// another, so a mutex guards everything.
type fakeBackend struct {
	mu    sync.Mutex
	calls []string
	items []*fakeItem // entries in the menu now, in creation order
}

type fakeItem struct {
	b       *fakeBackend
	label   string
	checked bool
	clicked chan struct{}
}

func (b *fakeBackend) record(format string, args ...any) {
	b.calls = append(b.calls, fmt.Sprintf(format, args...))
}

func (b *fakeBackend) newItem(label string, checked bool) *fakeItem {
	it := &fakeItem{b: b, label: label, checked: checked, clicked: make(chan struct{})}
	b.items = append(b.items, it)
	return it
}

func (b *fakeBackend) SetIcon(png []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("SetIcon (%d bytes)", len(png))
}

func (b *fakeBackend) SetTooltip(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("SetTooltip %s", text)
}

// ResetMenu closes every entry's channel, like the real library.
func (b *fakeBackend) ResetMenu() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("ResetMenu")
	for _, it := range b.items {
		close(it.clicked)
	}
	b.items = nil
}

func (b *fakeBackend) AddItem(label, _ string, _, checked bool) Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("AddItem %s", label)
	return b.newItem(label, checked)
}

func (b *fakeBackend) AddSeparator() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("AddSeparator")
}

func (it *fakeItem) AddSubItem(label, _ string, _, checked bool) Item {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("AddSubItem %s", label)
	return it.b.newItem(label, checked)
}

func (it *fakeItem) AddSeparator() {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("AddSeparator in %s", it.label)
}

func (it *fakeItem) SetTitle(label string) {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("SetTitle %s -> %s", it.label, label)
	it.label = label
}

func (it *fakeItem) SetTooltip(text string) {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("SetTooltip %s: %s", it.label, text)
}

func (it *fakeItem) Check() {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("Check %s", it.label)
	it.checked = true
}

func (it *fakeItem) Uncheck() {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("Uncheck %s", it.label)
	it.checked = false
}

func (it *fakeItem) Enable() {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("Enable %s", it.label)
}

func (it *fakeItem) Disable() {
	it.b.mu.Lock()
	defer it.b.mu.Unlock()
	it.b.record("Disable %s", it.label)
}

func (it *fakeItem) Clicked() <-chan struct{} {
	return it.clicked
}

// takeCalls returns the calls recorded since the last takeCalls.
func (b *fakeBackend) takeCalls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	calls := b.calls
	b.calls = nil
	return calls
}

// find returns the menu entry with this (escaped) label, or nil.
func (b *fakeBackend) find(label string) *fakeItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, it := range b.items {
		if it.label == label {
			return it
		}
	}
	return nil
}

// fakeNotifier records notifications.
type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Notification
}

func (n *fakeNotifier) Notify(_ context.Context, msg notify.Notification) (uint32, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, msg)
	return uint32(len(n.sent)), nil // IDs 1, 2, 3, ...
}

func (n *fakeNotifier) all() []notify.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.Notification(nil), n.sent...)
}

// fakeClock is a clock tests move by hand. It is shared by the controller
// and zt-mock (which delays joins).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeHooks records Hooks calls.
//
// Authorize and EnableDNS run in their own goroutines. Tests set the
// fields before the click that starts them, and the "go" statement makes
// those writes visible to the new goroutine, so no lock is needed.
type fakeHooks struct {
	authorizeErr error
	authorized   int // Authorize calls
	startAtLogin bool
	connected    int // Connected calls
	// onConnected, if set, runs inside Connected (to turn on Start at
	// Login, like the real hook).
	onConnected  func()
	enableDNSErr error
}

func (h *fakeHooks) Authorize(context.Context) error {
	h.authorized++
	return h.authorizeErr
}
func (h *fakeHooks) StartAtLogin() bool { return h.startAtLogin }
func (h *fakeHooks) SetStartAtLogin(on bool) error {
	h.startAtLogin = on
	return nil
}
func (h *fakeHooks) Connected() {
	h.connected++
	if h.onConnected != nil {
		h.onConnected()
	}
}
func (h *fakeHooks) EnableDNS(context.Context) error { return h.enableDNSErr }

// testEnv is a controller wired to fakes and a zt-mock server.
type testEnv struct {
	c        *controller
	backend  *fakeBackend
	notes    *fakeNotifier
	clock    *fakeClock
	mock     *ztmock.Server
	client   *ztapi.Client // for changing the mock from the outside
	store    *ztapi.SavedStore
	hooks    *fakeHooks
	copied   []string
	opened   []string
	askReply joinResult // what the fake Join dialog answers
}

func newTestEnv(t *testing.T, scenario string) *testEnv {
	t.Helper()
	env := &testEnv{
		backend: &fakeBackend{},
		notes:   &fakeNotifier{},
		clock:   &fakeClock{now: fixedNow},
		hooks:   &fakeHooks{},
	}

	mock, err := ztmock.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	mock.Now = env.clock.Now
	ts := httptest.NewServer(mock)
	t.Cleanup(ts.Close)
	env.mock = mock
	env.client = ztapi.New(ts.URL, ztmock.Token)

	dir := t.TempDir()
	env.store = &ztapi.SavedStore{
		Path:          filepath.Join(dir, "saved_networks.json"),
		DesktopUIPath: filepath.Join(dir, "none.json"),
	}

	c, err := newController(Config{Version: "v9.9.9", Hooks: env.hooks}, env.backend, env.store, env.notes, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.connect = func() (*ztapi.Client, error) { return ztapi.New(ts.URL, ztmock.Token), nil }
	c.copyText = func(_ context.Context, text string) error {
		env.copied = append(env.copied, text)
		return nil
	}
	c.openURL = func(url string) error {
		env.opened = append(env.opened, url)
		return nil
	}
	c.askNetworkID = func(context.Context) (ztapi.NetworkID, bool, error) {
		return env.askReply.id, env.askReply.ok, env.askReply.err
	}
	c.startAbout = func(context.Context) error { return errors.New("no UI in tests") }
	c.detectHost = func() (labelStyle, bool) { return labelStyleSpec, true }
	c.now = env.clock.Now
	env.c = c
	return env
}

// shown returns the shown node with this key.
func (env *testEnv) shown(t *testing.T, key string) Node {
	t.Helper()
	var found *Node
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for i := range nodes {
			if nodes[i].Key == key {
				found = &nodes[i]
			}
			walk(nodes[i].Children)
		}
	}
	walk(env.c.shown)
	if found == nil {
		t.Fatalf("no menu entry %q", key)
	}
	return *found
}

// has reports whether the menu shows an entry with this key.
func (env *testEnv) has(key string) bool {
	_, ok := env.c.items[key]
	return ok
}
