package bundle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
)

// UnconfirmedIdentityNotice is the one plain sentence every surface (CLI and
// desktop, a single copy or a batch) uses when a profile's printer never
// answered during copy, so its identity was never confirmed. Descriptive, not
// a coined term: it says what happened and what follows from it, once,
// instead of each surface phrasing the same fact its own way.
const UnconfirmedIdentityNotice = "The printer did not answer, so its identity was not confirmed. Applying it will run offline; check the printer once it is reachable."

// SaveProfile writes a standalone profile as a bundle carrying no driver
// payload -- the file `profile capture` produces. It never overwrites an
// existing file: an operator's captured setup is inventory, and silently
// replacing it is a mistake this refuses to make.
//
// This, LoadProfile and EditProfile are the one place a Profile is read from
// or written to disk. There is no separate bare-JSON profile format: a saved
// printer and a printer handed to another PC are the same file shape, an
// .ssb, whether or not a driver payload rides along.
func SaveProfile(path string, p install.Profile) error {
	return Write(path, Manifest{Profile: p}, "")
}

// LoadProfile opens a bundle and returns its profile alone. Callers that also
// need an embedded driver payload -- the install/apply paths that stage a new
// queue -- use Open and PrepareDriver directly instead; everything else
// (configure, remove, status, saved-setup review) only ever needed the
// profile document, so this is the common case.
func LoadProfile(path string) (install.Profile, error) {
	opened, err := Open(path)
	if err != nil {
		return install.Profile{}, err
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return install.Profile{}, err
	}
	return opened.Manifest.Profile, nil
}

// EditProfile updates a saved profile's fields in place. It preserves the
// file's existing driver payload (if any) and provenance (created/source/note),
// and keeps the previous version as a backup before replacing it -- the one
// path that legitimately overwrites a profile file, matching the guarantee
// `profile edit` has always made.
func EditProfile(path string, p install.Profile) (string, error) {
	current, err := Open(path)
	if err != nil {
		return "", err
	}
	manifest := current.Manifest
	manifest.Profile = p

	var payloadRoot string
	if manifest.Driver != nil {
		parent, tmpErr := os.MkdirTemp("", "spoolsmith-profile-edit-")
		if tmpErr != nil {
			current.Close()
			return "", tmpErr
		}
		defer os.RemoveAll(parent)
		// Extract creates its destination itself and refuses one that already
		// exists, so it gets a not-yet-created child of the reserved parent
		// rather than the parent MkdirTemp already created.
		extracted := filepath.Join(parent, "payload")
		if extractErr := current.Extract(extracted); extractErr != nil {
			current.Close()
			return "", extractErr
		}
		payloadRoot = extracted
	}
	if closeErr := current.Close(); closeErr != nil {
		return "", closeErr
	}

	backupDir := filepath.Join(filepath.Dir(path), ".backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", err
	}
	backupPath, err := copyToBackup(path, backupDir)
	if err != nil {
		return "", err
	}

	// Write reserves a real path itself (O_CREATE|O_EXCL), so the staged name
	// only needs to not already exist -- a fresh name per attempt, never
	// pre-created, unlike os.CreateTemp.
	staged := filepath.Join(filepath.Dir(path), fmt.Sprintf(".profile-edit-%d-%d.tmp", os.Getpid(), time.Now().UnixNano()))
	if err := Write(staged, manifest, payloadRoot); err != nil {
		return backupPath, err
	}
	if err := os.Rename(staged, path); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

// copyToBackup copies path's current bytes into a new, uniquely-named file
// under backupDir, verbatim -- the previous version is preserved exactly as
// it was, not re-derived from a decoded-and-re-encoded copy.
func copyToBackup(path, backupDir string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.CreateTemp(backupDir, filepath.Base(path)+"-*.bak")
	if err != nil {
		return "", err
	}
	backupPath := dst.Name()
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return backupPath, copyErr
	}
	return backupPath, closeErr
}
