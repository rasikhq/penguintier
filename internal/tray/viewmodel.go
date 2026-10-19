package tray

import (
	"errors"
	"sort"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// ServiceState says whether the tray can talk to the ZeroTier service.
type ServiceState int

// The service states. "iota" counts up from 0 inside a const block, so
// ServiceOK is 0, ServiceDown is 1, and so on. Only the names matter; the
// numbers are never shown or saved.
const (
	// ServiceOK: the service answered, so the node and networks are known.
	ServiceOK ServiceState = iota
	// ServiceDown: the service is not running, or not answering.
	ServiceDown
	// ServiceNotInstalled: ZeroTier One is not installed on this machine.
	ServiceNotInstalled
	// ServiceNeedsAuth: there is no usable API token (missing, unsafe,
	// malformed, or rejected by the service).
	ServiceNeedsAuth
	// ServiceError: anything else, such as a reply we could not read.
	ServiceError
)

// Snapshot is the input of NewViewModel: what one poll of the service
// found, plus the tray's own state.
type Snapshot struct {
	Status   ztapi.NodeStatus
	Networks []ztapi.Network
	// Err is the poll's error. When it is not nil, Status and Networks
	// are empty.
	Err error

	// Saved are the networks the user disconnected from.
	Saved map[ztapi.NetworkID]ztapi.SavedNetwork
	// Joining are networks we asked to join, with the time we asked.
	Joining map[ztapi.NetworkID]time.Time

	StartAtLogin bool

	// Location is the time zone SSO expiry times are shown in:
	// time.Local in the app, time.UTC in tests (so they don't depend on
	// the machine's time zone). nil means time.Local.
	Location *time.Location
}

// ViewModel is everything the menu, icon and tooltip show, as plain data.
// Build turns it into menu nodes.
type ViewModel struct {
	Service ServiceState
	// Problem is a short, human explanation for every state except
	// ServiceOK. It is shown as a menu row.
	Problem string

	Address string // this node's ZeroTier address ("My Address")
	Online  bool   // the node reaches the ZeroTier network

	Networks []NetworkView     // joined networks, sorted by ID
	Saved    []SavedView       // disconnected networks, sorted by ID
	Joining  []ztapi.NetworkID // asked to join but not listed yet, sorted

	StartAtLogin bool
}

// NetworkView is one joined network.
//
// It "embeds" ztapi.Network: the field has a type but no name, so all of
// Network's fields and methods can be used directly on a NetworkView
// (n.Name, n.Status, n.AuthExpiry()). It is Go's way of reusing a struct,
// a little like spreading an object in TypeScript.
type NetworkView struct {
	ztapi.Network
	// SSOExpiry is when the SSO login runs out, in Snapshot.Location. It
	// is the zero time when the network has no expiry.
	SSOExpiry time.Time
}

// SavedView is one disconnected network.
type SavedView struct {
	ID ztapi.NetworkID
	ztapi.SavedNetwork
}

// NewViewModel turns a Snapshot into a ViewModel. It does no I/O.
func NewViewModel(s Snapshot) ViewModel {
	vm := ViewModel{StartAtLogin: s.StartAtLogin}
	vm.Service, vm.Problem = classify(s.Err)
	if vm.Service != ServiceOK {
		// Without the service, the node and its networks are unknown, and
		// saved networks can't be reconnected; the menu shows only the
		// problem.
		return vm
	}

	vm.Address = s.Status.Address
	vm.Online = s.Status.Online

	loc := s.Location
	if loc == nil {
		loc = time.Local
	}
	joined := map[ztapi.NetworkID]bool{}
	for _, n := range s.Networks {
		joined[n.ID] = true
		view := NetworkView{Network: n}
		if expiry, ok := n.AuthExpiry(); ok {
			view.SSOExpiry = expiry.In(loc)
		}
		vm.Networks = append(vm.Networks, view)
	}
	// The client already sorts networks by ID; sorting again keeps the
	// menu order stable even if that ever changes.
	sort.Slice(vm.Networks, func(i, j int) bool { return vm.Networks[i].ID < vm.Networks[j].ID })

	// A saved network that is joined again shows up as joined only.
	// Ranging over a map visits the keys in random order, hence the sort.
	for id, saved := range s.Saved {
		if !joined[id] {
			vm.Saved = append(vm.Saved, SavedView{ID: id, SavedNetwork: saved})
		}
	}
	sort.Slice(vm.Saved, func(i, j int) bool { return vm.Saved[i].ID < vm.Saved[j].ID })

	for id := range s.Joining {
		if !joined[id] {
			vm.Joining = append(vm.Joining, id)
		}
	}
	sort.Slice(vm.Joining, func(i, j int) bool { return vm.Joining[i] < vm.Joining[j] })

	return vm
}

// classify maps a poll error to a ServiceState and the text the menu
// shows for it.
func classify(err error) (ServiceState, string) {
	// errors.As needs a variable of the error type to fill in.
	var insecure *ztapi.InsecureTokenFileError
	var invalid *ztapi.InvalidTokenError

	switch {
	case err == nil:
		return ServiceOK, ""
	case errors.Is(err, ztapi.ErrNotInstalled):
		return ServiceNotInstalled, "ZeroTier One is not installed"
	case errors.Is(err, ztapi.ErrServiceDown):
		return ServiceDown, "Waiting for ZeroTier system service…"
	case errors.Is(err, ztapi.ErrTimeout):
		return ServiceDown, "ZeroTier system service is not responding…"
	case errors.Is(err, ztapi.ErrTokenMissing):
		return ServiceNeedsAuth, "Not yet authorized to use ZeroTier"
	case errors.Is(err, ztapi.ErrUnauthorized):
		return ServiceNeedsAuth, "ZeroTier rejected Penguintier's API token"
	case errors.As(err, &insecure):
		return ServiceNeedsAuth, "Unsafe API token file: " + insecure.Path
	case errors.As(err, &invalid):
		return ServiceNeedsAuth, "The API token file is damaged"
	default:
		return ServiceError, "Unexpected reply from ZeroTier (see the log)"
	}
}

// network returns the joined network with this ID.
func (vm ViewModel) network(id ztapi.NetworkID) (NetworkView, bool) {
	for _, n := range vm.Networks {
		if n.ID == id {
			return n, true
		}
	}
	return NetworkView{}, false
}
