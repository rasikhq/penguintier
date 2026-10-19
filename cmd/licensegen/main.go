// Command licensegen writes THIRD_PARTY_LICENSES, the file that the .deb and
// .rpm packages install into /usr/share/doc/penguintier/.
//
//	licensegen -o bin/THIRD_PARTY_LICENSES -goroot "$(go env GOROOT)" \
//	    -npm ui/frontend  go-root.tsv go-ui.tsv
//
// Why it exists: Penguintier's programs contain other people's code (Go
// libraries, the Go standard library, and the JavaScript in the Join/About
// windows), and their licenses (MIT, BSD, Apache-2.0) require that anyone who
// receives the compiled programs also receives the license text and copyright
// notice. This command collects those texts into one file.
//
// Where the texts come from:
//
//   - Go modules: the arguments are report files written by
//     `go-licenses report --template cmd/licensegen/report.tmpl` (see
//     `make third-party-licenses`). go-licenses lists the packages that are
//     really compiled into a program and finds each one's license file; this
//     command reads those files.
//   - The Go standard library: the LICENSE file in -goroot.
//   - JavaScript: -npm is the frontend folder. Its package.json
//     "dependencies" (and theirs, recursively) are what Vite bundles into the
//     window; each package's LICENSE file is read from node_modules. Dev
//     tools (vite, typescript, ...) are not bundled, so they are not listed.
//
// Nothing is left out silently: a package without a license file, or with an
// "Unknown" license, is an error and the file is not written. It is a dev tool
// and is not shipped in packages.
//
// Exit codes: 0 success, 1 failed, 2 wrong usage.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is the whole program, separate from main so tests can call it. It
// returns the exit code.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("licensegen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: licensegen -o FILE -goroot DIR -npm DIR [go-licenses-report ...]")
		flags.PrintDefaults()
	}
	out := flags.String("o", "", "file to write")
	goroot := flags.String("goroot", "", "the Go installation that built the programs (its LICENSE covers the standard library)")
	npmDir := flags.String("npm", "", "the frontend folder: package.json and node_modules/")
	if err := flags.Parse(args); err != nil {
		return 2 // the flag package already printed the problem
	}
	if *out == "" || *goroot == "" || *npmDir == "" {
		flags.Usage()
		return 2
	}

	// Whatever is left after the flags are the go-licenses report files.
	sections, err := collect(*goroot, *npmDir, flags.Args())
	if err != nil {
		fmt.Fprintln(stderr, "licensegen:", err)
		return 1
	}
	if err := writeFile(*out, sections); err != nil {
		fmt.Fprintln(stderr, "licensegen:", err)
		return 1
	}
	return 0
}

// collect gathers every component, grouped into the sections of the file.
func collect(goroot, npmDir string, goReports []string) ([]section, error) {
	std, err := goStdlib(goroot)
	if err != nil {
		return nil, err
	}

	modules, err := readGoReports(goReports)
	if err != nil {
		return nil, err
	}

	packages, err := npmPackages(npmDir)
	if err != nil {
		return nil, err
	}

	return []section{
		{
			title:      "Go standard library",
			intro:      "Part of every Go program of Penguintier.",
			components: []component{std},
		},
		{
			title:      "Go modules",
			intro:      "The libraries that penguintier, penguintier-dnsd, penguintier-token-helper and penguintier-ui are built from.",
			components: modules,
		},
		{
			title:      "JavaScript packages",
			intro:      "Bundled into the Join and About windows of penguintier-ui.",
			components: packages,
		},
	}, nil
}
