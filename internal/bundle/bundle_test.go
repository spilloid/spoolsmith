package bundle

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

func testProfile() install.Profile {
	return install.Profile{
		Version:     1,
		Target:      "192.0.2.10",
		PrinterName: "Accounting",
		DriverName:  "Brother HL-L2315D series",
		Evidence: evidence.Evidence{
			IP:         "192.0.2.10",
			Provenance: "captured",
			HTTPTitle:  "Brother HL-L2315D series",
			PJLID:      "Brother HL-L2315D series",
		},
	}
}

func writePayload(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "brhl2315a.inf"), []byte("[Version]\nCatalogFile=brhl2315a.cat\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "brhl2315a.cat"), []byte("catalog bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "amd64"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "amd64", "driver.dll"), []byte("driver bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeBundle(t *testing.T, withDriver bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "office.ssb")
	manifest := Manifest{Profile: testProfile(), SourceHost: "BENCH-01"}
	payload := ""
	if withDriver {
		manifest.Driver = &DriverPayload{
			WindowsDriverName: "Brother HL-L2315D series",
			INF:               "brhl2315a.inf",
			ExportedFrom:      "oem15.inf",
		}
		payload = writePayload(t)
	}
	if err := Write(path, manifest, payload); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWriteReadVerifyRoundTrip(t *testing.T) {
	path := writeBundle(t, true)
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatalf("Verify() = %v, want nil", err)
	}
	if opened.Manifest.Profile.PrinterName != "Accounting" {
		t.Fatalf("profile did not survive the round trip: %#v", opened.Manifest.Profile)
	}
	if got := len(opened.Manifest.Driver.Files); got != 3 {
		t.Fatalf("payload files = %d, want 3", got)
	}
	// Write fills the file list from the archive's real contents, so a caller
	// cannot cause the manifest and payload to disagree.
	for _, file := range opened.Manifest.Driver.Files {
		if len(file.SHA256) != 64 {
			t.Fatalf("file %q has no hash", file.Path)
		}
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	path := writeBundle(t, false)
	err := Write(path, Manifest{Profile: testProfile()}, "")
	if err == nil || !os.IsExist(err) {
		t.Fatalf("Write() over an existing bundle = %v, want an exists error", err)
	}
}

func TestPayloadDigestIsStableAcrossWrites(t *testing.T) {
	payload := writePayload(t)
	digests := make([]string, 2)
	for index := range digests {
		path := filepath.Join(t.TempDir(), "same.ssb")
		manifest := Manifest{
			Profile: testProfile(),
			Driver:  &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "brhl2315a.inf"},
		}
		if err := Write(path, manifest, payload); err != nil {
			t.Fatal(err)
		}
		opened, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		digests[index] = opened.PayloadDigest()
		opened.Close()
	}
	// Two clones of the same driver must produce the same staging directory,
	// or the same reviewed plan would not fingerprint identically on the next
	// machine and scripted rollout would be impossible.
	if digests[0] != digests[1] {
		t.Fatalf("payload digest is not stable: %q vs %q", digests[0], digests[1])
	}
}

func TestOpenRejectsTamperedPayload(t *testing.T) {
	path := writeBundle(t, true)
	tampered := filepath.Join(t.TempDir(), "tampered.ssb")
	// Same length as the original, so the size check cannot be what catches
	// this and the hash check is genuinely exercised.
	rewriteEntry(t, path, tampered, "payload/brhl2315a.inf", []byte("[Version]\nCatalogFile=evilcat00.cat\n"))

	opened, err := Open(tampered)
	if err != nil {
		t.Fatalf("Open() = %v; the manifest itself is still well formed", err)
	}
	defer opened.Close()
	if err := opened.Verify(); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Verify() = %v, want a hash failure", err)
	}
	if err := opened.Extract(filepath.Join(t.TempDir(), "stage")); err == nil {
		t.Fatal("Extract() accepted a tampered payload")
	}
}

func TestExtractLeavesNothingBehindOnFailure(t *testing.T) {
	path := writeBundle(t, true)
	tampered := filepath.Join(t.TempDir(), "tampered.ssb")
	rewriteEntry(t, path, tampered, "payload/amd64/driver.dll", []byte("different bytes entirely"))
	opened, err := Open(tampered)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	dest := filepath.Join(t.TempDir(), "stage")
	if err := opened.Extract(dest); err == nil {
		t.Fatal("Extract() accepted a tampered payload")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a failed extraction left %q behind for a later step to stage", dest)
	}
}

func TestOpenRejectsUnlistedArchiveEntry(t *testing.T) {
	path := writeBundle(t, true)
	smuggled := filepath.Join(t.TempDir(), "smuggled.ssb")
	addEntry(t, path, smuggled, "payload/extra.dll", []byte("unlisted"))
	if _, err := Open(smuggled); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("Open() = %v, want rejection of an entry the manifest does not list", err)
	}
}

