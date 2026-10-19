package tray

import (
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/dnsrule"
	"github.com/rasikhq/penguintier/internal/dnsstatus"
	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// update is a command-line flag for this test binary:
//
//	go test ./internal/tray -update
//
// rewrites the golden files from the current output instead of comparing.
// Review the changes with `git diff` before committing them.
var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// fixedNow is the fake clock of the golden tests, so SSO expiry times in
// the files never change.
var fixedNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// goldenSaved are the saved networks every golden menu starts with. The
// second one is joined in most scenarios, so it must NOT show as saved
// there.
var goldenSaved = map[ztapi.NetworkID]ztapi.SavedNetwork{
	"c0ffee12340000aa": {Name: "old_lab", AllowManaged: true, AllowDNS: true},
	"c0ffee1234000001": {Name: "home-lab", AllowManaged: true},
}

// goldenCase is one golden file: a zt-mock scenario, seen with a token
// (or without one).
type goldenCase struct {
	name     string // the file is testdata/golden/<name>.txt
	scenario string
	token    string
	joining  []ztapi.NetworkID
	// dns is the DNS situation of the machine (see goldenDNS); ""
	// means everything works, so qualifying networks show "applied".
	dns         string
	enablingDNS bool
}

// goldenCases is every zt-mock scenario, plus a client without a token,
// plus the "online" scenario in each DNS situation.
func goldenCases() []goldenCase {
	var cases []goldenCase
	for _, scenario := range ztmock.Scenarios {
		gc := goldenCase{name: scenario, scenario: scenario, token: ztmock.Token}
		if scenario == ztmock.Joining {
			// A join we just sent, not listed by the service yet.
			gc.joining = []ztapi.NetworkID{"c0ffee1234000099"}
		}
		cases = append(cases, gc)
	}
	cases = append(cases, goldenCase{name: "token-missing", scenario: ztmock.Online, token: ""})
	for _, dns := range []string{"no-resolved", "daemon-off", "link-managed", "pending"} {
		cases = append(cases, goldenCase{name: "dns-" + dns, scenario: ztmock.Online, token: ztmock.Token, dns: dns})
	}
	return append(cases, goldenCase{
		name: "dns-enabling", scenario: ztmock.Online, token: ztmock.Token, dns: "no-resolved", enablingDNS: true,
	})
}

// goldenDNS returns the DNS status of each network in one of these
// situations:
//
//	""             resolved and the daemon run; settings are applied
//	"no-resolved"  systemd-resolved is not running
//	"daemon-off"   penguintier-dnsd is not running
//	"link-managed" systemd-networkd manages the interfaces
//	"pending"      everything runs, settings not applied yet
func goldenDNS(networks []ztapi.Network, situation string) map[ztapi.NetworkID]dnsstatus.State {
	states := map[ztapi.NetworkID]dnsstatus.State{}
	for _, n := range networks {
		f := dnsstatus.Facts{ResolvedRunning: true, DaemonRunning: true}
		switch situation {
		case "":
			// The link has exactly what the daemon sets.
			want, _ := dnsrule.Check(n)
			f.Link = resolved.LinkDNS{Servers: want.Servers, Domains: []resolved.Domain{{Name: want.Domain, RoutingOnly: true}}}
		case "no-resolved":
			f.ResolvedRunning = false
		case "daemon-off":
			f.DaemonRunning = false
		case "link-managed":
			f.Managed = true
		}
		states[n.ID] = dnsstatus.Evaluate(n, f)
	}
	return states
}

// goldenViewModel polls a fake service the way the controller does and
// returns the view model.
func goldenViewModel(t *testing.T, gc goldenCase) ViewModel {
	t.Helper()
	srv, err := ztmock.New(gc.scenario)
	if err != nil {
		t.Fatal(err)
	}
	srv.Now = func() time.Time { return fixedNow }
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	status, nets, err := fetch(t.Context(), ztapi.New(ts.URL, gc.token))
	joining := map[ztapi.NetworkID]time.Time{}
	for _, id := range gc.joining {
		joining[id] = fixedNow
	}
	return NewViewModel(Snapshot{
		Status: status, Networks: nets, Err: err,
		Saved: goldenSaved, Joining: joining, Location: time.UTC,
		DNS: goldenDNS(nets, gc.dns), EnablingDNS: gc.enablingDNS,
	})
}

// TestBuildGolden compares the menu of every scenario with its golden
// file in testdata/golden/.
func TestBuildGolden(t *testing.T) {
	for _, gc := range goldenCases() {
		t.Run(gc.name, func(t *testing.T) {
			got := renderMenu(goldenViewModel(t, gc))
			path := filepath.Join("testdata", "golden", gc.name+".txt")

			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run `go test ./internal/tray -update` to create it)", err)
			}
			if got != string(want) {
				t.Errorf("menu differs from %s (run `go test ./internal/tray -update`, then check git diff).\n--- got:\n%s", path, got)
			}
		})
	}
}

// renderMenu writes the icon, tooltip and menu as indented text:
//
//	[x] = checked checkbox, [ ] = unchecked checkbox, ▸ = submenu,
//	(disabled) = greyed out, → = what a click does.
func renderMenu(vm ViewModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "icon: %s\ntooltip: %s\n\n", vm.Icon(), vm.Tooltip())
	writeNodes(&b, Build(vm), 0)
	return b.String()
}

