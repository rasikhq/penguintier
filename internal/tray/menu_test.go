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
}

// goldenCases is every zt-mock scenario, plus a client without a token.
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
	return append(cases, goldenCase{name: "token-missing", scenario: ztmock.Online, token: ""})
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
