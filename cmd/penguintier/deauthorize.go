package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// runDeauthorize handles "penguintier deauthorize [--yes]": it deletes the
// user's token copy, ~/.zeroTierOneAuthToken, after asking (or at once with
// --yes). Exit codes: 0 deleted (or nothing to delete), 1 not deleted,
// 2 bad command line.
func runDeauthorize(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("penguintier deauthorize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	yes := flags.Bool("yes", false, "delete without asking")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "penguintier deauthorize: unexpected argument %q\n", flags.Arg(0))
		return 2
	}

	path, err := ztapi.UserTokenPath()
	if err != nil {
		fmt.Fprintln(stderr, "penguintier deauthorize:", err)
		return 1
	}
	// Lstat looks at the path itself: a symlink is removed, not followed.
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stdout, "There is no token copy at %s, so there is nothing to do.\n", path)
		return 0
	}

	if !*yes {
		fmt.Fprintf(stdout, "Delete %s?\nPenguintier and zerotier-cli will need authorizing again. [y/N] ", path)
		// ReadString reads up to and including the newline. At the end of
		// the input (EOF) it returns what it has, which counts as "no".
		answer, _ := bufio.NewReader(stdin).ReadString('\n') //nolint:errcheck // EOF or an error both mean "no"
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Nothing deleted.")
			return 1
		}
	}

	if err := os.Remove(path); err != nil {
		fmt.Fprintln(stderr, "penguintier deauthorize:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Deleted %s.\n", path)
	fmt.Fprintln(stdout, "A Penguintier that is running now keeps its access until it quits.")
	return 0
}
