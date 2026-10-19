package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

func TestRunArguments(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
	}{
		{name: "list", args: []string{"--list"}, wantCode: 0, wantStdout: strings.Join(ztmock.Scenarios, "\n") + "\n"},
		{name: "help", args: []string{"--help"}, wantCode: 0},
		{name: "unknown scenario", args: []string{"--scenario", "bogus"}, wantCode: 2},
		{name: "extra argument", args: []string{"online"}, wantCode: 2},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tc.args, strings.NewReader(""), &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tc.wantCode, stderr.String())
			}
			if tc.wantStdout != "" && stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.wantStdout)
			}
		})
	}
}

// TestRunServes starts zt-mock on a free port, talks to it with the real
// client, switches the scenario through stdin, and stops it.
func TestRunServes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// io.Pipe connects a writer to a reader in memory: what the test writes
	// to stdinW, run reads from stdinR, and the same for stdout.
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	var stderr bytes.Buffer // only read after run has returned

	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--listen", "127.0.0.1:0"}, stdinR, stdoutW, &stderr)
		_ = stdoutW.Close()
	}()

	// Read the printed setup lines to learn the URL and token file. A pipe
	// write waits until it is read, so the channel has room for many lines:
	// zt-mock must never wait on the test to read its output.
	lines := make(chan string, 100)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var apiURL, tokenFile string
	for apiURL == "" || tokenFile == "" {
		line := nextLine(t, lines)
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "export "+ztapi.EnvAPIURL+"="); ok {
			apiURL = v
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "export "+ztapi.EnvTokenFile+"="); ok {
			tokenFile = v
		}
	}

	// The token file is private, as ztapi requires.
	token, err := ztapi.ReadTokenFile(tokenFile)
	if err != nil || token != ztmock.Token {
		t.Fatalf("ReadTokenFile(%s) = %q, %v", tokenFile, token, err)
	}

	c := ztapi.New(apiURL, token)
	nets, err := c.Networks(ctx)
	if err != nil || len(nets) != 2 {
		t.Fatalf("Networks = %d, %v; want 2", len(nets), err)
	}

	// Switch the scenario by typing its name.
	if _, err := io.WriteString(stdinW, "many-networks\n"); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, lines, `switched to scenario "many-networks"`)
	nets, err = c.Networks(ctx)
	if err != nil || len(nets) != 12 {
		t.Fatalf("after switching: %d networks, %v; want 12", len(nets), err)
	}

	// A bad name is reported and ignored.
	if _, err := io.WriteString(stdinW, "bogus\n"); err != nil {
		t.Fatal(err)
	}

	// Closing stdin must not stop the server.
	_ = stdinW.Close()
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("Health after closing stdin: %v", err)
	}

	cancel() // like pressing Ctrl+C
	waitForLine(t, lines, "zt-mock: stopping")
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("zt-mock did not stop")
	}
	if !strings.Contains(stderr.String(), `unknown scenario "bogus"`) {
		t.Errorf("stderr = %q, want the unknown scenario reported", stderr.String())
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file %s still exists after exit", tokenFile)
	}
}

// waitForLine skips printed lines until one contains want.
func waitForLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	for {
		if strings.Contains(nextLine(t, lines), want) {
			return
		}
	}
}

// nextLine returns the next line zt-mock printed, or fails after a while.
func nextLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("zt-mock stopped printing")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for zt-mock output")
		return ""
	}
}
