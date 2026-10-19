package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

const (
	// checkInterval is how often the daemon compares ZeroTier with
	// resolved.
	checkInterval = 5 * time.Second

	// checkTimeout caps one whole check (all API and D-Bus calls in it).
	checkTimeout = 10 * time.Second

	// revertTimeout caps reverting everything when we stop. It must be
	// shorter than the unit's TimeoutStopSec= (15 s), or systemd kills us
	// halfway.
	revertTimeout = 10 * time.Second
)

// daemon holds the daemon's state. Only one goroutine (the one running
// run) ever touches it, so it needs no locks: other goroutines (the D-Bus
// signal watcher, the signal handler) only send it messages on channels.
type daemon struct {
	zt  *ztapi.Client
	dns *resolved.Client
	log *logger

	// ifIndex looks up an interface's index. It is resolved.IfIndex; tests
	// swap in a fake, since they have no real ZeroTier interfaces.
	ifIndex func(name string) (int, error)

	// interval is checkInterval; tests make it longer or shorter.
	interval time.Duration

	// applied is what we set, per interface name, mirrored in the state
	// file at statePath.
	applied   map[string]Config
	statePath string

	// What we have already logged, so a problem that lasts is logged once
	// and not every 5 seconds.
	resolvedMissing bool              // we said "systemd-resolved is not running"
	ztProblem       string            // the last ZeroTier API error we logged
	linkProblems    map[string]string // per link, the last error we logged
	leftAlone       map[string]bool   // links we said we leave alone
}

// newDaemon sets up a daemon with the given applied state.
func newDaemon(zt *ztapi.Client, dns *resolved.Client, log *logger, statePath string, applied map[string]Config) *daemon {
	return &daemon{
		zt:           zt,
		dns:          dns,
		log:          log,
		ifIndex:      resolved.IfIndex,
		interval:     checkInterval,
		applied:      applied,
		statePath:    statePath,
		linkProblems: map[string]string{},
		leftAlone:    map[string]bool{},
	}
}

// errFatal marks errors that should stop the daemon (with a non-zero exit
// code, so systemd restarts it).
var errFatal = errors.New("fatal")

// run checks every interval until ctx is cancelled (SIGTERM), then reverts
// everything we set and returns nil.
//
// It returns an error, without reverting, when the daemon has to stop:
// the ZeroTier service rejected our token (HTTP 401), or the D-Bus
// connection was lost. systemd then restarts us (Restart=on-failure),
// which loads the token again. Not reverting means DNS keeps working
// during the restart, and the state file tells the new process which
// links are ours.
func (d *daemon) run(ctx context.Context) error {
	// Watch for resolved appearing on (or leaving) the bus, so we can act
	// at once instead of at the next tick.
	owner, err := d.dns.WatchOwner(ctx)
	if err != nil {
		return err
	}

	// A Ticker sends the time on its channel C every interval.
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	if err := d.check(ctx); err != nil {
		return err
	}
	for {
		// select waits until one of the channels has something, then runs
		// that case. This loop is the daemon's whole event handling.
		select {
		case <-ctx.Done():
			// SIGTERM or SIGINT (see main.go).
			d.revertAll()
			return nil

		case <-d.dns.Done():
			return fmt.Errorf("%w: lost the connection to the system D-Bus", errFatal)

		case up, ok := <-owner:
			if !ok {
				// The watcher stopped (the connection closed). The
				// Done case above handles that; a nil channel blocks
				// forever, so select simply ignores this case from now on.
				owner = nil
				continue
			}
			if up {
				d.log.Debugf("systemd-resolved appeared on the bus")
			} else {
				d.log.Debugf("systemd-resolved left the bus")
			}
			if err := d.check(ctx); err != nil {
				return err
			}

		case <-ticker.C:
			if err := d.check(ctx); err != nil {
				return err
			}
		}
	}
}

