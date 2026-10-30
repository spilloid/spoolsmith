package bundle

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// testCatalog builds a minimal PKCS#7 SignedData whose one signer is a fresh
// self-signed certificate, plus an unrelated certificate listed first so the
// signer has to be matched by issuer and serial rather than position.
func testCatalog(t *testing.T, subject string) (catalog []byte, thumbprint string) {
	t.Helper()
	newCert := func(cn string, serial int64) []byte {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	decoy := newCert("Decoy CA", 7)
	signer := newCert(subject, 42)
	parsed, err := x509.ParseCertificate(signer)
	if err != nil {
		t.Fatal(err)
	}
	marshal := func(v any, params string) []byte {
		out, err := asn1.MarshalWithParams(v, params)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ias := marshal(struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}{asn1.RawValue{FullBytes: parsed.RawIssuer}, parsed.SerialNumber}, "")
	signerInfo := marshal(struct {
		Version int
		SID     asn1.RawValue
		Digest  asn1.RawValue
	}{1, asn1.RawValue{FullBytes: ias}, asn1.RawValue{FullBytes: marshal(struct{ Algorithm asn1.ObjectIdentifier }{asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}}, "")}}, "")
	signed := marshal(struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		ContentInfo      asn1.RawValue
		Certificates     asn1.RawValue
		SignerInfos      asn1.RawValue
	}{
		1,
		asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true},
		asn1.RawValue{FullBytes: marshal(struct{ Type asn1.ObjectIdentifier }{asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 10, 1}}, "")},
		asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: append(append([]byte{}, decoy...), signer...)},
		asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: signerInfo},
	}, "")
	catalog = marshal(struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue
	}{oidSignedData, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: signed}}, "")
	if _, err := catalogSignerCertificate(catalog); err != nil {
		t.Fatalf("test catalog does not parse: %v", err)
	}
	sum := sha1.Sum(signer)
	return catalog, strings.ToUpper(hex.EncodeToString(sum[:]))
}

func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestCatalogSignersNamesTheSignerOfEachDeclaredCatalog(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver")
	if err := os.Mkdir(driver, 0700); err != nil {
		t.Fatal(err)
	}
	hp, hpThumb := testCatalog(t, "HP Inc.")
	other, otherThumb := testCatalog(t, "Other Signer")
	stray, _ := testCatalog(t, "Not Declared")
	inf := "; comment\r\n[version]\r\nSignature=\"$Windows NT$\"\r\ncatalogfile = hp.cat ; main\r\nCatalogFile.NTamd64 = \"other.cat\"\r\nCatalogFile = missing.cat\r\n[Strings]\r\nCatalogFile = strings.cat\r\n"
	files := map[string][]byte{"hp.inf": utf16LE(inf), "hp.cat": hp, "other.cat": other, "strings.cat": stray, "stray.cat": stray}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(driver, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	signers, err := catalogSigners(dir, "driver/hp.inf")
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 2 {
		t.Fatalf("signers = %#v, want hp.cat and other.cat only (missing, [Strings] and undeclared catalogs never count)", signers)
	}
	if signers[0].Catalog != "hp.cat" || signers[0].Thumbprint != hpThumb || signers[0].Subject != "CN=HP Inc." {
		t.Fatalf("signers[0] = %#v, want hp.cat signed by CN=HP Inc. (%s)", signers[0], hpThumb)
	}
	if signers[1].Catalog != "other.cat" || signers[1].Thumbprint != otherThumb {
		t.Fatalf("signers[1] = %#v, want other.cat (%s)", signers[1], otherThumb)
	}
}

func TestCatalogSignersRefusesACatalogOutsideTheINFDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.inf"), []byte("[Version]\nCatalogFile=..\\evil.cat\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogSigners(dir, "a.inf"); err == nil || !strings.Contains(err.Error(), "outside its own directory") {
		t.Fatalf("catalogSigners() = %v, want a refusal", err)
	}
}

func TestCatalogSignerCertificateRejectsGarbage(t *testing.T) {
	if _, err := catalogSignerCertificate([]byte("catalog")); err == nil {
		t.Fatal("catalogSignerCertificate() accepted bytes that are not PKCS#7")
	}
}
