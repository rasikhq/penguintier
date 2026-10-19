package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// packageJSON holds the fields of a package.json that we need.
type packageJSON struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	License      string            `json:"license"`
	Dependencies map[string]string `json:"dependencies"`
}

func readPackageJSON(dir string) (packageJSON, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return packageJSON{}, fmt.Errorf("read package.json: %w", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, fmt.Errorf("parse %s/package.json: %w", dir, err)
	}
	return pkg, nil
}

// npmPackages lists the JavaScript packages that Vite bundles into the
// windows: the "dependencies" of the frontend's package.json, the
// "dependencies" of those, and so on. "devDependencies" (vite, typescript,
// ...) only build the bundle and are not part of it.
//
// It needs `bun install` to have run: it reads the packages from node_modules.
func npmPackages(frontendDir string) ([]component, error) {
	root, err := readPackageJSON(frontendDir)
	if err != nil {
		return nil, err
	}

	// A package to look up, and the folder it is required from.
	type lookup struct{ name, from string }
	var queue []lookup
	for _, name := range sortedKeys(root.Dependencies) {
		queue = append(queue, lookup{name: name, from: frontendDir})
	}

	var found []component
	seen := map[string]bool{} // package folders that are already listed
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]

		dir, err := findPackageDir(next.from, next.name)
		if err != nil {
			return nil, err
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true

		pkg, err := readPackageJSON(dir)
		if err != nil {
			return nil, err
		}
		if pkg.License == "" {
			return nil, fmt.Errorf("%s: package.json has no \"license\" field", next.name)
		}
		names, err := findFiles(dir, "license", "licence", "copying", "notice")
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("%s: no LICENSE file in %s", next.name, dir)
		}
		texts, err := readTexts(dir, names)
		if err != nil {
			return nil, err
		}

		found = append(found, component{
			name:    pkg.Name,
			version: pkg.Version,
			license: pkg.License,
			url:     fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", pkg.Name, pkg.Version),
			texts:   texts,
		})
		for _, dep := range sortedKeys(pkg.Dependencies) {
			queue = append(queue, lookup{name: dep, from: dir})
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	return found, nil
}

// findPackageDir finds the folder of an installed package the way Node does:
// look in node_modules next to the requiring package, then in the
// node_modules of each parent folder.
func findPackageDir(from, name string) (string, error) {
	for dir := from; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "node_modules", name)
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate, nil
		}
		if filepath.Dir(dir) == dir { // reached the root of the file system
			break
		}
	}
	return "", fmt.Errorf("package %s is not installed (run \"bun install --frozen-lockfile\" in the frontend folder)", name)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
