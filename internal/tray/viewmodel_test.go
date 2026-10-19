package tray

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ServiceState
	}{
		{name: "no error", err: nil, want: ServiceOK},
		{name: "not installed", err: fmt.Errorf("GET /status: %w", ztapi.ErrNotInstalled), want: ServiceNotInstalled},
		{name: "service down", err: fmt.Errorf("GET /status: %w", ztapi.ErrServiceDown), want: ServiceDown},
		{name: "timeout", err: fmt.Errorf("GET /status: %w", ztapi.ErrTimeout), want: ServiceDown},
		{name: "token missing", err: ztapi.ErrTokenMissing, want: ServiceNeedsAuth},
		{name: "token rejected", err: fmt.Errorf("GET /status: %w", ztapi.ErrUnauthorized), want: ServiceNeedsAuth},
		{name: "unsafe token file", err: &ztapi.InsecureTokenFileError{Path: "/home/u/.zeroTierOneAuthToken"}, want: ServiceNeedsAuth},
		{name: "damaged token file", err: &ztapi.InvalidTokenError{Path: "/x"}, want: ServiceNeedsAuth},
		{name: "server error", err: &ztapi.HTTPError{Method: "GET", Path: "/status", StatusCode: 500}, want: ServiceError},
		{name: "anything else", err: errors.New("boom"), want: ServiceError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := classify(tc.err)
			if got != tc.want {
				t.Errorf("classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
			if (problem == "") != (tc.want == ServiceOK) {
				t.Errorf("classify(%v) problem = %q", tc.err, problem)
			}
		})
	}
}

func TestNewViewModel(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	vm := NewViewModel(Snapshot{
		Status: ztapi.NodeStatus{Address: "a1b2c3d4e5", Online: true},
		Networks: []ztapi.Network{
			{ID: "c0ffee1234000002", Name: "b"},
			{ID: "c0ffee1234000001", Name: "a", SSOEnabled: true, AuthenticationExpiryTime: now.UnixMilli()},
		},
		Saved: map[ztapi.NetworkID]ztapi.SavedNetwork{
			"c0ffee12340000bb": {Name: "saved-b"},
			"c0ffee12340000aa": {Name: "saved-a"},
			"c0ffee1234000002": {Name: "joined again"},
		},
		Joining: map[ztapi.NetworkID]time.Time{
			"c0ffee1234000001": now, // listed already
			"c0ffee12340000ff": now,
		},
		Location: time.FixedZone("UTC+5", 5*60*60),
	})

	if vm.Service != ServiceOK || vm.Address != "a1b2c3d4e5" || !vm.Online {
		t.Errorf("service %v, address %q, online %v", vm.Service, vm.Address, vm.Online)
	}
	if len(vm.Networks) != 2 || vm.Networks[0].ID != "c0ffee1234000001" {
		t.Errorf("networks not sorted by ID: %+v", vm.Networks)
	}
	if got := vm.Networks[0].SSOExpiry.Format("15:04 MST"); got != "17:00 UTC+5" {
		t.Errorf("SSO expiry = %s, want 17:00 in the given zone", got)
	}
	if len(vm.Saved) != 2 || vm.Saved[0].ID != "c0ffee12340000aa" || vm.Saved[1].ID != "c0ffee12340000bb" {
		t.Errorf("saved = %+v, want aa then bb, without the joined one", vm.Saved)
	}
	if len(vm.Joining) != 1 || vm.Joining[0] != "c0ffee12340000ff" {
		t.Errorf("joining = %v, want only the unlisted network", vm.Joining)
	}
}

// TestNewViewModelServiceDown: without the service, only the problem is
// shown; saved networks can't be reconnected then.
func TestNewViewModelServiceDown(t *testing.T) {
	vm := NewViewModel(Snapshot{
		Err:   ztapi.ErrServiceDown,
		Saved: map[ztapi.NetworkID]ztapi.SavedNetwork{"c0ffee12340000aa": {}},
	})
	if vm.Service != ServiceDown || len(vm.Saved) != 0 || len(vm.Networks) != 0 {
		t.Errorf("got %+v", vm)
	}
}

func TestIcon(t *testing.T) {
	ok := NetworkView{Network: ztapi.Network{Status: ztapi.StatusOK}}
	joining := NetworkView{Network: ztapi.Network{Status: ztapi.StatusRequestingConfiguration}}
	unknown := NetworkView{Network: ztapi.Network{Status: ztapi.StatusUnknown}}
	tests := []struct {
		name string
		vm   ViewModel
		want IconState
	}{
		{name: "all fine", vm: ViewModel{Online: true, Networks: []NetworkView{ok, joining, unknown}}, want: IconOnline},
		{name: "no networks", vm: ViewModel{Online: true}, want: IconOnline},
		{name: "node offline", vm: ViewModel{Online: false, Networks: []NetworkView{ok}}, want: IconOffline},
		{name: "service down", vm: ViewModel{Service: ServiceDown}, want: IconOffline},
		{name: "not installed", vm: ViewModel{Service: ServiceNotInstalled}, want: IconOffline},
		{name: "no token", vm: ViewModel{Service: ServiceNeedsAuth}, want: IconAttention},
		{name: "odd reply", vm: ViewModel{Service: ServiceError}, want: IconAttention},
	}
	for _, s := range []ztapi.Status{
		ztapi.StatusAccessDenied, ztapi.StatusAuthenticationRequired, ztapi.StatusNotFound,
		ztapi.StatusPortError, ztapi.StatusClientTooOld,
	} {
		bad := NetworkView{Network: ztapi.Network{Status: s}}
		tests = append(tests, struct {
			name string
			vm   ViewModel
			want IconState
		}{name: string(s), vm: ViewModel{Online: true, Networks: []NetworkView{ok, bad}}, want: IconAttention})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.vm.Icon(); got != tc.want {
				t.Errorf("Icon() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestLoadIcons checks that all three embedded icons are real PNGs.
func TestLoadIcons(t *testing.T) {
	icons, err := loadIcons()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range allIconStates {
		img, err := png.Decode(bytes.NewReader(icons[state]))
		if err != nil {
			t.Errorf("%s icon: %v", state, err)
			continue
		}
		if size := img.Bounds().Size(); size.X < 16 || size.X != size.Y {
			t.Errorf("%s icon is %v, want a square of at least 16 px", state, size)
		}
	}
}
