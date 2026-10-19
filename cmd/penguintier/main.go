// Command penguintier is the Penguintier tray app for ZeroTier.
//
// It accepts the planned command line:
//
//	penguintier [--autostart]     start the tray
//	penguintier --version         print the version
//	penguintier doctor [--json]   print a diagnostic report
//	penguintier deauthorize       delete the copied ZeroTier token
//
// The tray runs (phase P3, internal/tray). doctor and deauthorize still
// only print "not implemented yet"; phase P5 owns this file and fills
// them in. See docs/architecture.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rasikhq/penguintier/internal/tray"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args, does the work and returns the process exit code
// (0 = success, 2 = bad command line).
//
// It takes the arguments and output streams as parameters instead of using
// os.Args and os.Stdout directly, so main_test.go can call it like a normal
// function and inspect what it printed.
func run(args []string, stdout, stderr io.Writer) int {
	// A subcommand, if any, is the first word: "penguintier doctor --json".
	if len(args) > 0 {
		switch args[0] {
		case "doctor":
			return runDoctor(args[1:], stdout, stderr)
		case "deauthorize":
			return runDeauthorize(args[1:], stderr)
		}
	}

	// No subcommand: parse the tray's own flags. A FlagSet is a small,
	// self-contained flag parser. ContinueOnError makes Parse return an error
	// instead of exiting the program, which keeps run testable.
	// Go's flag package accepts both -flag and --flag.
	flags := flag.NewFlagSet("penguintier", flag.ContinueOnError)
	flags.SetOutput(stderr)
	autostart := flags.Bool("autostart", false, "started by the desktop autostart file (skips first-run prompts)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage:")
		fmt.Fprintln(stderr, "  penguintier [--autostart]     start the tray")
		fmt.Fprintln(stderr, "  penguintier --version         print the version")
		fmt.Fprintln(stderr, "  penguintier doctor [--json]   print a diagnostic report")
		fmt.Fprintln(stderr, "  penguintier deauthorize       delete the copied ZeroTier token")
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
		fmt.Fprintf(stdout, "penguintier %s (not implemented yet)\n", version)
		return 0
	}
	// Phase P5 uses --autostart to skip first-run prompts. The tray itself
	// starts the same way with or without it.
	_ = autostart
	return startTray(stderr)
}

// startTray runs the tray until the user quits. It is a variable holding a
// function so main_test.go can swap in a stand-in: the real tray blocks
// until Quit.
var startTray = func(stderr io.Writer) int {
	// The context is cancelled on Ctrl+C or SIGTERM, which quits the tray
	// cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := tray.Config{Version: version, Hooks: tray.StubHooks{}}
	if version == "dev" {
		// `make run` (go run): also find the UI that `make ui-build` made.
		cfg.DevUIDir = filepath.Join("ui", "build", "bin")
	}
	if err := tray.Run(ctx, cfg); err != nil {
		fmt.Fprintln(stderr, "penguintier:", err)
		return 1
	}
	return 0
}

// runDoctor handles "penguintier doctor [--json]".
func runDoctor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("penguintier doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the report as JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *asJSON {
		fmt.Fprintln(stdout, `{"error": "penguintier doctor --json is not implemented yet"}`)
		return 0
	}
	fmt.Fprintln(stdout, "penguintier doctor: not implemented yet")
	return 0
}

// runDeauthorize handles "penguintier deauthorize".
func runDeauthorize(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("penguintier deauthorize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	fmt.Fprintln(stderr, "penguintier deauthorize: not implemented yet")
	return 0
}
