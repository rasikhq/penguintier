// Package atomicfile writes a file so that nobody ever sees half of it.
//
// Penguintier writes a handful of small files that it, or another program,
// reads back later: the token copy, saved_networks.json, state.json, the
// DNS daemon's applied.json, the autostart entry and the backup of the DNS
// switch. They are all written the same way, through WriteFile, so the
// guarantee is the same everywhere and explained once, here.
//
// How it works:
//
//  1. Write the data to a new temporary file in the same directory.
//     os.CreateTemp opens it with O_EXCL ("fail if it already exists"),
//     so we never write into a file someone else prepared, and it starts
//     with mode 0600 (only we can read it).
//  2. Set the mode (and, for WriteFileOwner, the owner) on that file.
//  3. fsync it, so the bytes are really on disk.
//  4. Rename it over the old file. On Linux a rename within one directory
//     is atomic: the name points to the old file or the new one, never to
//     nothing. If path is a symlink, the symlink itself is replaced; its
//     target is not touched.
//  5. fsync the directory, so the rename itself is on disk.
//
// A crash or power cut in the middle leaves the old file in place, and at
// worst a temporary file called ".<name>.tmp-<random>" next to it.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile replaces the file at path with data, with the permission bits
// perm (for example 0o600). The directory must exist already.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	return write(path, data, perm, -1, -1)
}

// WriteFileOwner is WriteFile for programs running as root that must also
// give the file an owner, such as penguintier-dns-helper putting
// /etc/resolv.conf back the way it was. uid and gid are numeric IDs.
func WriteFileOwner(path string, data []byte, perm fs.FileMode, uid, gid int) error {
	return write(path, data, perm, uid, gid)
}

// write does the work. A uid of -1 keeps the owner CreateTemp gave the
// file: the user running the program.
func write(path string, data []byte, perm fs.FileMode, uid, gid int) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	// defer runs this function when write returns, however it returns.
	// Because the result is named (err error, in the signature), the
	// deferred function can see whether we are returning an error, and
	// then removes the temporary file so it doesn't pile up.
	defer func() {
		if err != nil {
			_ = tmp.Close() // may already be closed; that error is not interesting
			_ = os.Remove(tmpPath)
		}
	}()

	// CreateTemp always uses 0600; set the mode the caller asked for
	// explicitly, so the user's umask can't change it.
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if uid >= 0 {
		if err := tmp.Chown(uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", tmpPath, err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}

	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so a rename inside it survives a crash.
// Errors are ignored on purpose: the new file is already in place, and some
// file systems don't support fsync on directories at all.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is the parent of a path we just wrote
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
