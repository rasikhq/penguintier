// Command penguintier is the Penguintier tray app for ZeroTier.
//
//	penguintier [--autostart] [--foreground]   start the tray
//	penguintier --version                      print the version
//	penguintier doctor [--json]                print a diagnostic report
//	penguintier deauthorize [--yes]            delete the copied ZeroTier token
//
// The files of this command:
//
//   - main.go: the command line.
//   - app.go: starting the tray (single instance, first run, the one-time
//     import, the missing-tray-host notice).
//   - background.go: moving to the background when started from a
//     terminal.
//   - hooks.go: what the tray menu asks of the desktop (tray.Hooks).
//   - authorize.go: "Authorize Penguintier…" (pkexec + the token helper).
//   - enabledns.go: "Enable DNS support…" (penguintier-dns-helper).
//   - state.go: state.json, what the tray remembers between runs.
//   - doctor.go and deauthorize.go: the two subcommands.
//
// See docs/architecture.md for the contracts these follow.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version is the app version. Release builds replace it at link time:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/penguintier
//
// A plain `go build` or `go run` keeps "dev".
var version = "dev"

func main() {
	// os.Exit ends the program at once and skips any deferred calls, so it
	// is only called here, after run has finished all of its work.
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run parses args, does the work and returns the process exit code
// (0 = success, 1 = failure, 2 = bad command line).
//
// It takes the arguments and the standard streams as parameters instead
// of using os.Args, os.Stdin and os.Stdout directly, so main_test.go can
// call it like a normal function and inspect what it printed.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// A subcommand, if any, is the first word: "penguintier doctor --json".
	if len(args) > 0 {
		switch args[0] {
		case "doctor":
			return runDoctor(args[1:], stdout, stderr)
		case "deauthorize":
			return runDeauthorize(args[1:], stdin, stdout, stderr)
		}
	}

	// No subcommand: parse the tray's own flags. A FlagSet is a small,
	// self-contained flag parser. ContinueOnError makes Parse return an error
	// instead of exiting the program, which keeps run testable.
	// Go's flag package accepts both -flag and --flag.
	flags := flag.NewFlagSet("penguintier", flag.ContinueOnError)
	flags.SetOutput(stderr)
	autostart := flags.Bool("autostart", false, "started by the desktop at login (never shows first-run prompts)")
	foreground := flags.Bool("foreground", false, "stay in this terminal, with the log on stderr (from a terminal it otherwise moves to the background)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage:")
		fmt.Fprintln(stderr, "  penguintier [--autostart] [--foreground]   start the tray")
		fmt.Fprintln(stderr, "  penguintier --version                      print the version")
		fmt.Fprintln(stderr, "  penguintier doctor [--json]                print a diagnostic report")
		fmt.Fprintln(stderr, "  penguintier deauthorize [--yes]            delete the copied ZeroTier token")
		fmt.Fprintln(stderr, "\nFlags:")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		// -h / --help is not a mistake, so it exits 0.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "penguintier: unknown command %q\n", flags.Arg(0))
		flags.Usage()
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "penguintier %s\n", version)
		return 0
	}
	if inBackground(*autostart, *foreground) {
		return startInBackground(stdout, stderr)
	}
	return startTray(*autostart, stderr)
}