// check is one round of reconciling: read ZeroTier and resolved, Plan,
// carry out the plan. It returns an error only for fatal problems; anything
// else is logged and retried at the next check.
func (d *daemon) check(parent context.Context) error {
	// context.WithTimeout makes a child context that is cancelled after
	// checkTimeout, or when parent is (whichever comes first). Every call
	// below gets it, so one stuck call can't stall the daemon. cancel
	// frees its timer; defer makes sure it runs.
	ctx, cancel := context.WithTimeout(parent, checkTimeout)
	defer cancel()

	if !d.resolvedRunning(ctx) {
		// Stay idle: no ZeroTier calls, no writes. WatchOwner wakes us up
		// when resolved starts.
		return nil
	}

	networks, err := d.zt.Networks(ctx)
	if errors.Is(err, ztapi.ErrUnauthorized) {
		return fmt.Errorf("%w: %w; exiting so systemd restarts us with a fresh token", errFatal, err)
	}
	if err != nil {
		// Service down or restarting: change nothing until it is back.
		d.noteZeroTierProblem(err)
		return nil
	}
	d.noteZeroTierProblem(nil)

	desired := Desired(networks)
	d.plan(ctx, desired)
	return nil
}

// plan reads the current state of every link in desired or applied, then
// plans and executes. revertAll calls it with nothing desired.
func (d *daemon) plan(ctx context.Context, desired map[string]Config) {
	current, unreadable := d.readLinks(ctx, linkNames(desired, d.applied))

	// Leave links we could not read out of this round entirely: Plan
	// would take a missing link for a missing interface.
	applied := d.applied
	if len(unreadable) > 0 {
		desired = without(desired, unreadable)
		applied = without(d.applied, unreadable)
	}

	problems := map[string]string{}
	for link, err := range unreadable {
		problems[link] = err
	}
	d.execute(ctx, Plan(desired, current, applied), problems)
	if errors.Is(ctx.Err(), context.Canceled) {
		// We are stopping: the errors are just "context canceled". Don't
		// log them as link problems. (A timeout is DeadlineExceeded, and
		// is logged.)
		return
	}
	d.noteLinkProblems(problems)
}

// resolvedRunning reports whether resolved is on the bus, and logs when
// that changes.
func (d *daemon) resolvedRunning(ctx context.Context) bool {
	up, err := d.dns.Available(ctx)
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) { // not just "we are stopping"
			d.log.Warnf("%v", err)
		}
		return false
	}
	switch {
	case !up && !d.resolvedMissing:
		d.log.Infof("systemd-resolved is not running; waiting for it (ZeroTier DNS settings are not applied until it starts)")
		d.resolvedMissing = true
	case up && d.resolvedMissing:
		d.log.Infof("systemd-resolved is running")
		d.resolvedMissing = false
	}
	return up
}

// readLinks asks resolved for the current state of each link. Links whose
// interface doesn't exist (or that resolved doesn't know yet) are left out
// of current. Links we could not read at all are in unreadable, with the
// error text.
func (d *daemon) readLinks(ctx context.Context, names []string) (current map[string]resolved.LinkDNS, unreadable map[string]string) {
	current = map[string]resolved.LinkDNS{}
	unreadable = map[string]string{}
	for _, name := range names {
		index, err := d.ifIndex(name)
		if errors.Is(err, resolved.ErrNoInterface) {
			continue
		}
		if err != nil {
			unreadable[name] = err.Error()
			continue
		}
		link, err := d.dns.Link(ctx, index)
		if errors.Is(err, resolved.ErrNoSuchLink) {
			continue
		}
		if err != nil {
			unreadable[name] = err.Error()
			continue
		}
		current[name] = link
	}
	return current, unreadable
}

// execute carries out the ops in order. When an op fails, the remaining
// ops for the same link are skipped; the next check plans again from
// whatever state the link is in. Errors are collected in problems.
func (d *daemon) execute(ctx context.Context, ops []Op, problems map[string]string) {
	leftAlone := map[string]bool{}
	for _, op := range ops {
		if _, failed := problems[op.Link]; failed {
			continue
		}
		if op.Kind == OpLeaveAlone {
			leftAlone[op.Link] = true
			continue
		}
		if err := d.do(ctx, op); err != nil {
			problems[op.Link] = err.Error()
		}
	}

	// Log "leaving alone" once, when it starts.
	for link := range leftAlone {
		if !d.leftAlone[link] {
			d.log.Infof("%s already has DNS settings that penguintier-dnsd did not make; leaving them alone", link)
		}
	}
	d.leftAlone = leftAlone
}

