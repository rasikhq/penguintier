package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/rasikhq/penguintier/internal/doctor"
	"github.com/rasikhq/penguintier/internal/tray"
)

// gatherFacts reads the doctor's facts from the system. A variable so
// main_test.go can give it made-up facts instead.
var gatherFacts = func(ctx context.Context) doctor.Facts {
	return doctor.Gather(ctx, doctor.Options{
		Version: version,
		// A function literal adapts FindHelper (which takes optional extra
		// directories) to the one-argument form the doctor wants.
		FindHelper: func(name string) (string, error) { return tray.FindHelper(name) },
	})
}

// runDoctor handles "penguintier doctor [--json]". It exits 1 when a check
// failed, so scripts can tell.
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
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "penguintier doctor: unexpected argument %q\n", flags.Arg(0))
		return 2
	}

	report := doctor.Evaluate(gatherFacts(context.Background()))
	var err error
	if *asJSON {
		err = report.WriteJSON(stdout)
	} else {
		err = report.WriteText(stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "penguintier doctor:", err)
		return 1
	}
	if report.Failed() {
		return 1
	}
	return 0
}
