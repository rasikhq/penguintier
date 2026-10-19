// Command zt-mock is a fake ZeroTier One local API for development.
//
//	zt-mock [--scenario NAME] [--listen ADDRESS]
//	zt-mock --list
//
// It serves canned scenarios (online, service-down, unauthorized,
// sso-required, access-denied, many-networks, joining) so the tray can be
// tested without a real ZeroTier service. Join, leave and flag changes are
// kept in memory. Run it with `make mock` or `make mock MOCK_ARGS="..."`.
//
// On start it prints two `export` lines that point Penguintier and ztctl
// at it. While it runs, type another scenario name and press Enter to
// switch; the networks reset to that scenario's starting set.
//
// It is a dev tool and is not shipped in packages. The scenarios themselves
// live in internal/ztapi/ztmock, so tests can use them too.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
	"github.com/rasikhq/penguintier/internal/ztapi/ztmock"
)

// defaultListen is not the real service's port (9993), so zt-mock and
// zerotier-one can run side by side.
const defaultListen = "127.0.0.1:19993"

func main() {
	// signal.NotifyContext gives a context that is cancelled when the
	// process gets Ctrl+C (SIGINT) or SIGTERM, so run can shut down cleanly
	// and delete its token file.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the whole program. It is separate from main so tests can call it
// with their own arguments and output buffers. It returns the exit code.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("zt-mock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scenario := flags.String("scenario", ztmock.Online, "scenario to start with (see --list)")
	listen := flags.String("listen", defaultListen, "address to listen on")
	list := flags.Bool("list", false, "list the scenarios and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0 // -h / --help: the flag package already printed the usage
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "zt-mock: unexpected argument %q\n", flags.Arg(0))
		return 2
	}

	if *list {
		for _, name := range ztmock.Scenarios {
			fmt.Fprintln(stdout, name)
		}
		return 0
	}

	srv, err := ztmock.New(*scenario)
	if err != nil {
		fmt.Fprintln(stderr, "zt-mock:", err)
		return 2
	}

	tokenPath, err := writeTokenFile()
	if err != nil {
		fmt.Fprintln(stderr, "zt-mock:", err)
		return 1
	}
	defer os.Remove(tokenPath) // clean up when run returns

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, "zt-mock:", err)
		return 1
	}
	httpServer := &http.Server{
		Handler: srv,
		// Stops a client that never finishes sending its headers from
		// holding a connection forever.
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Serve blocks, so it runs in its own goroutine. When it stops, its
	// error arrives on serveErr. The channel has room for one value, so the
	// goroutine can always send and finish even if nobody reads it.
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	url := "http://" + listener.Addr().String()
	fmt.Fprintf(stdout, "zt-mock: scenario %q on %s\n\n", *scenario, url)
	fmt.Fprintln(stdout, "Point Penguintier or ztctl at it with:")
	fmt.Fprintf(stdout, "  export %s=%s\n", ztapi.EnvAPIURL, url)
	fmt.Fprintf(stdout, "  export %s=%s\n\n", ztapi.EnvTokenFile, tokenPath)
	fmt.Fprintf(stdout, "Type a scenario name and press Enter to switch (%s).\n", strings.Join(ztmock.Scenarios, ", "))
	fmt.Fprintln(stdout, "Press Ctrl+C to stop.")

	lines := make(chan string)
	go readLines(stdin, lines)

	for {
		// select waits until one of its channels is ready.
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "\nzt-mock: stopping")
			_ = httpServer.Close() // we are exiting anyway
			return 0

		case err := <-serveErr:
			fmt.Fprintln(stderr, "zt-mock:", err)
			return 1

		case line, ok := <-lines:
			if !ok {
				// stdin is closed (e.g. zt-mock runs in the background).
				// Receiving from a nil channel blocks forever, so this
				// select case simply never fires again.
				lines = nil
				continue
			}
			name := strings.TrimSpace(line)
			if name == "" {
				continue
			}
			if err := srv.SetScenario(name); err != nil {
				fmt.Fprintln(stderr, "zt-mock:", err)
				continue
			}
			fmt.Fprintf(stdout, "zt-mock: switched to scenario %q\n", name)
		}
	}
}

// readLines sends each line of r on lines, and closes lines at the end of
// the input.
func readLines(r io.Reader, lines chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines <- scanner.Text()
	}
	close(lines)
}

// writeTokenFile saves the mock's token to a new private temporary file
// and returns its path. os.CreateTemp creates the file with mode 0600,
// which ztapi's token checks require.
func writeTokenFile() (string, error) {
	f, err := os.CreateTemp("", "zt-mock-token-*")
	if err != nil {
		return "", fmt.Errorf("create token file: %w", err)
	}
	if _, err := f.WriteString(ztmock.Token); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write token file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write token file: %w", err)
	}
	return f.Name(), nil
}
