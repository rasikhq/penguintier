package resolved

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysClassNet is where the kernel lists network interfaces, one directory
// per interface. It is a variable only so tests can point it at a
// temporary directory.
var sysClassNet = "/sys/class/net"

// maxIfNameLen is the longest Linux interface name: IFNAMSIZ (16) minus the
// terminating zero byte.
const maxIfNameLen = 15

// IfIndex returns the kernel's index number for the network interface
// called name (for example "ztabcdefgh"). resolved identifies links by this
// number.
//
// It reads /sys/class/net/<name>/ifindex every time. Don't keep the result
// around: when ZeroTier restarts it creates the interface again, and the
// new one usually gets a new number.
//
// A missing interface gives an error wrapping ErrNoInterface.
func IfIndex(name string) (int, error) {
	if !validIfName(name) {
		return 0, fmt.Errorf("invalid interface name %q", name)
	}
	path := filepath.Join(sysClassNet, name, "ifindex")
	data, err := os.ReadFile(path) //nolint:gosec // name was checked above, so the path stays inside /sys/class/net
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("interface %s: %w", name, ErrNoInterface)
	}
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	index, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || index <= 0 {
		return 0, fmt.Errorf("read %s: not an interface index: %q", path, strings.TrimSpace(string(data)))
	}
	return index, nil
}

// validIfName reports whether name can be a Linux interface name, so it is
// safe to put into a path: 1 to 15 characters, no "/", no spaces, and not
// "." or "..".
func validIfName(name string) bool {
	if name == "" || len(name) > maxIfNameLen || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r == ':' || r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}
