package dnssetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rasikhq/penguintier/internal/atomicfile"
)

// HelperPath is where the packages install penguintier-dns-helper. pkexec
// only runs it through Penguintier's polkit action from this exact path.
const HelperPath = "/usr/libexec/penguintier/penguintier-dns-helper"

// BackupDir is where the switch keeps its backup and manifest.
const BackupDir = "/var/lib/penguintier/dns-backup"

// manifestName is the manifest's file name inside BackupDir.
const manifestName = "manifest.json"

// AffectedFiles are the files installing systemd-resolved can change:
// the package's install script replaces /etc/resolv.conf with a link, and
// its optional helpers (libnss-resolve) would edit /etc/nsswitch.conf (we
// don't install those, but back the file up anyway). resolved.conf is the
// package's own settings file, which may be left over from an earlier
// install.
var AffectedFiles = []string{
	"/etc/resolv.conf",
	"/etc/nsswitch.conf",
	"/etc/systemd/resolved.conf",
}

// The states a switch goes through, as Manifest.State records them.
const (
	// StateInProgress: the backup is made, the switch has not finished.
	// After a crash or power cut, `penguintier-dns-helper undo` finishes
	// the job.
	StateInProgress = "in-progress"
	// StateEnabled: the switch worked. The backup stays for a later undo.
	StateEnabled = "enabled"
	// StateRolledBack: a check failed, and everything was put back.
	StateRolledBack = "rolled-back"
	// StateUndone: an enabled switch was undone on request.
	StateUndone = "undone"
	// StateUndoFailed: putting things back failed; the backup stays and
	// the manual steps were printed.
	StateUndoFailed = "undo-failed"
)

// Manifest describes one switch: its state, what was backed up, and what
// was installed. It is stored as JSON next to the copies.
type Manifest struct {
	Version int       `json:"version"`
	State   string    `json:"state"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	// Files are the backed-up files, in AffectedFiles order.
	Files []FileBackup `json:"files"`
	// Installed are the packages this switch installed, which an undo
	// removes again.
	Installed []string `json:"installed"`
	// CheckName is the name looked up before and after the switch.
	CheckName string `json:"checkName"`
	// Message says what happened last, in a sentence.
	Message string `json:"message,omitempty"`
}

// FileBackup is one backed-up file.
type FileBackup struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	// Link is the target when the file was a symbolic link.
	Link string `json:"link,omitempty"`
	// Mode, UID and GID are the regular file's permissions and owner.
	Mode uint32 `json:"mode,omitempty"`
	UID  int    `json:"uid,omitempty"`
	GID  int    `json:"gid,omitempty"`
	// Copy is the copy's file name inside the backup folder, and SHA256
	// its checksum, so a restore can prove it wrote the same bytes back.
	Copy   string `json:"copy,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// manifestVersion is the current layout.
const manifestVersion = 1

// Store reads and writes the backup. Root is put in front of every system
// path ("" on a real system); tests use a temporary folder.
type Store struct {
	Root string
}

// path returns a system path under s.Root.
func (s Store) path(p string) string {
	return filepath.Join(s.Root, p)
}

// dir is the backup folder.
func (s Store) dir() string {
	return s.path(BackupDir)
}

// Load reads the manifest. A missing manifest returns (nil, nil): no
// switch was ever made.
func (s Store) Load() (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(), manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the DNS switch backup: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("read the DNS switch backup: the manifest is damaged: %w", err)
	}
	return &m, nil
}

// Save writes the manifest atomically (see internal/atomicfile).
func (s Store) Save(m *Manifest) error {
	m.Version = manifestVersion
	m.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the DNS switch manifest: %w", err)
	}
	return atomicfile.WriteFile(filepath.Join(s.dir(), manifestName), append(data, '\n'), 0o644)
}

