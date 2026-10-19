package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// component is one piece of third-party software and its license text.
type component struct {
	name    string // Go package path or npm package name
	version string // may be empty
	license string // SPDX identifier, e.g. "MIT"
	url     string // where to read the license online; may be empty
	texts   []licenseText
}

// licenseText is the content of one file: usually LICENSE, sometimes also
// NOTICE.
type licenseText struct {
	file string // base name, e.g. "LICENSE"
	text string
}

// readTexts reads the named files of dir.
func readTexts(dir string, names []string) ([]licenseText, error) {
	var texts []licenseText
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path) //nolint:gosec // paths come from go-licenses, node_modules and the command line
		if err != nil {
			return nil, fmt.Errorf("read license file: %w", err)
		}
		texts = append(texts, licenseText{file: name, text: tidy(string(data))})
	}
	return texts, nil
}

// findFiles returns the names of the regular files in dir whose names start
// with one of the prefixes, ignoring case ("NOTICE", "notice.md"). The names
// come back sorted, so the output does not depend on the file system.
func findFiles(dir string, prefixes ...string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		for _, prefix := range prefixes {
			if strings.HasPrefix(lower, prefix) {
				names = append(names, entry.Name())
				break
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// tidy makes a license text uniform without changing a word of it: Unix line
// endings, and no blank lines before it or whitespace after it.
func tidy(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimLeft(text, "\n")
	return strings.TrimRight(text, " \t\n")
}
