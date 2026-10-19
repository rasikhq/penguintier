// Package dnsstatus explains, for each ZeroTier network, what happens to
// its DNS settings: applied, waiting for systemd-resolved, the DNS daemon
// is not running, and so on. The tray shows it as a "DNS: …" row in each
// network's submenu, and `penguintier doctor` prints it.
//
// It never changes anything, and it needs no privileges: it reads
// systemd-resolved's link properties (org.freedesktop.resolve1), the
// state of penguintier-dnsd.service (org.freedesktop.systemd1) and
// whether systemd-networkd manages the interface
// (org.freedesktop.network1), all on the system bus. penguintier-dnsd's
// own state file is private to the daemon, so it is not read.
//
// The package has two halves:
//
//   - dnsstatus.go: Evaluate, a pure function from a network and the facts
//     about it to a State. Table tests cover every state.
//   - probe.go: Probe, which reads those facts over D-Bus.
package dnsstatus

import (
	"slices"
	"strings"

	"github.com/rasikhq/penguintier/internal/dnsrule"
	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// State is the DNS status of one network. It is a string so it reads well
// in `penguintier doctor --json`; those values are part of its stable
// output, so don't rename them.
type State string

// The states. The first group is for networks that qualify (see
// internal/dnsrule); the second repeats dnsrule's reasons for networks
// that don't.
const (
	// Applied: the interface has the network's DNS servers and its
	// routing-only domain.
	Applied State = "applied"
	// WaitingForResolved: systemd-resolved is not running (or not
	// installed). penguintier-dnsd waits for it.
	WaitingForResolved State = "waiting-for-resolved"
	// DaemonNotRunning: penguintier-dnsd.service is not active.
	DaemonNotRunning State = "daemon-not-running"
	// LinkManaged: systemd-networkd manages the interface, so
	// systemd-resolved refuses DNS settings from anyone else.
	LinkManaged State = "link-managed"
	// SetByOther: the interface has DNS settings from another program.
	// penguintier-dnsd leaves those alone.
	SetByOther State = "set-by-other"
	// Pending: everything is in place, but the settings are not on the
	// interface yet. The daemon checks every 5 seconds.
	Pending State = "pending"
	// Unknown: the facts could not be read (no system bus, for example).
	Unknown State = "unknown"

	// NotRequested: "Allow DNS Configuration" is off.
	NotRequested State = "not-requested"
	// NotConnected: the network is not connected (yet).
	NotConnected State = "not-connected"
	// NoDNS: the network's controller sends no DNS settings.
	NoDNS State = "no-dns"
)

// Text is the state in a few words, for the tray row ("DNS: " + Text) and
// the doctor report.
func (s State) Text() string {
	switch s {
	case Applied:
		return "applied"
	case WaitingForResolved:
		return "waiting for systemd-resolved"
	case DaemonNotRunning:
		return "daemon not running"
	case LinkManaged:
		return "link managed by systemd-networkd"
	case SetByOther:
		return "set by another program"
	case Pending:
		return "applying…"
	case NotRequested:
		return "not requested (Allow DNS is off)"
	case NotConnected:
		return "waiting for the network"
	case NoDNS:
		return "network has no DNS"
	default:
		return "status unknown"
	}
}

// Broken reports whether the state means DNS should work but doesn't, and
// the user can do something about it. `penguintier doctor` exits non-zero
// for these. Waiting for systemd-resolved is not counted: it is a choice
// ("Enable DNS support…" turns it on), not a fault.
func (s State) Broken() bool {
	switch s {
	case DaemonNotRunning, LinkManaged:
		return true
	default:
		return false
	}
}

// Facts are what Evaluate needs to know besides the network itself. Probe
// reads them.
type Facts struct {
	// ResolvedRunning: org.freedesktop.resolve1 has an owner on the
	// system bus.
	ResolvedRunning bool
	// DaemonRunning: penguintier-dnsd.service is active.
	DaemonRunning bool
	// Link is what systemd-resolved reports for the network's interface.
	Link resolved.LinkDNS
	// LinkErr is set when the link could not be read. A missing link (the
	// interface is brand new) wraps resolved.ErrNoSuchLink or
	// resolved.ErrNoInterface.
	LinkErr error
	// Managed: systemd-networkd manages the interface.
	Managed bool
	// Unreadable: the facts could not be read at all.
	Unreadable bool
}

// Evaluate returns the DNS state of network n. It does no I/O.
func Evaluate(n ztapi.Network, f Facts) State {
	want, reason := dnsrule.Check(n)
	switch reason {
	case dnsrule.NotRequested:
		return NotRequested
	case dnsrule.NotConnected:
		return NotConnected
	case dnsrule.NoDNS:
		return NoDNS
	}

	// The network qualifies: penguintier-dnsd should apply it.
	switch {
	case f.Unreadable:
		return Unknown
	case !f.ResolvedRunning:
		return WaitingForResolved
	case f.LinkErr == nil && isApplied(f.Link, want):
		// Checked before the daemon: what counts is what resolved does.
		return Applied
	case f.Managed:
		return LinkManaged
	case !f.DaemonRunning:
		return DaemonNotRunning
	case f.LinkErr == nil && (len(f.Link.Servers) > 0 || len(f.Link.Domains) > 0):
		return SetByOther
	default:
		// The daemon runs and the link is empty, or resolved doesn't know
		// the interface yet: the next check applies it.
		return Pending
	}
}

// isApplied reports whether the link has exactly what penguintier-dnsd
// sets: the network's servers, in order, and its routing-only domain.
// Domain names are compared ignoring case and a trailing dot, because
// resolved may report them a little differently from how they were set.
func isApplied(link resolved.LinkDNS, want dnsrule.Want) bool {
	if !slices.Equal(link.Servers, want.Servers) || len(link.Domains) != 1 {
		return false
	}
	d := link.Domains[0]
	return d.RoutingOnly && strings.ToLower(strings.TrimSuffix(d.Name, ".")) == want.Domain
}
