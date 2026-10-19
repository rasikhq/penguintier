package tray

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// Kind is what sort of menu entry a Node is.
type Kind int

// The node kinds.
const (
	// KindItem is a normal entry. With Children it opens a submenu.
	KindItem Kind = iota
	// KindCheckbox is an entry with a check mark that is on or off.
	KindCheckbox
	// KindSeparator is a line between groups of entries.
	KindSeparator
)

// Node is one menu entry as plain data. Build returns a tree of them, and
// the controller turns that tree into the real tray menu.
type Node struct {
	// Key names the entry, e.g. "net/c0ffee1234000001/disconnect". It is
	// unique in the menu and stays the same from one Build to the next,
	// so the controller can match old and new entries. A click is reported
	// as the clicked entry's Key.
	Key      string
	Kind     Kind
	Label    string // the text, before escaping for the tray host
	Tooltip  string
	Checked  bool // KindCheckbox only
	Disabled bool // shown greyed out; clicks do nothing
	Action   Action
	Children []Node
}

// ActionKind is what clicking an entry does.
type ActionKind int

// The actions. ActNone (the zero value) does nothing.
const (
	ActNone         ActionKind = iota
	ActCopy                    // copy Text to the clipboard
	ActJoin                    // ask for a network ID and join it
	ActSetFlag                 // set Flag of Network to Value
	ActOpenURL                 // open Text (an SSO login page) in the browser
	ActDisconnect              // save Network's settings, then leave it
	ActReconnect               // join the saved Network again with its saved settings
	ActForget                  // delete Network's saved settings
	ActAuthorize               // get an API token (Hooks.Authorize)
	ActStartAtLogin            // turn starting at login on or off (Value)
	ActEnableDNS               // install systemd-resolved (Hooks.EnableDNS)
	ActAbout                   // show the About window
	ActQuit                    // quit the tray
)

// Action is what clicking an entry does, with the data it needs. Which
// fields are used depends on Kind (see the ActionKind constants).
type Action struct {
	Kind    ActionKind
	Network ztapi.NetworkID
	Flag    ztapi.Flag
	Value   bool
	Text    string
	// Notify, for ActCopy: also show a "Copied" notification.
	Notify bool
}

// String describes the action, for the golden files and logs. Types with
// a String method print with it in fmt's %s and %v.
func (a Action) String() string {
	switch a.Kind {
	case ActCopy:
		if a.Notify {
			return fmt.Sprintf("copy %q, notify", a.Text)
		}
		return fmt.Sprintf("copy %q", a.Text)
	case ActJoin:
		return "join"
	case ActSetFlag:
		return fmt.Sprintf("set %s %s=%t", a.Network, a.Flag, a.Value)
	case ActOpenURL:
		return fmt.Sprintf("open %q", a.Text)
	case ActDisconnect:
		return "disconnect " + a.Network.String()
	case ActReconnect:
		return "reconnect " + a.Network.String()
	case ActForget:
		return "forget " + a.Network.String()
	case ActAuthorize:
		return "authorize"
	case ActStartAtLogin:
		return fmt.Sprintf("start-at-login=%t", a.Value)
	case ActEnableDNS:
		return "enable-dns"
	case ActAbout:
		return "about"
	case ActQuit:
		return "quit"
	default:
		return "none"
	}
}

// Build turns a ViewModel into the menu. It is a pure function: it only
// looks at vm and returns new data.
//
// The menu is at most two levels deep (top level, and one submenu per
// network), because some tray hosts handle deeper menus badly. A network's
// addresses and routes are therefore sections inside its submenu, not
// submenus of their own.
func Build(vm ViewModel) []Node {
	var nodes []Node
	switch vm.Service {
	case ServiceOK:
		nodes = buildOnline(vm)
	case ServiceNeedsAuth:
		nodes = []Node{
			{Key: "problem", Label: vm.Problem, Disabled: true},
			{Key: "authorize", Label: "Authorize Penguintier…", Action: Action{Kind: ActAuthorize}},
		}
	default:
		nodes = []Node{{Key: "problem", Label: vm.Problem, Disabled: true}}
	}

	// The same last section in every state.
	return append(nodes,
		separator("sep/app"),
		Node{
			Key: "start-at-login", Kind: KindCheckbox, Label: "Start at Login", Checked: vm.StartAtLogin,
			Action: Action{Kind: ActStartAtLogin, Value: !vm.StartAtLogin},
		},
		Node{Key: "about", Label: "About Penguintier", Action: Action{Kind: ActAbout}},
		Node{Key: "quit", Label: "Quit", Action: Action{Kind: ActQuit}},
	)
}

