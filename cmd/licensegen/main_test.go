package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates the files of a map (path relative to root -> content),
// with any folders they need.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// names lists the "name version" label of each component, in order.
func names(components []component) []string {
	var out []string
	for _, c := range components {
		out = append(out, label(c))
	}
	return out
}

func TestReadGoReports(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"mod/a@v1.0.0/LICENSE":     "License of a\r\n\n",
		"mod/a@v1.0.0/NOTICE":      "Notice of a",
		"mod/b@v2.0.0/LICENSE.txt": "License of b",
	})
	licenseA := filepath.Join(root, "mod/a@v1.0.0/LICENSE")
	licenseB := filepath.Join(root, "mod/b@v2.0.0/LICENSE.txt")

	tests := []struct {
		name     string
		reports  []string // contents of one report file each
		want     []string // "name version" of the components, in order
		wantText map[string][]string
		wantErr  string
	}{
		{
			name: "sorted, and one license file is listed once",
			reports: []string{
				"b\tv2.0.0\tMIT\thttps://example.com/b\t" + licenseB + "\n" +
					"a\tv1.0.0\tApache-2.0\thttps://example.com/a\t" + licenseA + "\n",
				// The same module again, as a second report would list it.
				"a/sub\tv1.0.0\tApache-2.0\thttps://example.com/a\t" + licenseA + "\n\n",
			},
			want: []string{"a v1.0.0", "b v2.0.0"},
			wantText: map[string][]string{
				// Line endings fixed, trailing blank lines gone, NOTICE added.
				"a": {"License of a", "Notice of a"},
				"b": {"License of b"},
			},
		},
		{
			name:    "unknown license",
			reports: []string{"a\tv1.0.0\tUnknown\tUnknown\tUnknown\n"},
			wantErr: "could not identify the license of a",
		},
		{
			name:    "wrong number of fields",
			reports: []string{"a\tv1.0.0\tMIT\n"},
			wantErr: "want 5 tab-separated fields, got 3",
		},
		{
			name:    "license file is missing",
			reports: []string{"c\tv1.0.0\tMIT\thttps://example.com/c\t" + filepath.Join(root, "mod/c@v1.0.0/LICENSE") + "\n"},
			wantErr: "c: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			for i, content := range tt.reports {
				path := filepath.Join(t.TempDir(), fmt.Sprintf("report%d.tsv", i))
				writeTree(t, filepath.Dir(path), map[string]string{filepath.Base(path): content})
				paths = append(paths, path)
			}

			got, err := readGoReports(paths)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if g := strings.Join(names(got), ", "); g != strings.Join(tt.want, ", ") {
				t.Fatalf("components = %q, want %q", g, tt.want)
			}
			for _, c := range got {
				var texts []string
				for _, lt := range c.texts {
					texts = append(texts, lt.text)
				}
				if g, w := strings.Join(texts, "|"), strings.Join(tt.wantText[c.name], "|"); g != w {
					t.Errorf("%s texts = %q, want %q", c.name, g, w)
				}
			}
		})
	}
}

// A module that go.mod replaces with a folder of this repo has no version and
// no URL in the go-licenses report.
func TestReadGoReportsLocalModule(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"third_party/systray/LICENSE": "Apache license"})
	report := "fyne.io/systray\tUnknown\tApache-2.0\tUnknown\t" + filepath.Join(dir, "third_party/systray/LICENSE") + "\n"
	writeTree(t, dir, map[string]string{"report.tsv": report})

	got, err := readGoReports([]string{filepath.Join(dir, "report.tsv")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d components, want 1", len(got))
	}
	if c := got[0]; label(c) != "fyne.io/systray" || c.url != "" {
		t.Errorf("label = %q, url = %q; want the name alone and no URL", label(c), c.url)
	}
}