// do carries out one op.
func (d *daemon) do(ctx context.Context, op Op) error {
	switch op.Kind {
	case OpRemember:
		d.applied[op.Link] = op.Config
		return d.save()

	case OpForget:
		d.log.Debugf("%s: forgetting it (reverted, interface gone, or managed by someone else now)", op.Link)
		delete(d.applied, op.Link)
		return d.save()
	}

	// Everything else is a D-Bus call. Look the ifindex up right now:
	// ZeroTier may have just created the interface again under a new
	// number.
	index, err := d.ifIndex(op.Link)
	if err != nil {
		return err
	}
	switch op.Kind {
	case OpSetDefaultRoute:
		err = d.dns.SetLinkDefaultRoute(ctx, index, false)
		if err == nil {
			d.log.Debugf("%s: DefaultRoute set to false", op.Link)
		}
	case OpSetDomains:
		err = d.dns.SetLinkDomains(ctx, index, op.Config.domains())
		if err == nil {
			d.log.Infof("%s (network %s): DNS queries for %s now go to its servers", op.Link, op.Config.Network, op.Config.Domain)
		}
	case OpSetDNS:
		err = d.dns.SetLinkDNS(ctx, index, op.Config.Servers)
		if err == nil {
			d.log.Infof("%s (network %s): DNS servers set to %s", op.Link, op.Config.Network, joinAddrs(op.Config.Servers))
		}
	case OpRevert:
		err = d.dns.RevertLink(ctx, index)
		if err == nil {
			d.log.Infof("%s: DNS settings reverted", op.Link)
		}
	default:
		err = fmt.Errorf("unknown op %q", op.Kind)
	}
	return err
}

// save writes d.applied to the state file.
func (d *daemon) save() error {
	return saveState(d.statePath, d.applied)
}

// revertAll reverts every link we applied, for shutdown. It uses its own
// context, because the one from run is already cancelled by then.
func (d *daemon) revertAll() {
	if len(d.applied) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), revertTimeout)
	defer cancel()

	up, err := d.dns.Available(ctx)
	if err != nil || !up {
		// Nothing to revert right now. Keep the state file: if resolved
		// kept our settings (it saves them under /run across its own
		// restarts), the next run still knows they are ours.
		d.log.Infof("systemd-resolved is not running; nothing to revert")
		return
	}
	d.log.Infof("stopping: reverting the DNS settings of %d link(s)", len(d.applied))
	// Planning with nothing desired gives a revert (or a forget) for every
	// link we applied.
	d.plan(ctx, nil)
}

// noteZeroTierProblem logs a ZeroTier API problem once, and logs when it
// is gone. Pass nil after a successful call.
func (d *daemon) noteZeroTierProblem(err error) {
	switch {
	case err == nil && d.ztProblem != "":
		d.log.Infof("ZeroTier service reachable again")
		d.ztProblem = ""
	case err != nil && err.Error() != d.ztProblem:
		d.log.Warnf("ZeroTier service: %v (DNS settings stay as they are until it answers)", err)
		d.ztProblem = err.Error()
	}
}

// noteLinkProblems logs each link's error once (and again only if it
// changes), and logs when a link's problem is gone.
func (d *daemon) noteLinkProblems(problems map[string]string) {
	for link, msg := range problems {
		if d.linkProblems[link] != msg {
			d.log.Warnf("%s: %s", link, msg)
		}
	}
	for link := range d.linkProblems {
		if _, still := problems[link]; !still {
			d.log.Infof("%s: OK again", link)
		}
	}
	d.linkProblems = problems
}

// without returns a copy of m without the keys in drop.
func without(m map[string]Config, drop map[string]string) map[string]Config {
	out := make(map[string]Config, len(m))
	for k, v := range m {
		if _, skip := drop[k]; !skip {
			out[k] = v
		}
	}
	return out
}

func joinAddrs(list []netip.Addr) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}