// buildOnline is the menu while the service answers.
func buildOnline(vm ViewModel) []Node {
	address := "My Address: " + vm.Address
	if !vm.Online {
		address += " (offline)"
	}
	nodes := []Node{
		{
			Key: "address", Label: address, Tooltip: "Copy to clipboard",
			Action: Action{Kind: ActCopy, Text: vm.Address, Notify: true},
		},
		separator("sep/join"),
		{Key: "join", Label: "Join New Network…", Action: Action{Kind: ActJoin}},
	}
	for _, id := range vm.Joining {
		nodes = append(nodes, Node{Key: "joining/" + id.String(), Label: "Joining " + id.String() + "…", Disabled: true})
	}

	// Networks waiting for an SSO login get an entry at the top level too,
	// so the login is one click away.
	var logins []Node
	for _, n := range vm.Networks {
		if needsLogin(n) {
			logins = append(logins, Node{
				Key:    "login/" + n.ID.String(),
				Label:  "Log In to " + networkTitle(n.Network) + "…",
				Action: Action{Kind: ActOpenURL, Text: n.AuthenticationURL},
			})
		}
	}
	if len(logins) > 0 {
		nodes = append(nodes, separator("sep/logins"))
		nodes = append(nodes, logins...) // "..." passes each element of the slice
	}

	nodes = append(nodes, separator("sep/networks"))
	if len(vm.Networks) == 0 {
		nodes = append(nodes, Node{Key: "no-networks", Label: "(no networks joined)", Disabled: true})
	}
	for _, n := range vm.Networks {
		nodes = append(nodes, networkNode(n))
	}

	if vm.OfferDNS || vm.EnablingDNS {
		enable := Node{
			Key: "enable-dns", Label: "Enable DNS support…",
			Tooltip: "Switch this computer's DNS to systemd-resolved, so ZeroTier networks can set DNS",
			Action:  Action{Kind: ActEnableDNS},
		}
		if vm.EnablingDNS {
			// Same key and action, so the entry changes in place.
			enable.Label, enable.Disabled = "Switching DNS to systemd-resolved…", true
		}
		nodes = append(nodes, separator("sep/dns"), enable)
	}

	if len(vm.Saved) > 0 {
		nodes = append(nodes,
			separator("sep/saved"),
			Node{Key: "saved", Label: "Saved Networks", Disabled: true},
		)
		for _, s := range vm.Saved {
			nodes = append(nodes, savedNode(s))
		}
	}
	return nodes
}

// networkNode is one joined network: a submenu titled "name (id)", checked
// when the network works.
func networkNode(n NetworkView) Node {
	id := n.ID.String()
	key := "net/" + id

	children := []Node{
		{
			Key: key + "/id", Label: "Network ID: " + id, Tooltip: "Copy to clipboard",
			Action: Action{Kind: ActCopy, Text: id},
		},
		separator(key + "/sep/flags"),
	}
	for _, f := range ztapi.AllFlags {
		on := n.Flag(f)
		children = append(children, Node{
			Key: key + "/flag/" + string(f), Kind: KindCheckbox, Label: flagLabel(f), Checked: on,
			Action: Action{Kind: ActSetFlag, Network: n.ID, Flag: f, Value: !on},
		})
	}

	children = append(children,
		separator(key+"/sep/info"),
		info(key+"/status", "Status: "+statusText(n.Status)),
		info(key+"/ethernet", "Ethernet: "+orDash(n.MAC)),
		info(key+"/device", "Device: "+orDash(n.PortDeviceName)),
		info(key+"/type", "Type: "+typeText(n.Type)),
		info(key+"/dns-domain", "DNS Domain: "+orDash(n.DNS.Domain)),
	)
	if n.DNSState != "" {
		// What penguintier-dnsd does with the network's DNS settings.
		children = append(children, info(key+"/dns", "DNS: "+n.DNSState.Text()))
	}

	if n.SSOEnabled {
		children = append(children, separator(key+"/sep/sso"), info(key+"/sso", ssoText(n)))
		if n.AuthenticationURL != "" {
			children = append(children, Node{
				Key: key + "/sso-open", Label: "Open SSO Login URL…",
				Action: Action{Kind: ActOpenURL, Text: n.AuthenticationURL},
			})
		}
	}

	if len(n.AssignedAddresses) > 0 {
		children = append(children, separator(key+"/sep/addresses"), info(key+"/addresses", "Addresses"))
		for i, addr := range n.AssignedAddresses {
			// "10.147.17.5/24" is shown, "10.147.17.5" is copied.
			ip, _, _ := strings.Cut(addr, "/")
			children = append(children, Node{
				Key: key + "/address/" + strconv.Itoa(i), Label: addr, Tooltip: "Copy " + ip,
				Action: Action{Kind: ActCopy, Text: ip},
			})
		}
	}

	if len(n.Routes) > 0 {
		children = append(children, separator(key+"/sep/routes"), info(key+"/routes", "Routes"))
		for i, r := range n.Routes {
			label := r.Target
			if r.Via != "" {
				label += " via " + r.Via
			}
			children = append(children, Node{
				Key: key + "/route/" + strconv.Itoa(i), Label: label, Tooltip: "Copy " + r.Target,
				Action: Action{Kind: ActCopy, Text: r.Target},
			})
		}
	}

	children = append(children,
		separator(key+"/sep/disconnect"),
		Node{
			Key: key + "/disconnect", Label: "Disconnect",
			Tooltip: "Leave the network; its settings stay under Saved Networks",
			Action:  Action{Kind: ActDisconnect, Network: n.ID},
		},
	)

	return Node{
		Key:      key,
		Kind:     KindCheckbox,
		Label:    networkTitle(n.Network),
		Checked:  n.Status == ztapi.StatusOK,
		Children: children,
	}
}

