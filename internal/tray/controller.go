package tray

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/platform/clipboard"
	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/platform/open"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// pollInterval is how often the controller asks the service for news.
const pollInterval = 2500 * time.Millisecond

// joinTimeout is how long a "Joining…" row waits for its network to show
// up before it is dropped.
const joinTimeout = 2 * time.Minute

// clickQueueSize is how many clicks can wait while the controller is busy
// (for example during a slow API call). People don't click that fast.
const clickQueueSize = 32

// notifier shows desktop notifications. *notify.Notifier is the real one.
type notifier interface {
	Notify(ctx context.Context, msg notify.Notification) (uint32, error)
}

// joinResult is what the Join dialog's goroutine sends back.
type joinResult struct {
	id  ztapi.NetworkID
	ok  bool // false: the user cancelled
	err error
}

// controller owns all of the tray's state.
//
// Only one goroutine, the one running run, reads or changes the fields
// below. Other goroutines (click watchers, dialogs, the notification
// listener) never touch them; they send messages on the channels instead.
// Because only one goroutine touches the state, it needs no locks.
type controller struct {
	backend Backend
	hooks   Hooks
	store   *ztapi.SavedStore
	version string
	icons   map[IconState][]byte

	// connect makes the API client: ztapi.Discover, in the app.
	connect func() (*ztapi.Client, error)
	client  *ztapi.Client
	// newClientNeeded makes the next poll call connect again, to pick up a
	// token that was missing or rejected the last time.
	newClientNeeded bool
	lastPollErr     string // to log each new poll error only once

	saved        map[ztapi.NetworkID]ztapi.SavedNetwork // copy of the saved store
	joining      map[ztapi.NetworkID]time.Time
	startAtLogin bool
	vm           ViewModel // what the menu shows now, for the click handlers

	// ssoNotified: networks we already told about their SSO login.
	// ssoNotifications: notification ID → the network it is about.
	ssoNotified      map[ztapi.NetworkID]bool
	ssoNotifications map[uint32]ztapi.NetworkID

	joinDialogOpen bool
	authorizing    bool
	enablingDNS    bool

	// promptAuthorize: run Authorize by itself the first time the service
	// says a token is needed (Config.PromptAuthorize). Cleared once used,
	// and once the service answers with a working token.
	promptAuthorize bool
	// connected: Hooks.Connected was called in this run.
	connected bool

	// The menu as shown now.
	shown      []Node
	items      map[string]Item   // Key → library entry (separators have none)
	actions    map[string]Action // Key → what a click does
	icon       IconState
	tooltip    string
	labelStyle labelStyle
	hostKnown  bool // labelStyle came from a running tray host

	// The click watcher goroutines of the current menu (see watchClicks).
	stopClicks chan struct{} // closing it stops them
	// A sync.WaitGroup counts running goroutines: Add(1) before starting
	// one, Done() when it ends, and Wait() blocks until the count is 0.
	clickWatchers sync.WaitGroup

	// Channels that other goroutines send to.
	clicks        chan string // the Key of a clicked entry
	joinResults   chan joinResult
	authResults   chan error
	dnsResults    chan error                // results of Hooks.EnableDNS
	notifyActions <-chan notify.ActionEvent // nil when there is no notifier

	// Side effects. newController sets the real ones; tests replace them.
	notifier     notifier
	copyText     func(ctx context.Context, text string) error
	openURL      func(url string) error
	askNetworkID func(ctx context.Context) (ztapi.NetworkID, bool, error)
	startAbout   func(ctx context.Context) error
	detectHost   func() (labelStyle, bool)
	now          func() time.Time
	// readDNS returns each network's DNS status (a dnsstatus.Probe's Read
	// method in the app). nil shows no DNS rows.
	readDNS func(ctx context.Context, networks []ztapi.Network) map[ztapi.NetworkID]dnsstatus.State
}