func writeNodes(b *strings.Builder, nodes []Node, depth int) {
	indent := strings.Repeat("    ", depth)
	for _, n := range nodes {
		if n.Kind == KindSeparator {
			b.WriteString(indent + "-----\n")
			continue
		}
		line := indent
		if n.Kind == KindCheckbox {
			if n.Checked {
				line += "[x] "
			} else {
				line += "[ ] "
			}
		}
		line += n.Label
		if len(n.Children) > 0 {
			line += " ▸"
		}
		if n.Disabled {
			line += "  (disabled)"
		}
		if n.Action.Kind != ActNone {
			line += "  → " + n.Action.String()
		}
		b.WriteString(line + "\n")
		writeNodes(b, n.Children, depth+1)
	}
}

// TestBuildRules checks the tray rules on every golden menu: keys are
// unique and not empty, the menu is at most two levels deep, and networks
// are sorted by ID.
func TestBuildRules(t *testing.T) {
	for _, gc := range goldenCases() {
		t.Run(gc.name, func(t *testing.T) {
			nodes := Build(goldenViewModel(t, gc))
			seen := map[string]bool{}
			var walk func(nodes []Node, depth int)
			walk = func(nodes []Node, depth int) {
				for _, n := range nodes {
					if n.Key == "" {
						t.Errorf("entry %q has no key", n.Label)
					}
					if seen[n.Key] {
						t.Errorf("key %q is used twice", n.Key)
					}
					seen[n.Key] = true
					if depth >= 1 && len(n.Children) > 0 {
						t.Errorf("entry %q is a submenu inside a submenu (menu deeper than 2)", n.Label)
					}
					if n.Kind == KindSeparator && (n.Label != "" || len(n.Children) > 0) {
						t.Errorf("separator %q has a label or children", n.Key)
					}
					walk(n.Children, depth+1)
				}
			}
			walk(nodes, 0)

			var ids []string
			for _, n := range nodes {
				if strings.HasPrefix(n.Key, "net/") {
					ids = append(ids, strings.TrimPrefix(n.Key, "net/"))
				}
			}
			for i := 1; i < len(ids); i++ {
				if ids[i-1] >= ids[i] {
					t.Errorf("networks not sorted by ID: %v", ids)
				}
			}
		})
	}
}

func TestNetworkTitle(t *testing.T) {
	tests := []struct {
		name string
		in   ztapi.Network
		want string
	}{
		{name: "name and ID", in: ztapi.Network{ID: "c0ffee1234000001", Name: "home-lab"}, want: "home-lab (c0ffee1234000001)"},
		{name: "no name yet", in: ztapi.Network{ID: "c0ffee1234000001"}, want: "c0ffee1234000001"},
		{name: "control characters", in: ztapi.Network{ID: "c0ffee1234000001", Name: "evil\nname\t"}, want: "evil name (c0ffee1234000001)"},
		{name: "only spaces", in: ztapi.Network{ID: "c0ffee1234000001", Name: " \n "}, want: "c0ffee1234000001"},
		{
			name: "long non-ASCII name is cut at a character",
			in:   ztapi.Network{ID: "c0ffee1234000001", Name: strings.Repeat("é", 60)},
			want: strings.Repeat("é", maxNameLength-1) + "… (c0ffee1234000001)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := networkTitle(tc.in); got != tc.want {
				t.Errorf("networkTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDNSRow: every DNS state gets its row, a network without a state
// gets none, and only "waiting for systemd-resolved" offers "Enable DNS
// support…". (The golden files show the rows in context.)
func TestDNSRow(t *testing.T) {
	states := []dnsstatus.State{
		dnsstatus.Applied, dnsstatus.WaitingForResolved, dnsstatus.DaemonNotRunning, dnsstatus.LinkManaged,
		dnsstatus.SetByOther, dnsstatus.Pending, dnsstatus.Unknown,
		dnsstatus.NotRequested, dnsstatus.NotConnected, dnsstatus.NoDNS, "",
	}
	const id = ztapi.NetworkID("c0ffee1234000001")
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			vm := NewViewModel(Snapshot{
				Networks: []ztapi.Network{{ID: id, Name: "lab", Status: ztapi.StatusOK}},
				DNS:      map[ztapi.NetworkID]dnsstatus.State{id: state},
			})
			var row *Node
			nodes := Build(vm)
			for i := range nodes {
				for j := range nodes[i].Children {
					if nodes[i].Children[j].Key == "net/"+string(id)+"/dns" {
						row = &nodes[i].Children[j]
					}
				}
			}
			switch {
			case state == "" && row != nil:
				t.Errorf("a network without a DNS state has a row %q", row.Label)
			case state != "" && row == nil:
				t.Fatalf("no DNS row for %q", state)
			case state != "" && (row.Label != "DNS: "+state.Text() || !row.Disabled):
				t.Errorf("row = %q (disabled %t), want %q, disabled", row.Label, row.Disabled, "DNS: "+state.Text())
			}

			offered := false
			for _, n := range nodes {
				offered = offered || n.Key == "enable-dns"
			}
			if want := state == dnsstatus.WaitingForResolved; offered != want {
				t.Errorf("Enable DNS support offered = %t, want %t", offered, want)
			}
		})
	}
}
