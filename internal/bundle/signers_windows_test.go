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
		catalogs = catalogs[:8]
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
		out, err := exec.Command("powershell", "-NoProfile", "-Command", "(Get-AuthenticodeSignature -LiteralPath '"+path+"').SignerCertificate.Thumbprint").Output()
		if err != nil {
			t.Fatalf("Get-AuthenticodeSignature: %v", err)
		}
		want := strings.TrimSpace(string(out))
		if want == "" {
			continue
		}
		signers := []string{thumbprint(cert)}
		if signers[0] != want {
			t.Errorf("%s: parsed signer %s, Windows reports %s", filepath.Base(path), signers[0], want)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("no signed system catalogs")
	}
}
