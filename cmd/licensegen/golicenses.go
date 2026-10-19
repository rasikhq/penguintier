package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// goStdlib describes the Go standard library, which is compiled into every Go
// program. go-licenses does not list it, so it is added here from the LICENSE
// file of the Go installation that built the programs.
func goStdlib(goroot string) (component, error) {
	// The VERSION file starts with the version ("go1.27.2") and then has a
	// line with the build time.
	data, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		return component{}, fmt.Errorf("read Go version: %w", err)
	}
	version, _, _ := strings.Cut(string(data), "\n")

	texts, err := readTexts(goroot, []string{"LICENSE"})
	if err != nil {
		return component{}, err
	}
	return component{
		name:    "Go standard library",
		version: strings.TrimSpace(version),
		license: "BSD-3-Clause", // what Go's LICENSE file says
		url:     "https://go.dev/LICENSE",
		texts:   texts,
	}, nil
}

// readGoReports reads the files written by
// `go-licenses report --template cmd/licensegen/report.tmpl`. Each line holds
// five fields separated by tabs (see report.tmpl):
//
//	package   version   license name   license URL   license file
//
// The same license file can show up more than once (several packages of one
// module, or the same module in both Go modules of the repo); it is listed
// once.
func readGoReports(paths []string) ([]component, error) {
	var found []component
	seen := map[string]bool{} // license files that are already listed

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read go-licenses report: %w", err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) != 5 {
				return nil, fmt.Errorf("%s:%d: want 5 tab-separated fields, got %d", path, i+1, len(fields))
			}
			name, version, license, url, licenseFile := fields[0], fields[1], fields[2], fields[3], fields[4]

			// go-licenses says "Unknown" when it cannot tell which license
			// a file is. Do not ship a guess: stop and let a person look.
			if license == "Unknown" {
				return nil, fmt.Errorf("%s: go-licenses could not identify the license of %s", path, name)
			}
			// A module that go.mod replaces with a folder of this repo (like
			// the patched systray in third_party/) has no version and no
			// web address. Leave them out rather than print "Unknown".
			if version == "Unknown" {
				version = ""
			}
			if url == "Unknown" {
				url = ""
			}
			if seen[licenseFile] {
				continue
			}
			seen[licenseFile] = true

			texts, err := readGoLicenseTexts(licenseFile)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			found = append(found, component{name: name, version: version, license: license, url: url, texts: texts})
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].name != found[j].name {
			return found[i].name < found[j].name
		}
		return found[i].version < found[j].version
	})
	return found, nil
}

// readGoLicenseTexts returns the license file and any NOTICE file next to it.
// Apache-2.0 asks for the NOTICE of the code you pass on, too.
func readGoLicenseTexts(licenseFile string) ([]licenseText, error) {
	dir := filepath.Dir(licenseFile)
	names := []string{filepath.Base(licenseFile)}

	notices, err := findFiles(dir, "notice")
	if err != nil {
		return nil, err
	}
	names = append(names, notices...)
	return readTexts(dir, names)
}