func TestNpmPackages(t *testing.T) {
	// The frontend depends on "a" and "b". "a" depends on "c" (installed in
	// a's own node_modules) and on "b" (installed next to it). "dev" is only
	// a devDependency and has no license file: it must not be looked at.
	tree := func() map[string]string {
		return map[string]string{
			"package.json": `{"dependencies": {"a": "^1", "b": "^1"}, "devDependencies": {"dev": "^1"}}`,

			"node_modules/a/package.json":                `{"name": "a", "version": "1.2.3", "license": "MIT", "dependencies": {"b": "^1", "c": "^1"}}`,
			"node_modules/a/LICENSE":                     "License of a\n",
			"node_modules/a/node_modules/c/package.json": `{"name": "c", "version": "0.1.0", "license": "ISC"}`,
			"node_modules/a/node_modules/c/LICENSE.md":   "License of c\n",

			"node_modules/b/package.json": `{"name": "b", "version": "1.0.0", "license": "MIT"}`,
			"node_modules/b/COPYING":      "License of b\n",
			"node_modules/b/NOTICE.txt":   "Notice of b\n",

			"node_modules/dev/package.json": `{"name": "dev", "version": "9.9.9", "license": "MIT"}`,
		}
	}

	tests := []struct {
		name    string
		change  func(files map[string]string) // edits the tree before the test
		want    []string
		wantErr string
	}{
		{
			name: "dependencies and their dependencies, not devDependencies",
			want: []string{"a 1.2.3", "b 1.0.0", "c 0.1.0"},
		},
		{
			name:    "package without a license file",
			change:  func(f map[string]string) { delete(f, "node_modules/b/COPYING"); delete(f, "node_modules/b/NOTICE.txt") },
			wantErr: "b: no LICENSE file",
		},
		{
			name: "package without a license field",
			change: func(f map[string]string) {
				f["node_modules/b/package.json"] = `{"name": "b", "version": "1.0.0"}`
			},
			wantErr: `b: package.json has no "license" field`,
		},
		{
			name:    "package that is not installed",
			change:  func(f map[string]string) { delete(f, "node_modules/b/package.json") },
			wantErr: "package b is not installed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := tree()
			if tt.change != nil {
				tt.change(files)
			}
			dir := t.TempDir()
			writeTree(t, dir, files)

			got, err := npmPackages(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if g := strings.Join(names(got), ", "); g != strings.Join(tt.want, ", ") {
				t.Fatalf("packages = %q, want %q", g, tt.want)
			}
			// b has a LICENSE-like file and a NOTICE: both are kept.
			if n := len(got[1].texts); n != 2 {
				t.Errorf("b has %d texts, want 2 (COPYING and NOTICE.txt)", n)
			}
		})
	}
}

func TestRender(t *testing.T) {
	sections := []section{
		{
			title: "Go modules",
			intro: "Compiled in.",
			components: []component{
				{name: "example.com/x", version: "v1.0.0", license: "MIT", url: "https://example.com/x/LICENSE",
					texts: []licenseText{{file: "LICENSE", text: "MIT text"}}},
				{name: "example.com/y", license: "Apache-2.0",
					texts: []licenseText{{file: "LICENSE", text: "Apache text"}, {file: "NOTICE", text: "Notice text"}}},
			},
		},
		{title: "Empty section"},
	}

	want := `
Go modules
  example.com/x v1.0.0 (MIT)
  example.com/y (Apache-2.0)

Empty section


================================================================================
Go modules
================================================================================
Compiled in.

--------------------------------------------------------------------------------
example.com/x v1.0.0
License: MIT
https://example.com/x/LICENSE

MIT text

--------------------------------------------------------------------------------
example.com/y
License: Apache-2.0

[LICENSE]

Apache text

[NOTICE]

Notice text


================================================================================
Empty section
================================================================================
`
	got := render(sections)
	if !strings.HasPrefix(got, header) {
		t.Fatalf("output does not start with the header:\n%s", got)
	}
	if body := strings.TrimPrefix(got, header); body != want {
		t.Errorf("body mismatch\n--- got ---\n%s\n--- want ---\n%s", body, want)
	}
}

func TestRun(t *testing.T) {
	// A fake Go installation, a fake frontend and one go-licenses report.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"goroot/VERSION":                      "go1.99.1\ntime 2026-01-01T00:00:00Z\n",
		"goroot/LICENSE":                      "Go license",
		"web/package.json":                    `{"dependencies": {"react": "1"}}`,
		"web/node_modules/react/package.json": `{"name": "react", "version": "1.0.0", "license": "MIT"}`,
		"web/node_modules/react/LICENSE":      "React license",
		"mod/dbus/LICENSE":                    "Dbus license",
	})
	good := "github.com/godbus/dbus/v5\tv5.2.4\tBSD-2-Clause\thttps://example.com\t" + filepath.Join(root, "mod/dbus/LICENSE") + "\n"
	unknown := "github.com/mystery/lib\tv1.0.0\tUnknown\tUnknown\tUnknown\n"
	writeTree(t, root, map[string]string{"good.tsv": good, "unknown.tsv": unknown})

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantFile bool
		wantIn   []string // pieces of the output file
	}{
		{
			name: "writes the file",
			args: []string{"-o", filepath.Join(root, "LICENSES"), "-goroot", filepath.Join(root, "goroot"),
				"-npm", filepath.Join(root, "web"), filepath.Join(root, "good.tsv")},
			wantFile: true,
			wantIn:   []string{"Go standard library go1.99.1", "Go license", "github.com/godbus/dbus/v5 v5.2.4", "Dbus license", "react 1.0.0", "React license"},
		},
		{
			name: "unknown license: nothing is written",
			args: []string{"-o", filepath.Join(root, "UNKNOWN"), "-goroot", filepath.Join(root, "goroot"),
				"-npm", filepath.Join(root, "web"), filepath.Join(root, "unknown.tsv")},
			wantCode: 1,
		},
		{
			name:     "missing flags",
			args:     []string{"-o", filepath.Join(root, "NONE")},
			wantCode: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(tt.args, &stderr); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}

			out := tt.args[1]
			data, err := os.ReadFile(out)
			if !tt.wantFile {
				if err == nil {
					t.Fatalf("%s was written", out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, piece := range tt.wantIn {
				if !strings.Contains(string(data), piece) {
					t.Errorf("output lacks %q", piece)
				}
			}
		})
	}
}