// newController sets up a controller. Nothing runs until run is called.
func newController(cfg Config, backend Backend, store *ztapi.SavedStore,
	n notifier, actions <-chan notify.ActionEvent,
) (*controller, error) {
	icons, err := loadIcons()
	if err != nil {
		return nil, err
	}
	hooks := cfg.Hooks
	if hooks == nil {
		hooks = StubHooks{}
	}
	ui := uiLauncher{devDir: cfg.DevUIDir}

	c := &controller{
		backend: backend,
		hooks:   hooks,
		store:   store,
		version: cfg.Version,
		icons:   icons,
		connect: ztapi.Discover,

		promptAuthorize: cfg.PromptAuthorize,

		joining:          map[ztapi.NetworkID]time.Time{},
		ssoNotified:      map[ztapi.NetworkID]bool{},
		ssoNotifications: map[uint32]ztapi.NetworkID{},

		// A channel made with a size is "buffered": that many values can
		// wait in it, and sending only blocks when it is full. The other
		// two are unbuffered: a send waits until run receives it.
		clicks:        make(chan string, clickQueueSize),
		joinResults:   make(chan joinResult),
		authResults:   make(chan error),
		dnsResults:    make(chan error),
		notifyActions: actions,

		notifier: n,
		copyText: clipboard.Copy,
		openURL:  open.URL,
		// ui.askNetworkID without parentheses is a "method value": a
		// function that calls askNetworkID on this ui.
		askNetworkID: ui.askNetworkID,
		startAbout:   ui.startAbout,
		detectHost:   detectTrayHost,
		now:          time.Now,
	}
	c.loadSaved()
	c.startAtLogin = hooks.StartAtLogin()
	return c, nil
}

// detectTrayHost asks the session bus which label style the tray host
// uses (see labels.go).
func detectTrayHost() (labelStyle, bool) {
	// dbus.SessionBus is one connection shared with the tray library; it
	// must not be closed.
	conn, err := dbus.SessionBus()
	if err != nil {
		return labelStyleSpec, false
	}
	return trayHostStyle(conn)
}

// run is the controller goroutine. It returns when ctx is cancelled or
// the user clicks Quit.
func (c *controller) run(ctx context.Context) {
	// A Ticker sends on its channel C every pollInterval.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	// Deferred calls run in reverse order when run returns, however it
	// returns.
	defer c.stopClickWatchers()

	// Run calls refresh once before this loop starts too; polling again
	// right away costs nothing, since an unchanged menu makes no calls.
	c.refresh(ctx)
	for {
		// select waits until one of its cases can go ahead, then runs
		// that case. Each loop handles one event.
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			c.refresh(ctx)

		case key := <-c.clicks:
			if quit := c.handleClick(ctx, key); quit {
				return
			}
			// Show the click's effect right away instead of at the next tick.
			c.refresh(ctx)

		case res := <-c.joinResults:
			c.handleJoinResult(ctx, res)
			c.refresh(ctx)

		case err := <-c.authResults:
			c.handleAuthResult(ctx, err)
			c.refresh(ctx)

		case err := <-c.dnsResults:
			c.handleEnableDNSResult(ctx, err)
			c.refresh(ctx)

		case ev, ok := <-c.notifyActions:
			if !ok {
				// The notifier stopped. Receiving from a nil channel waits
				// forever, so this case never fires again.
				c.notifyActions = nil
				continue
			}
			c.handleNotificationAction(ctx, ev)
		}
	}
}

// refresh polls the service and updates the menu, icon and tooltip.
func (c *controller) refresh(ctx context.Context) {
	vm := NewViewModel(c.poll(ctx))
	c.firstTimeSteps(ctx, &vm)
	c.vm = vm
	c.notifyLogins(ctx, vm)
	c.render(vm)
}

// firstTimeSteps does what happens once per run, the first time the
// service answers: Hooks.Connected when the token works, or the automatic
// Authorize prompt when a token is needed (first run only).
//
// vm is a pointer (*ViewModel) because Connected may turn on Start at
// Login, and the menu should show that at once; through the pointer this
// function changes the caller's view model, not a copy.
func (c *controller) firstTimeSteps(ctx context.Context, vm *ViewModel) {
	switch vm.Service {
	case ServiceOK:
		c.promptAuthorize = false // working token: no prompt needed in this run
		if !c.connected {
			c.connected = true
			c.hooks.Connected()
			c.startAtLogin = c.hooks.StartAtLogin()
			vm.StartAtLogin = c.startAtLogin
		}
	case ServiceNeedsAuth:
		if c.promptAuthorize {
			c.promptAuthorize = false
			c.startAuthorize(ctx)
		}
	}
}

// poll asks the service for the node status and networks, and adds the
// tray's own state.
func (c *controller) poll(ctx context.Context) Snapshot {
	snap := Snapshot{Saved: c.saved, Joining: c.joining, StartAtLogin: c.startAtLogin, EnablingDNS: c.enablingDNS}

	if c.client == nil || c.newClientNeeded {
		client, err := c.connect()
		if err != nil {
			snap.Err = err
			c.logPollError(err)
			return snap
		}
		c.client = client
	}

	snap.Status, snap.Networks, snap.Err = fetch(ctx, c.client)
	c.logPollError(snap.Err)
	if snap.Err == nil && c.readDNS != nil {
		snap.DNS = c.readDNS(ctx, snap.Networks)
	}
	c.newClientNeeded = errors.Is(snap.Err, ztapi.ErrTokenMissing) || errors.Is(snap.Err, ztapi.ErrUnauthorized)
	c.pruneJoining(snap.Networks)
	return snap
}