// savedNode is one disconnected network, with Reconnect and Forget.
func savedNode(s SavedView) Node {
	key := "saved/" + s.ID.String()
	return Node{
		Key:   key,
		Label: networkTitle(ztapi.Network{ID: s.ID, Name: s.Name}),
		Children: []Node{
			{Key: key + "/reconnect", Label: "Reconnect", Action: Action{Kind: ActReconnect, Network: s.ID}},
			{Key: key + "/forget", Label: "Forget", Action: Action{Kind: ActForget, Network: s.ID}},
		},
	}
}

// separator returns a separator node. Separators need keys too, so the
// controller can compare menu structures.
func separator(key string) Node {
	return Node{Key: key, Kind: KindSeparator}
}

// info returns a read-only row.
func info(key, label string) Node {
	return Node{Key: key, Label: label, Disabled: true}
}

// needsLogin reports whether a network is waiting for an SSO login we can
// open.
func needsLogin(n NetworkView) bool {
	return n.Status == ztapi.StatusAuthenticationRequired && n.AuthenticationURL != ""
}

// maxNameLength is where long network names are cut off, so one long name
// doesn't make the whole menu very wide.
const maxNameLength = 48

// networkTitle is "name (id)", or just the ID while the network has no
// name yet. The name comes from the network's controller (someone else's
// server), so control characters such as newlines are replaced.
func networkTitle(n ztapi.Network) string {
	name := cleanText(n.Name)
	if name == "" {
		return n.ID.String()
	}
	if runes := []rune(name); len(runes) > maxNameLength {
		// A rune is one Unicode character; cutting the []rune (not the
		// string's bytes) never splits a character like "é" in half.
		name = strings.TrimSpace(string(runes[:maxNameLength-1])) + "…"
	}
	return name + " (" + n.ID.String() + ")"
}

// cleanText replaces control characters (newlines, tabs, ...) with spaces
// and trims the result.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// flagLabel is the menu text of one "Allow ..." setting.
func flagLabel(f ztapi.Flag) string {
	switch f {
	case ztapi.FlagAllowManaged:
		return "Allow Managed Addresses"
	case ztapi.FlagAllowGlobal:
		return "Allow Global IPs"
	case ztapi.FlagAllowDefault:
		return "Allow Default Route Override"
	case ztapi.FlagAllowDNS:
		return "Allow DNS Configuration"
	default:
		return string(f)
	}
}

// statusText is a network status in words.
func statusText(s ztapi.Status) string {
	switch s {
	case ztapi.StatusOK:
		return "OK"
	case ztapi.StatusRequestingConfiguration:
		return "Requesting configuration"
	case ztapi.StatusAccessDenied:
		return "Access denied"
	case ztapi.StatusNotFound:
		return "Network not found"
	case ztapi.StatusPortError:
		return "Port error"
	case ztapi.StatusClientTooOld:
		return "ZeroTier is too old for this network"
	case ztapi.StatusAuthenticationRequired:
		return "Authentication required"
	default:
		return "Unknown"
	}
}

// typeText is a network's type in words.
func typeText(t string) string {
	switch t {
	case "PRIVATE":
		return "Private"
	case "PUBLIC":
		return "Public"
	default:
		return orDash(cleanText(t))
	}
}

// ssoText is the SSO status row of a network that uses SSO.
func ssoText(n NetworkView) string {
	switch {
	case !n.SSOExpiry.IsZero():
		// Go formats dates by example: the layout is how the fixed
		// reference time Mon Jan 2 15:04:05 2006 would look.
		return "SSO login expires " + n.SSOExpiry.Format("Mon 2 Jan 15:04")
	case n.Status == ztapi.StatusAuthenticationRequired:
		return "SSO: not logged in"
	default:
		return "SSO: logged in"
	}
}

// orDash returns s, or "—" when s is empty.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