// Backup copies every file in AffectedFiles into the backup folder and
// saves a manifest in StateInProgress. An older backup is replaced (Check
// refuses to start while one is unfinished).
func (s Store) Backup(checkName string) (*Manifest, error) {
	// The copies are not secrets (all three files are readable by
	// everyone), and doctor reads the manifest, so the folder is readable.
	if err := os.MkdirAll(s.dir(), 0o755); err != nil { //nolint:gosec // a readable folder on purpose, see above
		return nil, fmt.Errorf("create the backup folder: %w", err)
	}
	now := time.Now().UTC()
	m := &Manifest{State: StateInProgress, Started: now, CheckName: checkName}

	for _, p := range AffectedFiles {
		fb := FileBackup{Path: p}
		info, err := os.Lstat(s.path(p))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Recorded as "did not exist", so an undo removes it again.
		case err != nil:
			return nil, fmt.Errorf("back up %s: %w", p, err)
		case info.Mode()&fs.ModeSymlink != 0:
			fb.Existed = true
			if fb.Link, err = os.Readlink(s.path(p)); err != nil {
				return nil, fmt.Errorf("back up %s: %w", p, err)
			}
		case info.Mode().IsRegular():
			fb.Existed = true
			data, err := os.ReadFile(s.path(p)) //nolint:gosec // one of the fixed AffectedFiles
			if err != nil {
				return nil, fmt.Errorf("back up %s: %w", p, err)
			}
			fb.Mode = uint32(info.Mode().Perm())
			// info.Sys() holds the raw stat data; the type assertion checks
			// it is Linux's *syscall.Stat_t.
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				fb.UID, fb.GID = int(st.Uid), int(st.Gid)
			}
			fb.Copy = filepath.Base(p)
			fb.SHA256 = checksum(data)
			if err := atomicfile.WriteFile(filepath.Join(s.dir(), fb.Copy), data, info.Mode().Perm()); err != nil {
				return nil, fmt.Errorf("back up %s: %w", p, err)
			}
		default:
			return nil, fmt.Errorf("back up %s: not a regular file or a link", p)
		}
		m.Files = append(m.Files, fb)
	}
	if err := s.Save(m); err != nil {
		return nil, err
	}
	return m, nil
}

// Restore puts every backed-up file back as it was: the same bytes, mode
// and owner, or the same link, and removes the files that did not exist.
// It keeps going after an error, so as much as possible is restored, and
// returns all errors together.
func (s Store) Restore(m *Manifest) error {
	var errs []error
	for _, fb := range m.Files {
		if err := s.restoreFile(fb); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", fb.Path, err))
		}
	}
	// errors.Join returns nil when errs is empty.
	return errors.Join(errs...)
}

func (s Store) restoreFile(fb FileBackup) error {
	target := s.path(fb.Path)
	switch {
	case !fb.Existed:
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	case fb.Link != "":
		// Make the link under a temporary name, then rename it over
		// whatever is there: the path is never missing in between.
		tmp := target + ".penguintier-restore"
		_ = os.Remove(tmp) // a leftover from an interrupted restore; may not exist
		if err := os.Symlink(fb.Link, tmp); err != nil {
			return err
		}
		return os.Rename(tmp, target)
	default:
		data, err := os.ReadFile(filepath.Join(s.dir(), fb.Copy))
		if err != nil {
			return fmt.Errorf("read the backup copy: %w", err)
		}
		if checksum(data) != fb.SHA256 {
			return errors.New("the backup copy is damaged (checksum mismatch)")
		}
		if err := atomicfile.WriteFileOwner(target, data, fs.FileMode(fb.Mode), fb.UID, fb.GID); err != nil {
			return err
		}
		// Prove it: read back what is there now.
		back, err := os.ReadFile(target) //nolint:gosec // one of the fixed AffectedFiles
		if err != nil {
			return err
		}
		if checksum(back) != fb.SHA256 {
			return errors.New("the restored file differs from the backup")
		}
		return nil
	}
}

// checksum returns data's SHA-256 as hex.
func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
