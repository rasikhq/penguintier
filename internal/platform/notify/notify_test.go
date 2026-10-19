package notify

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// startBus runs a private dbus-daemon for one test and returns its address.
// The test is skipped when dbus-daemon is not installed.
func startBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}

	// A socket path may be at most 108 bytes, and t.TempDir() can be
	// longer than that (it includes the test name and $TMPDIR), so the
	// socket goes in a short directory under /tmp.
	dir, err := os.MkdirTemp("/tmp", "pt-bus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// The policy allows everything: sending, receiving replies and
	// signals, and owning any name.
	config := `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=` + filepath.Join(dir, "bus") + `</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow receive_sender="*"/>
    <allow own="*"/>
  </policy>
</busconfig>
`
	configPath := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), daemon, "--config-file="+configPath, "--nofork", "--print-address")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The daemon prints its address once it is ready.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read dbus-daemon address: %v", err)
	}
	return strings.TrimSpace(line)
}

// connect opens a connection to the test bus.
func connect(t *testing.T, address string) *dbus.Conn {
	t.Helper()
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// fakeService is a pretend notification service. godbus calls its
// exported methods when a D-Bus call arrives.
type fakeService struct {
	caps []string

	mu       sync.Mutex
	received []fakeNotification
}

type fakeNotification struct {
	appName, summary, body string
	actions                []string
	hints                  map[string]dbus.Variant
}

func (f *fakeService) GetCapabilities() ([]string, *dbus.Error) {
	return f.caps, nil
}

func (f *fakeService) Notify(appName string, _ uint32, _ string, summary, body string,
	actions []string, hints map[string]dbus.Variant, _ int32,
) (uint32, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, fakeNotification{appName, summary, body, actions, hints})
	return uint32(len(f.received)), nil // IDs 1, 2, 3, ...
}

func (f *fakeService) last(t *testing.T) fakeNotification {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) == 0 {
		t.Fatal("the service received no notification")
	}
	return f.received[len(f.received)-1]
}

// startService exports a fakeService on the bus under the real service name.
func startService(t *testing.T, address string, caps []string) (*fakeService, *dbus.Conn) {
	t.Helper()
	conn := connect(t, address)
	svc := &fakeService{caps: caps}
	if err := conn.Export(svc, objectPath, iface); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: reply %v, err %v", busName, reply, err)
	}
	return svc, conn
}

func TestNotify(t *testing.T) {
	tests := []struct {
		name     string
		caps     []string
		msg      Notification
		wantBody string
		// wantActions is the flat key, label list the service gets. A
		// default action is always added, so a click never starts a second
		// Penguintier on GNOME.
		wantActions []string
	}{
		{
			name:        "plain text service",
			caps:        []string{"body", "actions"},
			msg:         Notification{Summary: "Copied", Body: "a1b2c3d4e5 & <more>", Transient: true},
			wantBody:    "a1b2c3d4e5 & <more>",
			wantActions: []string{"default", "Dismiss"},
		},
		{
			name:        "markup service gets escaped text",
			caps:        []string{"body", "body-markup", "actions"},
			msg:         Notification{Summary: "Login needed", Body: "R&D <lab>", Actions: []Action{{Key: "open", Label: "Open login"}}},
			wantBody:    "R&amp;D &lt;lab&gt;",
			wantActions: []string{"open", "Open login", "default", "Dismiss"},
		},
		{
			name: "own default action is kept",
			caps: []string{"body", "actions"},
			msg: Notification{Summary: "Login needed", Body: "lab", Actions: []Action{
				{Key: DefaultAction, Label: "Open login"}, {Key: "open-login", Label: "Open login"},
			}},
			wantBody:    "lab",
			wantActions: []string{"default", "Open login", "open-login", "Open login"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			address := startBus(t)
			svc, _ := startService(t, address, tc.caps)

			n, err := newNotifier(connect(t, address))
			if err != nil {
				t.Fatal(err)
			}
			id, err := n.Notify(t.Context(), tc.msg)
			if err != nil {
				t.Fatal(err)
			}
			if id != 1 {
				t.Errorf("Notify returned ID %d, want 1", id)
			}

			got := svc.last(t)
			if got.appName != appName || got.summary != tc.msg.Summary || got.body != tc.wantBody {
				t.Errorf("service got app %q, summary %q, body %q; want %q, %q, %q",
					got.appName, got.summary, got.body, appName, tc.msg.Summary, tc.wantBody)
			}
			if strings.Join(got.actions, "|") != strings.Join(tc.wantActions, "|") {
				t.Errorf("actions = %q, want %q", got.actions, tc.wantActions)
			}
			_, transient := got.hints["transient"]
			if transient != tc.msg.Transient {
				t.Errorf("transient hint present = %v, want %v", transient, tc.msg.Transient)
			}
			if v := got.hints["desktop-entry"].Value(); v != appID {
				t.Errorf("desktop-entry hint = %v, want %q", v, appID)
			}
		})
	}
}

func TestActions(t *testing.T) {
	address := startBus(t)
	_, serviceConn := startService(t, address, []string{"actions"})

	n, err := newNotifier(connect(t, address))
	if err != nil {
		t.Fatal(err)
	}

	// A signal from some other program must be ignored.
	other := connect(t, address)
	if err := other.Emit(objectPath, iface+".ActionInvoked", uint32(7), "fake"); err != nil {
		t.Fatal(err)
	}
	// The service's own signal must arrive.
	if err := serviceConn.Emit(objectPath, iface+".ActionInvoked", uint32(7), "open"); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-n.Actions():
		if ev != (ActionEvent{ID: 7, Key: "open"}) {
			t.Errorf("got %+v, want ID 7, key open", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no action event arrived")
	}

	// Close ends the forwarding goroutine, which closes Actions.
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-n.Actions():
		if ok {
			t.Error("got another event after Close, want the channel closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Actions was not closed after Close")
	}
}

func TestNotifyWithoutService(t *testing.T) {
	address := startBus(t)
	n, err := newNotifier(connect(t, address))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Notify(t.Context(), Notification{Summary: "x"}); err == nil {
		t.Fatal("Notify with no service running = nil, want an error")
	}
}
