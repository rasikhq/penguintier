package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// The notification service's D-Bus names, from the spec.
const (
	busName    = "org.freedesktop.Notifications"
	objectPath = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface      = "org.freedesktop.Notifications"
)

// How Penguintier introduces itself to the notification service. appID is
// also the app icon's name and the .desktop file's name, so desktops can
// show the right icon and group the notifications under Penguintier.
const (
	appName = "Penguintier"
	appID   = "io.github.rasikhq.Penguintier"
)

// callTimeout limits each D-Bus call. The service answers in milliseconds;
// this only matters if it hangs.
const callTimeout = 3 * time.Second

// DefaultAction is the action key the service reports when the user clicks
// the notification itself rather than one of its buttons. Add an Action
// with this key to make that click do something.
const DefaultAction = "default"

// Notification is one notification to show.
type Notification struct {
	// Summary is the bold first line.
	Summary string
	// Body is the text below it. It is plain text; Notify escapes it when
	// the service reads markup.
	Body string
	// Actions are buttons, shown in this order. Some services show none.
	Actions []Action
	// Transient notifications are not kept in the desktop's notification
	// list after they disappear. Use it for short "Copied" messages.
	Transient bool
}

// Action is one button on a notification.
type Action struct {
	Key   string // reported back in ActionEvent.Key
	Label string // the button text
}

// ActionEvent says the user clicked an action of a notification we showed.
type ActionEvent struct {
	ID  uint32 // the ID Notify returned
	Key string // the Action's Key, or DefaultAction
}

// Notifier sends notifications and reports clicks on their actions.
// Create it with New and stop it with Close.
type Notifier struct {
	conn *dbus.Conn
	// markup is true when the service reads simple HTML-like markup in
	// the body, so "&" and "<" must be escaped.
	markup  bool
	actions chan ActionEvent
}

// New connects to the session bus and starts listening for action clicks.
//
// It opens its own private connection (not dbus.SessionBus, which the tray
// icon library shares), so Close can safely close it.
func New() (*Notifier, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the session bus: %w", err)
	}
	n, err := newNotifier(conn)
	if err != nil {
		_ = conn.Close() // the setup error is the one worth reporting
		return nil, err
	}
	return n, nil
}

// newNotifier does the work of New on an open connection. Tests call it
// with a connection to their own private bus.
func newNotifier(conn *dbus.Conn) (*Notifier, error) {
	// Ask the bus to send us the service's ActionInvoked signals. Naming
	// the sender means another program on the bus can't fake a click.
	err := conn.AddMatchSignal(
		dbus.WithMatchSender(busName),
		dbus.WithMatchObjectPath(objectPath),
		dbus.WithMatchInterface(iface),
		dbus.WithMatchMember("ActionInvoked"),
	)
	if err != nil {
		return nil, fmt.Errorf("listen for notification actions: %w", err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)

	n := &Notifier{
		conn:    conn,
		actions: make(chan ActionEvent, 16),
	}
	n.markup = n.hasCapability("body-markup")
	go n.forwardActions(signals)
	return n, nil
}

// Actions returns the channel that receives a value each time the user
// clicks an action. It is closed by Close.
//
// The return type "<-chan ActionEvent" is a receive-only channel: callers
// can read from it but not send to it or close it.
func (n *Notifier) Actions() <-chan ActionEvent {
	return n.actions
}

// Close disconnects from the bus. Actions is closed soon after.
func (n *Notifier) Close() error {
	return n.conn.Close()
}

// Notify shows msg and returns its ID, which ActionEvent.ID refers to.
func (n *Notifier) Notify(ctx context.Context, msg Notification) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	body := msg.Body
	if n.markup {
		body = escapeMarkup(body)
	}

	// The spec sends actions as one flat list: key, label, key, label, ...
	actions := []string{}
	hasDefault := false
	for _, a := range msg.Actions {
		actions = append(actions, a.Key, a.Label)
		if a.Key == DefaultAction {
			hasDefault = true
		}
	}
	// Every notification gets a default action (what clicking it does),
	// even if it does nothing. Without one, GNOME Shell "opens the app" on
	// a click: it runs the .desktop file, which starts a second
	// Penguintier, whose "already running" notice would come back on the
	// next click. With one, the click only reports the action to us. The
	// label is rarely shown (GNOME never shows it).
	if !hasDefault {
		actions = append(actions, DefaultAction, "Dismiss")
	}

	// Hints are optional extras. A D-Bus "variant" can hold a value of any
	// type; dbus.MakeVariant wraps a Go value in one.
	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant(appID),
		"urgency":       dbus.MakeVariant(byte(1)), // 1 = normal
	}
	if msg.Transient {
		hints["transient"] = dbus.MakeVariant(true)
	}

	var id uint32
	err := n.conn.Object(busName, objectPath).CallWithContext(ctx, iface+".Notify", 0,
		appName,     // app_name
		uint32(0),   // replaces_id: 0 = a new notification
		appID,       // app_icon
		msg.Summary, // summary
		body,        // body
		actions,     // actions
		hints,       // hints
		int32(-1),   // expire_timeout: -1 = the service's default
	).Store(&id)
	if err != nil {
		return 0, fmt.Errorf("show notification %q: %w", msg.Summary, err)
	}
	return id, nil
}

// hasCapability asks the service whether it supports an optional feature.
// Any failure counts as "no".
func (n *Notifier) hasCapability(want string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	var caps []string
	err := n.conn.Object(busName, objectPath).CallWithContext(ctx, iface+".GetCapabilities", 0).Store(&caps)
	if err != nil {
		return false
	}
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// forwardActions turns ActionInvoked signals into ActionEvents. It runs in
// its own goroutine until Close closes the connection, which makes godbus
// close the signals channel and ends the range loop.
func (n *Notifier) forwardActions(signals <-chan *dbus.Signal) {
	// defer: close our channel when this function returns, so whoever
	// reads Actions sees the end too.
	defer close(n.actions)

	for sig := range signals {
		if sig.Name != iface+".ActionInvoked" || len(sig.Body) != 2 {
			continue
		}
		// sig.Body holds the signal's arguments as "any" values. A type
		// assertion such as sig.Body[0].(uint32) checks the real type;
		// ok is false when it doesn't match.
		id, idOK := sig.Body[0].(uint32)
		key, keyOK := sig.Body[1].(string)
		if !idOK || !keyOK {
			continue
		}

		// select with a default case never waits: if nobody has read the
		// earlier events yet and the buffer is full, this click is
		// dropped rather than stalling the D-Bus connection.
		select {
		case n.actions <- ActionEvent{ID: id, Key: key}:
		default:
		}
	}
}

// markupEscaper replaces the three characters that are special in
// notification body markup.
var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeMarkup(s string) string {
	return markupEscaper.Replace(s)
}
