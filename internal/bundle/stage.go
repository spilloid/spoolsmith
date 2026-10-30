package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spilloid/spoolsmith/internal/install"
)

// StageDirName is the extraction directory's base name, derived only from the
// payload's own content.
//
// This determinism is load-bearing for scripted rollout. The staging path
// appears verbatim in the plan's PowerShell, and the plan is fingerprinted, so
// a random per-run directory would give every machine a different fingerprint
// and make a reviewed plan impossible to name in a loop. Deriving the name
// from the payload digest means the same bundle produces the same plan text on
// every machine, and a different payload produces a different one.
func (b *Bundle) StageDirName() string {
	return "SpoolSmith-bundle-" + b.PayloadDigest()
}

// PayloadDigest is a stable digest over the payload's file list. Manifest.Files
// is sorted by path at write time, so this is reproducible.
func (b *Bundle) PayloadDigest() string {
	digest := sha256.New()
	if b.Manifest.Driver != nil {
		fmt.Fprintf(digest, "%s\n", b.Manifest.Driver.WindowsDriverName)
		fmt.Fprintf(digest, "%s\n", b.Manifest.Driver.INF)
		for _, f := range b.Manifest.Driver.Files {
			fmt.Fprintf(digest, "%s %d %s\n", f.Path, f.Size, f.SHA256)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))[:32]
}

// StageDir is the absolute extraction directory for this bundle under the
// process's temp directory.
func (b *Bundle) StageDir() string {
	return filepath.Join(os.TempDir(), b.StageDirName())
}

// EnsureExtracted makes dir hold this bundle's verified payload, in a
// directory only administrators could have written.
//
// The privileged step later hands this directory to pnputil without hashing it
// again, and under SYSTEM it lives in C:\Windows\Temp, where standard users can
// create folders. So the directory is created in one call with an
// administrators-only DACL (createStageDir), and every file is extracted and
// hash checked into it. If something already exists at dir â€” the normal case
// when the same bundle is applied twice â€” it is reused only if checkStageDir
// shows no one outside SYSTEM and Administrators could have changed it, and
// its contents still match the manifest. Anything else is an error the
// operator resolves, never something to overwrite: this runs inside a
// privileged operation, and recursively deleting a path someone else may have
// planted is not a risk worth taking to save a manual step.
func (b *Bundle) EnsureExtracted(dir string) error {
	if b.Manifest.Driver == nil {
		return fmt.Errorf("bundle: this bundle carries no driver payload")
	}
	err := createStageDir(dir)
	if err == nil {
		if err := checkStageDir(dir); err != nil {
			os.Remove(dir)
			return fmt.Errorf("bundle: the staging directory %q SpoolSmith just created is not administrators-only (%v)", dir, err)
		}
		if err := b.extractInto(dir); err != nil {
			os.RemoveAll(dir)
			return err
		}
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := checkStageDir(dir); err != nil {
		return fmt.Errorf("bundle: staging path %q already exists and was not created by an administrator: %v. SpoolSmith will not use or delete it; check where it came from, remove it as an administrator, and retry", dir, err)
	}
	for _, file := range b.Manifest.Driver.Files {
		target := filepath.Join(dir, filepath.FromSlash(file.Path))
		existing, err := os.Open(target)
		if err != nil {
			return fmt.Errorf("bundle: staging directory %q is incomplete (%v); remove it and retry", dir, err)
		}
		digest := sha256.New()
		written, copyErr := io.Copy(digest, io.LimitReader(existing, file.Size+1))
		existing.Close()
		if copyErr != nil {
			return copyErr
		}
		if written != file.Size || hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
			return fmt.Errorf("bundle: staged file %q does not match the bundle; remove %q and retry", file.Path, dir)
		}
	}
	return nil
}

// verifyPayload extracts and hash checks the driver payload into a private
// temporary directory, reads its catalog signers, then removes it.
func (b *Bundle) verifyPayload() ([]install.BundleSigner, error) {
	work, err := os.MkdirTemp("", "spoolsmith-verify-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	payload := filepath.Join(work, "payload")
	if err := b.Extract(payload); err != nil {
		return nil, err
	}
	return catalogSigners(payload, b.Manifest.Driver.INF)
}
