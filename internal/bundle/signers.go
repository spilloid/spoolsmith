package bundle

import (
	"bytes"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/spilloid/spoolsmith/internal/install"
)

// The same rules the privileged staging script applies (bundledriver.go):
// PowerShell's -match is case-insensitive, so these are too.
var (
	infSection     = regexp.MustCompile(`(?i)^\s*\[\s*([^\]]+?)\s*\]`)
	infCatalogFile = regexp.MustCompile(`(?i)^\s*CatalogFile(\.[^=\s]+)?\s*=\s*"?([^";]+?)"?\s*(;.*)?$`)
)

// catalogSigners names the signer of every catalog the payload's INF declares
// in [Version] CatalogFile entries, read from the catalog's own PKCS#7
// signature. These are the only certificates the staging script may add to
// TrustedPublisher, and the plan names them so an operator sees exactly which
// publisher a confirmation will trust.
//
// Every value comes from bundle content, so the same bundle names the same
// signers on every machine. Whether Windows will report a signature Valid is
// machine-specific and still decided at execution time; this only says who
// signed. A catalog that is missing or that SpoolSmith cannot parse names no
// signer, and the script will not trust a signer the plan does not name.
func catalogSigners(dir, inf string) ([]install.BundleSigner, error) {
	infPath := filepath.Join(dir, filepath.FromSlash(inf))
	raw, err := os.ReadFile(infPath)
	if err != nil {
		return nil, err
	}
	names, err := declaredCatalogs(decodeINF(raw))
	if err != nil {
		return nil, err
	}
	var signers []install.BundleSigner
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(infPath), name))
		if err != nil {
			continue
		}
		cert, err := catalogSignerCertificate(data)
		if err != nil {
			continue
		}
		signers = append(signers, install.BundleSigner{
			Catalog:    name,
			Subject:    cert.Subject.String(),
			Thumbprint: thumbprint(cert),
		})
	}
	return signers, nil
}

// declaredCatalogs returns the sorted, de-duplicated CatalogFile names from an
// INF's [Version] section. A name with a path separator is refused, as the
// staging script refuses it.
func declaredCatalogs(text string) ([]string, error) {
	seen := map[string]bool{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := infSection.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if !strings.EqualFold(section, "Version") {
			continue
		}
		if m := infCatalogFile.FindStringSubmatch(line); m != nil {
			name := m[2]
			if strings.ContainsAny(name, `\/:`) {
				return nil, fmt.Errorf("bundle: driver INF names catalog %s outside its own directory; SpoolSmith will not use it", name)
			}
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// decodeINF reads an INF as Get-Content does: UTF-16 with a byte-order mark,
// UTF-8 with or without one.
func decodeINF(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		order := binary.ByteOrder(binary.LittleEndian)
		if raw[0] == 0xFE {
			order = binary.BigEndian
		}
		units := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			units = append(units, order.Uint16(raw[i:]))
		}
		return string(utf16.Decode(units))
	default:
		return string(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	}
}

type pkcs7ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type pkcs7SignedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	ContentInfo      asn1.RawValue
	Certificates     asn1.RawValue     `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue     `asn1:"optional,tag:1"`
	SignerInfos      []pkcs7SignerInfo `asn1:"set"`
}

// Only the fields up to the signer identifier are read; encoding/asn1 allows
// the rest of the SEQUENCE to follow.
type pkcs7SignerInfo struct {
	Version int
	SID     asn1.RawValue
}

type pkcs7IssuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

// catalogSignerCertificate returns the certificate of the first signer of a
// PKCS#7 SignedData catalog: the one Get-AuthenticodeSignature reports as the
// SignerCertificate.
func catalogSignerCertificate(data []byte) (*x509.Certificate, error) {
	var info pkcs7ContentInfo
	if _, err := asn1.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if !info.ContentType.Equal(oidSignedData) {
		return nil, errors.New("bundle: catalog is not PKCS#7 signed data")
	}
	var signed pkcs7SignedData
	if _, err := asn1.Unmarshal(info.Content.Bytes, &signed); err != nil {
		return nil, err
	}
	if len(signed.SignerInfos) == 0 {
		return nil, errors.New("bundle: catalog has no signer")
	}
	var certs []*x509.Certificate
	for rest := signed.Certificates.Bytes; len(rest) > 0; {
		var element asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &element); err != nil {
			return nil, err
		}
		if cert, err := x509.ParseCertificate(element.FullBytes); err == nil {
			certs = append(certs, cert)
		}
	}
	sid := signed.SignerInfos[0].SID
	for _, cert := range certs {
		switch {
		case sid.Class == asn1.ClassUniversal && sid.Tag == asn1.TagSequence:
			var ias pkcs7IssuerAndSerial
			if _, err := asn1.Unmarshal(sid.FullBytes, &ias); err != nil {
				return nil, err
			}
			if bytes.Equal(cert.RawIssuer, ias.Issuer.FullBytes) && cert.SerialNumber.Cmp(ias.Serial) == 0 {
				return cert, nil
			}
		case sid.Class == asn1.ClassContextSpecific && sid.Tag == 0:
			if len(cert.SubjectKeyId) > 0 && bytes.Equal(cert.SubjectKeyId, sid.Bytes) {
				return cert, nil
			}
		}
	}
	return nil, errors.New("bundle: catalog signer certificate is not in the catalog")
}

// thumbprint is the certificate's SHA-1 thumbprint as Windows prints it.
func thumbprint(cert *x509.Certificate) string {
	sum := sha1.Sum(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