// fetch gets the node status and the joined networks.
func fetch(ctx context.Context, client *ztapi.Client) (ztapi.NodeStatus, []ztapi.Network, error) {
	if !client.HasToken() {
		// Without a token only /health works. Calling it tells "the
		// service is down" apart from "we are not authorized yet".
		if _, err := client.Health(ctx); err != nil {
			return ztapi.NodeStatus{}, nil, err
		}
		return ztapi.NodeStatus{}, nil, ztapi.ErrTokenMissing
	}
	status, err := client.Status(ctx)
	if err != nil {
		return ztapi.NodeStatus{}, nil, err
	}
	nets, err := client.Networks(ctx)
	if err != nil {
		return ztapi.NodeStatus{}, nil, err
	}
	return status, nets, nil
}

// logPollError logs a poll error once, not on every poll.
func (c *controller) logPollError(err error) {
	text := ""
	if err != nil {
		text = err.Error()
	}
	if text == c.lastPollErr {
		return
	}
	c.lastPollErr = text
	if err != nil {
		slog.Warn("cannot use the ZeroTier service", "err", err)
	} else {
		slog.Info("connected to the ZeroTier service")
	}
}

// pruneJoining drops "Joining…" rows whose network has appeared, or that
// waited too long.
func (c *controller) pruneJoining(nets []ztapi.Network) {
	for _, n := range nets {
		delete(c.joining, n.ID)
	}
	// Deleting from a map while ranging over it is allowed in Go.
	for id, since := range c.joining {
		if c.now().Sub(since) > joinTimeout {
			delete(c.joining, id)
		}
	}
}

// loadSaved reads the saved networks into c.saved.
func (c *controller) loadSaved() {
	saved, err := c.store.Load()
	if err != nil {
		// Show none. Disconnect will fail too (it reads the file before
		// writing it), so nothing gets overwritten.
		slog.Warn("cannot read the saved networks", "err", err)
		saved = map[ztapi.NetworkID]ztapi.SavedNetwork{}
	}
	c.saved = saved
}

// notifyLogins shows one notification for each network that starts
// waiting for an SSO login. It shows another only after that network has
// logged in (or left) and needs a login again.
func (c *controller) notifyLogins(ctx context.Context, vm ViewModel) {
	if vm.Service != ServiceOK {
		return
	}
	waiting := map[ztapi.NetworkID]bool{}
	for _, n := range vm.Networks {
		if !needsLogin(n) {
			continue
		}
		waiting[n.ID] = true
		if c.ssoNotified[n.ID] {
			continue
		}
		c.ssoNotified[n.ID] = true

		id, err := c.notifier.Notify(ctx, notify.Notification{
			Summary: "ZeroTier login required",
			Body:    networkTitle(n.Network) + " needs you to log in before it can connect.",
			Actions: []notify.Action{
				// "default" is what clicking the notification itself does.
				{Key: notify.DefaultAction, Label: "Open login"},
				{Key: "open-login", Label: "Open login"},
			},
		})
		if err != nil {
			slog.Warn("cannot show the login notification", "err", err)
			continue
		}
		c.ssoNotifications[id] = n.ID
	}
	for id := range c.ssoNotified {
		if !waiting[id] {
			delete(c.ssoNotified, id)
		}
	}
}

// render makes the tray show vm, with as few library calls as possible:
// none when nothing changed, in-place updates when only texts and check
// marks changed, and a full rebuild only when entries were added, removed
// or moved.
func (c *controller) render(vm ViewModel) {
	if icon := vm.Icon(); icon != c.icon {
		c.backend.SetIcon(c.icons[icon])
		c.icon = icon
	}
	if tip := vm.Tooltip(); tip != c.tooltip {
		c.backend.SetTooltip(tip)
		c.tooltip = tip
	}

	if !c.hostKnown {
		// How labels must be escaped depends on the tray host, which may
		// start after us (e.g. at login). Ask until one is running; if it
		// wants another style than the one used so far, rebuild.
		if style, found := c.detectHost(); found {
			c.hostKnown = true
			if style != c.labelStyle {
				c.labelStyle = style
				c.items = nil
			}
		}
	}

	nodes := Build(vm)
	if c.items == nil || !sameShape(c.shown, nodes) {
		c.rebuild(nodes)
	} else {
		c.update(c.shown, nodes)
	}
	c.shown = nodes
	c.actions = map[string]Action{}
	collectActions(nodes, c.actions)
}

