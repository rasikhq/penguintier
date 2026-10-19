package resolved

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		want     error  // the sentinel errors.Is must find; nil for none
		wantText string // a part of the message that must survive
	}{
		{
			name:     "not running",
			err:      dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown", Body: []any{"The name is not activatable"}},
			want:     ErrNotRunning,
			wantText: "The name is not activatable",
		},
		{
			name: "name has no owner",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner", Body: []any{"no owner"}},
			want: ErrNotRunning,
		},
		{
			name:     "managed by networkd",
			err:      dbus.Error{Name: "org.freedesktop.resolve1.LinkBusy", Body: []any{"Link zt3jn5bvfq is managed."}},
			want:     ErrLinkManaged,
			wantText: "Link zt3jn5bvfq is managed.",
		},
		{
			name: "pointer form",
			err:  &dbus.Error{Name: "org.freedesktop.resolve1.LinkBusy", Body: []any{"Link zt0 is managed."}},
			want: ErrLinkManaged,
		},
		{
			name: "no such link",
			err:  dbus.Error{Name: "org.freedesktop.resolve1.NoSuchLink", Body: []any{"Link 12 not known"}},
			want: ErrNoSuchLink,
		},
		{
			name: "polkit said no",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []any{"Access denied"}},
			want: ErrAccessDenied,
		},
		{
			name: "polkit wants a password",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired", Body: []any{"Interactive authentication required."}},
			want: ErrAccessDenied,
		},
		{
			name:     "unknown D-Bus error keeps its name",
			err:      dbus.Error{Name: "org.freedesktop.DBus.Error.InvalidArgs", Body: []any{"Invalid search domain"}},
			wantText: "org.freedesktop.DBus.Error.InvalidArgs: Invalid search domain",
		},
		{
			name: "wrapped D-Bus error",
			err:  fmt.Errorf("outer: %w", dbus.Error{Name: "org.freedesktop.resolve1.NoSuchLink", Body: []any{"x"}}),
			want: ErrNoSuchLink,
		},
		{
			name: "timeout stays a timeout",
			err:  context.DeadlineExceeded,
			want: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError("SetLinkDNS link 12", tt.err)
			if !strings.HasPrefix(got.Error(), "SetLinkDNS link 12: ") {
				t.Errorf("message %q does not start with the operation", got)
			}
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Errorf("mapError() = %v, want it to wrap %v", got, tt.want)
			}
			if !strings.Contains(got.Error(), tt.wantText) {
				t.Errorf("mapError() = %q, want it to contain %q", got, tt.wantText)
			}
		})
	}

	if mapError("op", nil) != nil {
		t.Error("mapError(nil) is not nil")
	}
}
