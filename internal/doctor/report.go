package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rasikhq/penguintier/internal/dnsstatus"
)

// SchemaVersion is the version of the JSON layout. It goes up only if a
// field is removed or changes meaning; new fields keep it.
const SchemaVersion = 1

// Status is the verdict of one check.
type Status string

// The verdicts. Only Fail makes `penguintier doctor` exit non-zero.
const (
	OK   Status = "ok"   // works
	Info Status = "info" // a fact, no verdict (such as the version)
	Warn Status = "warn" // works, but something is missing or degraded
	Fail Status = "fail" // broken: Penguintier can't do its job
	Skip Status = "skip" // not checked, because an earlier check failed
)

// Check is one line of the report.
type Check struct {
	// ID names the check in the JSON output, such as "zerotier.token".
	// IDs never change.
	ID string `json:"id"`
	// Title is the check's short name in the text output.
	Title  string `json:"title"`
	Status Status `json:"status"`
	// Summary is one line on what was found.
	Summary string `json:"summary"`
	// Fix says what to do about a warning or failure. It may have several
	// lines; commands sit on lines of their own.
	Fix string `json:"fix,omitempty"`
	// Details are the facts behind the verdict, for scripts.
	Details map[string]any `json:"details,omitempty"`
}

// Network is one joined network, with its DNS status.
type Network struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"` // ZeroTier's status, e.g. "OK"
	Device    string          `json:"device"`
	AllowDNS  bool            `json:"allowDNS"`
	DNSDomain string          `json:"dnsDomain"`
	DNS       dnsstatus.State `json:"dns"`
	DNSText   string          `json:"dnsText"`
}

// Report is the whole result.
type Report struct {
	SchemaVersion int       `json:"schemaVersion"`
	OK            bool      `json:"ok"` // no check failed
	Checks        []Check   `json:"checks"`
	Networks      []Network `json:"networks"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// WriteJSON writes the report as indented JSON.
func (r Report) WriteJSON(w io.Writer) error {
	if r.Networks == nil {
		r.Networks = []Network{} // [] rather than null, for scripts
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// SetEscapeHTML(false) keeps "<" and "&" in commands readable instead
	// of turning them into < and &.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// WriteText writes the report for people:
//
//	ok    ZeroTier service  1.16.2, running at http://127.0.0.1:9993
//	FAIL  API token         not found
//	                        To authorize by hand, run: ...
func (r Report) WriteText(w io.Writer) error {
	// A strings.Builder collects the text; one Write at the end means one
	// error check.
	var b strings.Builder
	b.WriteString("Penguintier doctor\n\n")

	width := 0
	for _, c := range r.Checks {
		width = max(width, len(c.Title))
	}
	indent := strings.Repeat(" ", 2+4+2+width+2)
	for _, c := range r.Checks {
		// %-*s pads to a width given as an argument.
		fmt.Fprintf(&b, "  %-4s  %-*s  %s\n", statusLabel(c.Status), width, c.Title, c.Summary)
		if c.Fix != "" {
			for _, line := range strings.Split(c.Fix, "\n") {
				b.WriteString(indent + line + "\n")
			}
		}
	}

	if len(r.Networks) > 0 {
		b.WriteString("\nNetworks\n")
		// A tabwriter lines up columns: cells end with a tab, and it pads
		// each column to its widest cell (plus 2 spaces) when flushed.
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, n := range r.Networks {
			title := n.ID
			if n.Name != "" {
				title = n.Name + " (" + n.ID + ")"
			}
			device := n.Device
			if device == "" {
				device = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\tDNS: %s\n", title, n.Status, device, n.DNSText)
		}
		_ = tw.Flush() // writing to a strings.Builder never fails
	}

	problems := 0
	for _, c := range r.Checks {
		if c.Status == Fail {
			problems++
		}
	}
	switch problems {
	case 0:
		b.WriteString("\nNo problems found.\n")
	case 1:
		b.WriteString("\n1 problem found.\n")
	default:
		fmt.Fprintf(&b, "\n%d problems found.\n", problems)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// statusLabel is how a status shows in the text output. Failures are in
// capitals so they stand out.
func statusLabel(s Status) string {
	if s == Fail {
		return "FAIL"
	}
	return string(s)
}
