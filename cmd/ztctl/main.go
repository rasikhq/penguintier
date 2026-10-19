// Command ztctl is a small developer CLI for the internal/ztapi client:
//
//	ztctl status                                   this node: address, version, online
//	ztctl list                                     joined networks
//	ztctl join <network-id>                        join a network
//	ztctl leave <network-id>                       leave a network
//	ztctl set <network-id> <flag>=<bool> [...]     change allowManaged, allowGlobal,
//	                                               allowDefault or allowDNS
//
// It finds the API and the token exactly like the tray does, so
// PENGUINTIER_API_URL and PENGUINTIER_TOKEN_FILE work too (zt-mock prints
// both). It is a dev tool and is not shipped in packages.
//
// Exit codes: 0 success, 1 the call failed, 2 wrong usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

const usage = `usage:
  ztctl status
  ztctl list
  ztctl join <network-id>
  ztctl leave <network-id>
  ztctl set <network-id> <flag>=<true|false> [<flag>=<true|false> ...]

flags: allowManaged, allowGlobal, allowDefault, allowDNS
`

// copyTokenCommand copies the API token into the file ztctl (and
// zerotier-cli) reads. It needs `make dev-install-helper` first. umask 077
// makes the shell create the file readable by you only.
const copyTokenCommand = "(umask 077 && pkexec /usr/libexec/penguintier/penguintier-token-helper > ~/.zeroTierOneAuthToken)" //nolint:gosec // a shell command, not a credential

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program, separate from main so tests can call it. It
// returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}

	// Check the arguments before talking to the service, so a typo gets a
	// usage message even when the service is down.
	cmd, rest := args[0], args[1:]
	var id ztapi.NetworkID
	var flags map[ztapi.Flag]bool
	var err error
	switch cmd {
	case "status", "list":
		if len(rest) != 0 {
			return usageError(stderr, "%s takes no arguments", cmd)
		}
	case "join", "leave":
		if len(rest) != 1 {
			return usageError(stderr, "%s takes one network ID", cmd)
		}
		if id, err = ztapi.ParseNetworkID(rest[0]); err != nil {
			return usageError(stderr, "%v", err)
		}
	case "set":
		if len(rest) < 2 {
			return usageError(stderr, "set takes a network ID and at least one flag=value")
		}
		if id, err = ztapi.ParseNetworkID(rest[0]); err != nil {
			return usageError(stderr, "%v", err)
		}
		if flags, err = parseFlags(rest[1:]); err != nil {
			return usageError(stderr, "%v", err)
		}
	default:
		return usageError(stderr, "unknown command %q", cmd)
	}

	client, err := ztapi.Discover()
	if err != nil {
		return fail(stderr, err)
	}
	ctx := context.Background()

	switch cmd {
	case "status":
		err = printStatus(ctx, client, stdout)
	case "list":
		err = printList(ctx, client, stdout)
	case "join":
		var n ztapi.Network
		n, err = client.Join(ctx, id)
		if err == nil {
			fmt.Fprintf(stdout, "joined %s (status %s)\n", n.ID, n.Status)
		}
	case "leave":
		err = client.Leave(ctx, id)
		if err == nil {
			fmt.Fprintf(stdout, "left %s\n", id)
		}
	case "set":
		var n ztapi.Network
		n, err = client.SetFlags(ctx, id, flags)
		if err == nil {
			fmt.Fprintf(stdout, "%s: %s\n", n.ID, flagSummary(n))
		}
	}
	if err != nil {
		return fail(stderr, err)
	}
	return 0
}

// parseFlags turns ["allowDNS=true", "allowGlobal=0"] into a map.
func parseFlags(args []string) (map[ztapi.Flag]bool, error) {
	flags := make(map[ztapi.Flag]bool, len(args))
	for _, arg := range args {
		// strings.Cut splits "allowDNS=true" at the first "=". found is
		// false if there is no "=".
		name, value, found := strings.Cut(arg, "=")
		if !found {
			return nil, fmt.Errorf("%q: want <flag>=<true|false>", arg)
		}
		flag, err := ztapi.ParseFlag(name)
		if err != nil {
			return nil, err
		}
		// ParseBool accepts true/false, 1/0, t/f and their capitalized forms.
		on, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %q is not true or false", arg, value)
		}
		flags[flag] = on
	}
	return flags, nil
}

func printStatus(ctx context.Context, client *ztapi.Client, w io.Writer) error {
	status, err := client.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "address:       %s\n", status.Address)
	fmt.Fprintf(w, "online:        %t\n", status.Online)
	fmt.Fprintf(w, "version:       %s\n", status.Version)
	fmt.Fprintf(w, "tcp fallback:  %t\n", status.TCPFallbackActive)
	fmt.Fprintf(w, "api:           %s\n", client.BaseURL())
	return nil
}

func printList(ctx context.Context, client *ztapi.Client, w io.Writer) error {
	nets, err := client.Networks(ctx)
	if err != nil {
		return err
	}
	if len(nets) == 0 {
		fmt.Fprintln(w, "(no networks joined)")
		return nil
	}

	// tabwriter lines up tab-separated columns. Flush writes the result.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tTYPE\tDEVICE\tADDRESSES\tFLAGS")
	for _, n := range nets {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			n.ID, orDash(n.Name), n.Status, orDash(n.Type), orDash(n.PortDeviceName),
			orDash(strings.Join(n.AssignedAddresses, ",")), flagSummary(n))
	}
	return tw.Flush()
}

// flagSummary lists a network's four settings, e.g.
// "allowManaged=true allowGlobal=false allowDefault=false allowDNS=true".
func flagSummary(n ztapi.Network) string {
	parts := make([]string, 0, len(ztapi.AllFlags))
	for _, f := range ztapi.AllFlags {
		parts = append(parts, fmt.Sprintf("%s=%t", f, n.Flag(f)))
	}
	return strings.Join(parts, " ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// usageError prints a usage problem and returns exit code 2.
func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "ztctl: "+format+"\n\n", args...)
	fmt.Fprint(stderr, usage)
	return 2
}

// fail prints an error, plus a hint for the common ones, and returns exit
// code 1.
func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "ztctl:", err)
	var insecure *ztapi.InsecureTokenFileError
	switch {
	case errors.Is(err, ztapi.ErrTokenMissing):
		fmt.Fprintln(stderr, "hint: copy the token once with:")
		fmt.Fprintln(stderr, "  make dev-install-helper")
		fmt.Fprintln(stderr, "  "+copyTokenCommand)
	case errors.Is(err, ztapi.ErrUnauthorized):
		fmt.Fprintln(stderr, "hint: the token is out of date, so copy it again with:")
		fmt.Fprintln(stderr, "  "+copyTokenCommand)
	case errors.Is(err, ztapi.ErrServiceDown):
		fmt.Fprintln(stderr, "hint: start the service with: sudo systemctl start zerotier-one")
	case errors.As(err, &insecure) && insecure.Fixable():
		fmt.Fprintf(stderr, "hint: chmod 600 %s\n", insecure.Path)
	}
	return 1
}