func TestManifestValidationRejectsUnsafeAndInconsistentDocuments(t *testing.T) {
	tests := []struct {
		name string
		want string
		make func() Manifest
	}{
		{
			name: "driver name disagrees with profile",
			want: "disagrees with itself",
			make: func() Manifest {
				m := Manifest{Version: Version, Profile: testProfile()}
				m.Driver = &DriverPayload{WindowsDriverName: "Some Other Driver", INF: "a.inf", Files: []File{{Path: "a.inf", SHA256: strings.Repeat("a", 64)}}}
				return m
			},
		},
		{
			name: "traversal path",
			want: "traversal",
			make: func() Manifest {
				m := Manifest{Version: Version, Profile: testProfile()}
				m.Driver = &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "a.inf", Files: []File{
					{Path: "a.inf", SHA256: strings.Repeat("a", 64)},
					{Path: "../../windows/system32/evil.dll", SHA256: strings.Repeat("b", 64)},
				}}
				return m
			},
		},
		{
			name: "absolute path",
			want: "absolute",
			make: func() Manifest {
				m := Manifest{Version: Version, Profile: testProfile()}
				m.Driver = &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "a.inf", Files: []File{
					{Path: "a.inf", SHA256: strings.Repeat("a", 64)},
					{Path: "/etc/passwd", SHA256: strings.Repeat("b", 64)},
				}}
				return m
			},
		},
		{
			name: "drive-qualified path",
			want: "names a drive",
			make: func() Manifest {
				m := Manifest{Version: Version, Profile: testProfile()}
				m.Driver = &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "a.inf", Files: []File{
					{Path: "a.inf", SHA256: strings.Repeat("a", 64)},
					{Path: "C:windows/evil.dll", SHA256: strings.Repeat("b", 64)},
				}}
				return m
			},
		},
		{
			name: "INF not listed",
			want: "not among the listed files",
			make: func() Manifest {
				m := Manifest{Version: Version, Profile: testProfile()}
				m.Driver = &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "missing.inf", Files: []File{{Path: "a.inf", SHA256: strings.Repeat("a", 64)}}}
				return m
			},
		},
		{
			name: "wrong version",
			want: "unsupported bundle version",
			make: func() Manifest {
				return Manifest{Version: 99, Profile: testProfile()}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.make().Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestEnsureExtractedReusesAndRefusesMismatch(t *testing.T) {
	path := writeBundle(t, true)
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	dest := filepath.Join(t.TempDir(), "stage")

	if err := opened.EnsureExtracted(dest); err != nil {
		t.Fatalf("first EnsureExtracted() = %v", err)
	}
	// Applying the same bundle twice is the normal case in a rollout; the
	// second run must verify and reuse rather than fail on an existing path.
	if err := opened.EnsureExtracted(dest); err != nil {
		t.Fatalf("second EnsureExtracted() = %v, want reuse", err)
	}

	if err := os.WriteFile(filepath.Join(dest, "brhl2315a.inf"), []byte("locally modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := opened.EnsureExtracted(dest); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("EnsureExtracted() over modified staging = %v, want a mismatch error", err)
	}
}

func TestWriteRejectsMismatchedPayloadDeclaration(t *testing.T) {
	if err := Write(filepath.Join(t.TempDir(), "a.ssb"), Manifest{Profile: testProfile()}, writePayload(t)); err == nil {
		t.Fatal("Write() accepted a payload directory with no declared driver payload")
	}
	manifest := Manifest{Profile: testProfile(), Driver: &DriverPayload{WindowsDriverName: "Brother HL-L2315D series", INF: "a.inf"}}
	if err := Write(filepath.Join(t.TempDir(), "b.ssb"), manifest, ""); err == nil {
		t.Fatal("Write() accepted a declared driver payload with no payload directory")
	}
}

func TestOpenRejectsUnknownManifestFields(t *testing.T) {
	path := writeBundle(t, false)
	altered := filepath.Join(t.TempDir(), "altered.ssb")
	var manifest map[string]any
	source, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := source.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(entry).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	entry.Close()
	source.Close()
	manifest["run_this_command"] = "shutdown /r"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	rewriteEntry(t, path, altered, ManifestName, encoded)
	if _, err := Open(altered); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Open() = %v, want rejection of an unknown manifest field", err)
	}
}

// rewriteEntry copies an archive, replacing one entry's bytes.
func rewriteEntry(t *testing.T, source, dest, name string, content []byte) {
	t.Helper()
	copyArchive(t, source, dest, func(entryName string) ([]byte, bool) {
		if entryName == name {
			return content, true
		}
		return nil, false
	}, nil)
}

// addEntry copies an archive and appends one extra entry.
func addEntry(t *testing.T, source, dest, name string, content []byte) {
	t.Helper()
	copyArchive(t, source, dest, nil, func(w *zip.Writer) {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	})
}

func copyArchive(t *testing.T, source, dest string, replace func(string) ([]byte, bool), extra func(*zip.Writer)) {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	writer := zip.NewWriter(out)
	for _, file := range reader.File {
		entry, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if replace != nil {
			if content, ok := replace(file.Name); ok {
				if _, err := entry.Write(content); err != nil {
					t.Fatal(err)
				}
				continue
			}
		}
		original, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, file.UncompressedSize64)
		read, _ := original.Read(buffer)
		original.Close()
		if _, err := entry.Write(buffer[:read]); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		extra(writer)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
