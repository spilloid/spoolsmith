package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// EnsureExtracted makes dir hold this bundle's verified payload.
//
// If dir does not exist, the payload is extracted and every file is hash
// checked. If it already exists — the normal case when the same bundle is
// applied twice, or applied after a preview — its contents are verified
// against the manifest and reused. A directory that exists but does not match
// is an error the operator resolves, not something to overwrite: this runs
// inside a privileged operation, and recursively deleting a path derived from
// an environment variable is not a risk worth taking to save a manual step.
func (b *Bundle) EnsureExtracted(dir string) error {
	if b.Manifest.Driver == nil {
		return fmt.Errorf("bundle: this bundle carries no driver payload")
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return b.Extract(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("bundle: staging path %q exists and is not a directory; remove it and retry", dir)
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