// sameShape reports whether two menus have the same entries in the same
// places, so one can be turned into the other by changing texts and check
// marks only.
func sameShape(a, b []Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Kind != b[i].Kind {
			return false
		}
		// Clickable entries have a click watcher; entries without an
		// action have none.
		if (a[i].Action.Kind == ActNone) != (b[i].Action.Kind == ActNone) {
			return false
		}
		if !sameShape(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

// rebuild replaces the whole menu with nodes.
func (c *controller) rebuild(nodes []Node) {
	c.stopClickWatchers()
	c.backend.ResetMenu()
	c.items = map[string]Item{}
	c.stopClicks = make(chan struct{})
	for _, n := range nodes {
		c.add(nil, n)
	}
}

// add creates the entry for n (and its children) at the end of parent's
// submenu, or of the top-level menu when parent is nil.
func (c *controller) add(parent Item, n Node) {
	if n.Kind == KindSeparator {
		if parent == nil {
			c.backend.AddSeparator()
		} else {
			parent.AddSeparator()
		}
		return
	}

	label := escapeLabel(n.Label, c.labelStyle)
	checkbox := n.Kind == KindCheckbox
	var item Item
	if parent == nil {
		item = c.backend.AddItem(label, n.Tooltip, checkbox, n.Checked)
	} else {
		item = parent.AddSubItem(label, n.Tooltip, checkbox, n.Checked)
	}
	if n.Disabled {
		item.Disable()
	}
	c.items[n.Key] = item
	if n.Action.Kind != ActNone {
		c.watchClicks(n.Key, item.Clicked())
	}
	for _, child := range n.Children {
		c.add(item, child)
	}
}

// update changes the shown entries from before to after, in place. The
// two menus must have the same shape (see sameShape).
func (c *controller) update(before, after []Node) {
	for i := range after {
		old, n := before[i], after[i]
		if n.Kind != KindSeparator {
			item := c.items[n.Key]
			if n.Label != old.Label {
				item.SetTitle(escapeLabel(n.Label, c.labelStyle))
			}
			if n.Tooltip != old.Tooltip {
				item.SetTooltip(n.Tooltip)
			}
			if n.Checked != old.Checked {
				if n.Checked {
					item.Check()
				} else {
					item.Uncheck()
				}
			}
			if n.Disabled != old.Disabled {
				if n.Disabled {
					item.Disable()
				} else {
					item.Enable()
				}
			}
		}
		c.update(old.Children, n.Children)
	}
}

// collectActions fills into with the action of every enabled, clickable
// entry, by Key.
func collectActions(nodes []Node, into map[string]Action) {
	for _, n := range nodes {
		if n.Action.Kind != ActNone && !n.Disabled {
			into[n.Key] = n.Action
		}
		collectActions(n.Children, into)
	}
}

// watchClicks starts a goroutine that forwards each click on one entry to
// the controller, as the entry's Key.
//
// It sends the Key, not the Action: the controller looks the action up
// when the click arrives, so a click always does what the entry shows now,
// even after an in-place update or a rebuild.
//
// The goroutine ends when the library closes clicked (the entry was
// removed) or when stopClickWatchers closes this menu's stop channel,
// whichever comes first.
func (c *controller) watchClicks(key string, clicked <-chan struct{}) {
	stop := c.stopClicks
	queue := c.clicks
	c.clickWatchers.Add(1)
	go func() {
		defer c.clickWatchers.Done()
		for {
			select {
			case _, ok := <-clicked:
				if !ok {
					return // the entry was removed
				}
				// Never block here: if the queue is full the click is
				// dropped (and logged) instead.
				select {
				case queue <- key:
				default:
					slog.Warn("too many clicks waiting; dropped one", "item", key)
				}
			case <-stop:
				return
			}
		}
	}()
}

// stopClickWatchers ends the current menu's click goroutines and waits
// until they are all gone.
func (c *controller) stopClickWatchers() {
	if c.stopClicks == nil {
		return // no menu yet
	}
	// Closing a channel wakes every goroutine waiting to receive from it.
	close(c.stopClicks)
	c.stopClicks = nil
	c.clickWatchers.Wait()
}
