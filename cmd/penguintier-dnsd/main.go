// Command penguintier-dnsd is the system service that makes ZeroTier's
// "Allow DNS Configuration" work on Linux. ZeroTier itself only logs a
// warning there (ZeroTierOne issue #2492).
//
// For every joined network with allowDNS on, an OK status, a DNS domain
// and at least one DNS server, it tells systemd-resolved over D-Bus:
//
//   - use the network's DNS servers on its ZeroTier interface,
//   - but only for names under its domain (a routing-only "~domain"),
//   - and not as a default route for other names.
//
// It checks every 5 seconds and writes only what changed. It reverts
// everything it set when the network stops qualifying, and when it stops
// (SIGTERM). It never touches a link whose DNS someone else set. It idles
// while systemd-resolved is not running and wakes up when it starts.
//
// It runs as the penguintier-dns system user under systemd
// (packaging/systemd/penguintier-dnsd.service), which hands it the API
// token as a credential. See docs/architecture.md and docs/testing.md.
//
// Exit codes: 0 after SIGTERM, 1 on errors (including HTTP 401 from
// ZeroTier, so Restart=on-failure loads the token again), 2 for wrong
// usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rasikhq/penguintier/internal/resolved"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: penguintier-dnsd [--version]

Applies ZeroTier network DNS settings to systemd-resolved. Normally run by
systemd as penguintier-dnsd.service. To run it by hand for testing:

  PENGUINTIER_TOKEN_FILE=~/.zeroTierOneAuthToken RUNTIME_DIRECTORY=/tmp/dnsd \
    penguintier-dnsd

Environment:
  PENGUINTIER_API_URL     ZeroTier API address (default http://127.0.0.1:<port>)
  PENGUINTIER_TOKEN_FILE  read the API token from this file, not the systemd credential
  PENGUINTIER_LOG         debug, info (default), warn or error
  RUNTIME_DIRECTORY       where the state file goes (default /run/penguintier-dnsd)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program, separate from main so it returns an exit code
// instead of calling os.Exit (which would skip deferred calls).
func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Fprintf(stdout, "penguintier-dnsd %s\n", version)
		return 0
	case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
		fmt.Fprint(stdout, usage)
		return 0
	case len(args) > 0:
		fmt.Fprint(stderr, usage)
		return 2
	}

	log := newLogger(stderr, os.Getenv("PENGUINTIER_LOG"), stderrIsJournal())

	// signal.NotifyContext returns a context that is cancelled when one of
	// the signals arrives. systemd sends SIGTERM to stop us; SIGINT is
	// Ctrl+C when run by hand. Everything the daemon does takes this
	// context, so cancelling it is how "stop" travels through the program.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		log.Errorf("%v", err)
		return 1
	}

	dns, err := resolved.Connect()
	if err != nil {
		log.Errorf("%v", err)
		return 1
	}
	defer dns.Close()

	applied, err := loadState(cfg.statePath)
	if err != nil {
		// Without the state we can't tell our links from others'. Start
		// fresh: links we set before now look like someone else's and are
		// left alone, which is the safe side.
		log.Warnf("%v (starting with an empty state)", err)
		applied = map[string]Config{}
	}

	log.Infof("penguintier-dnsd %s: ZeroTier API at %s, state in %s, %d link(s) applied before",
		version, cfg.apiURL, cfg.statePath, len(applied))

	d := newDaemon(ztapi.New(cfg.apiURL, cfg.token), dns, log, cfg.statePath, applied)
	if err := d.run(ctx); err != nil {
		if !errors.Is(err, errFatal) {
			err = fmt.Errorf("%w: %w", errFatal, err)
		}
		log.Errorf("%v", err)
		return 1
	}
	log.Infof("stopped")
	return 0
}

// stderrIsJournal reports whether stderr is connected to the journal.
// systemd puts "<device>:<inode>" of that connection in $JOURNAL_STREAM.
// Child processes inherit the variable even when their stderr goes
// somewhere else (a terminal, a pipe), so compare it with what stderr
// really is, as sd-daemon(3) recommends.
func stderrIsJournal() bool {
	want := os.Getenv("JOURNAL_STREAM")
	if want == "" {
		return false
	}
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t) // the raw stat data; see ztapi's checkPrivateFile
	if !ok {
		return false
	}
	return want == fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}
