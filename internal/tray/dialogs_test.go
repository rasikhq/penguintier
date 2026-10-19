package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// writeScript creates an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolate makes sure only the test's own fake programs can be found: no
// real penguintier-ui, zenity or kdialog from this machine. The fake
// scripts therefore call other programs by their full path.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvLibexecDir, "")
	old := libexecDir
	libexecDir = t.TempDir()
	// t.Cleanup runs when the test ends, like a deferred call.
	t.Cleanup(func() { libexecDir = old })
}

func TestFindHelper(t *testing.T) {
	isolate(t)
	envDir, libDir, devDir := t.TempDir(), t.TempDir(), t.TempDir()
	libexecDir = libDir

	if _, err := FindHelper("penguintier-ui", devDir); err == nil {
		t.Fatal("found a helper in empty directories")
	}

	writeScript(t, filepath.Join(devDir, "penguintier-ui"), "")
	assertFound(t, filepath.Join(devDir, "penguintier-ui"), devDir)

	writeScript(t, filepath.Join(libDir, "penguintier-ui"), "")
	assertFound(t, filepath.Join(libDir, "penguintier-ui"), devDir)

	// Not executable: skipped.
	if err := os.WriteFile(filepath.Join(envDir, "penguintier-ui"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvLibexecDir, envDir)
	assertFound(t, filepath.Join(libDir, "penguintier-ui"), devDir)

	writeScript(t, filepath.Join(envDir, "penguintier-ui"), "")
	if err := os.Chmod(filepath.Join(envDir, "penguintier-ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	assertFound(t, filepath.Join(envDir, "penguintier-ui"), devDir)
}

func assertFound(t *testing.T, want string, extra ...string) {
	t.Helper()
	got, err := FindHelper("penguintier-ui", extra...)
	if err != nil || got != want {
		t.Errorf("FindHelper = %q, %v; want %q", got, err, want)
	}
}

func TestAskNetworkID(t *testing.T) {
	const id = "c0ffee1234000001"
	tests := []struct {
		name    string
		ui      string // the fake penguintier-ui's script ("" = none)
		zenity  string // the fake zenity's script ("" = none)
		kdialog string
		wantID  ztapi.NetworkID
		wantOK  bool
		wantErr bool
	}{
		{name: "UI prints the ID", ui: "echo " + id, wantID: id, wantOK: true},
		{name: "UI cancelled", ui: "exit 1", zenity: "echo c0ffee1234000002"},
		{name: "UI crashes, zenity works", ui: "exit 2", zenity: "echo " + id, wantID: id, wantOK: true},
		{name: "no UI, zenity works", zenity: "echo '  C0FFEE1234000001  '", wantID: id, wantOK: true},
		{name: "no UI, zenity cancelled", zenity: "exit 1"},
		{name: "no UI, zenity broken, kdialog works", zenity: "exit 255", kdialog: "echo " + id, wantID: id, wantOK: true},
		{name: "no UI, only kdialog", kdialog: "echo " + id, wantID: id, wantOK: true},
		{name: "typed nonsense", zenity: "echo hello", wantErr: true},
		{name: "nothing installed", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			uiDir, binDir := t.TempDir(), os.Getenv("PATH")
			if tc.ui != "" {
				writeScript(t, filepath.Join(uiDir, "penguintier-ui"), tc.ui)
			}
			if tc.zenity != "" {
				writeScript(t, filepath.Join(binDir, "zenity"), tc.zenity)
			}
			if tc.kdialog != "" {
				writeScript(t, filepath.Join(binDir, "kdialog"), tc.kdialog)
			}

			gotID, gotOK, err := uiLauncher{devDir: uiDir}.askNetworkID(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tc.wantErr)
			}
			if gotID != tc.wantID || gotOK != tc.wantOK {
				t.Errorf("got %q, %v; want %q, %v", gotID, gotOK, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestUIEnvironment checks how penguintier-ui is started: with "join",
// and with WEBKIT_DISABLE_DMABUF_RENDERER=1.
func TestUIEnvironment(t *testing.T) {
	isolate(t)
	uiDir := t.TempDir()
	out := filepath.Join(uiDir, "seen.txt")
	writeScript(t, filepath.Join(uiDir, "penguintier-ui"),
		`echo "$1 $WEBKIT_DISABLE_DMABUF_RENDERER" > `+out+`; echo c0ffee1234000001`)

	if _, ok, err := (uiLauncher{devDir: uiDir}).askNetworkID(t.Context()); !ok || err != nil {
		t.Fatalf("askNetworkID: ok %v, err %v", ok, err)
	}
	seen, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(seen)); got != "join 1" {
		t.Errorf("penguintier-ui saw %q, want \"join 1\"", got)
	}
}

func TestStartAbout(t *testing.T) {
	isolate(t)
	if err := (uiLauncher{}).startAbout(t.Context()); err == nil {
		t.Error("startAbout without penguintier-ui = nil, want an error")
	}

	uiDir := t.TempDir()
	writeScript(t, filepath.Join(uiDir, "penguintier-ui"), "exit 0")
	if err := (uiLauncher{devDir: uiDir}).startAbout(t.Context()); err != nil {
		t.Errorf("startAbout = %v", err)
	}
}
