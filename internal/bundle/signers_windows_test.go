//go:build windows

package bundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The plan names signers by parsing catalogs in Go; the staging script
// compares them with Get-AuthenticodeSignature. They must agree on real
// Windows catalogs, or a named signer would never match.
func TestCatalogSignerMatchesGetAuthenticodeSignature(t *testing.T) {
	root := filepath.Join(os.Getenv("SystemRoot"), "System32", "CatRoot", "{F750E6C3-38EE-11D1-85E5-00C04FC295EE}")
	catalogs, _ := filepath.Glob(filepath.Join(root, "*.cat"))
	if len(catalogs) == 0 {
		t.Skip("no system catalogs to compare against")
	}
	if len(catalogs) > 8 {
		catalogs = catalogs[:16]
	}
	checked := 0
	for _, path := range catalogs {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cert, err := catalogSignerCertificate(data)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-AuthenticodeSignature -LiteralPath $env:SPOOLSMITH_CATALOG).SignerCertificate.Thumbprint")
		cmd.Env = append(os.Environ(), "SPOOLSMITH_CATALOG="+path)
		out, err := cmd.Output()
		if err != nil {
			// Windows itself could not read this catalog (in use, or PowerShell failed on the
			// runner): there is nothing to compare against, so it proves nothing either way.
			t.Logf("%s: Get-AuthenticodeSignature: %v", filepath.Base(path), err)
			continue
		}
		want := strings.TrimSpace(string(out))
		if want == "" {
			continue
		}
		if got := thumbprint(cert); got != want {
			t.Errorf("%s: parsed signer %s, Windows reports %s", filepath.Base(path), got, want)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("no signed system catalogs")
	}
}
