package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteFileReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, want := range []string{"first", "second, longer than the first"} {
		if err := WriteFile(path, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("file holds %q, want %q", got, want)
		}
	}
}

// TestWriteFileMode: the file gets exactly the mode asked for, whatever the
// umask says (a umask of 077 would turn 0644 into 0600 for a plain create).
func TestWriteFileMode(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	path := filepath.Join(t.TempDir(), "autostart.desktop")
	if err := WriteFile(path, []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %04o, want 0644", got)
	}
}

func TestWriteFileLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "token"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "token" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %q, want only the file", names)
	}
}

// TestWriteFileReplacesSymlink: a symlink at path is replaced by a regular
// file, and whatever it pointed to is left alone.
func TestWriteFileReplacesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("after the write, %s is %v, want a regular file", link, info.Mode())
	}
	if got, _ := os.ReadFile(target); string(got) != "untouched" {
		t.Errorf("the symlink's target was changed to %q", got)
	}
}

// TestWriteFileMissingDirectory: a failure leaves nothing behind.
func TestWriteFileMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	err := WriteFile(filepath.Join(dir, "missing", "file"), []byte("x"), 0o600)
	if err == nil {
		t.Fatal("writing into a missing directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("leftovers after a failed write: %v", entries)
	}
}

// TestWriteFileOwner: setting the owner to the user running the test works
// without root, and the mode and content are as asked.
func TestWriteFileOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := WriteFileOwner(path, []byte("nameserver 127.0.0.53\n"), 0o644, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %04o, want 0644", got)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no stat data")
	}
	if int(stat.Uid) != os.Getuid() || int(stat.Gid) != os.Getgid() {
		t.Errorf("owner = %d:%d, want %d:%d", stat.Uid, stat.Gid, os.Getuid(), os.Getgid())
	}
}

// TestWriteFileCleansUpOnFailure makes the final rename fail, after the
// temporary file was fully written, and checks that it is removed. A
// non-empty directory can't be replaced by a file, which works even as root.
func TestWriteFileCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, []byte("data"), 0o600); err == nil {
		t.Fatal("writing over a non-empty directory succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "target" {
		t.Errorf("leftovers after a failed write: %v", entries)
	}
}
