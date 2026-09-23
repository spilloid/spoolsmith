// Package intune exports local, reviewable Win32 app content. It never contacts
// a tenant or printer, downloads drivers, or executes an installer.
package intune

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/pe"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

//go:embed templates/*
var templates embed.FS

// EndpointCapability is emitted by compatible CLIs and prevents accidentally
// packaging older releases that lack the endpoint contract.
const EndpointCapability = "SpoolSmith:intune-endpoint-v2:ssb,offline,status"

type Options struct {
	ProfilePath        string
	BinaryPath         string
	BinarySHA256       string
	ID                 string
	Revision           int
	DisplayName        string
	Description        string
	Location           string
	Offline            bool
	DriverPrerequisite bool
	Adopt              bool
}

type Manifest struct {
	Format             int    `json:"format"`
	ID                 string `json:"id"`
	Revision           int    `json:"revision"`
	DisplayName        string `json:"display_name"`
	Description        string `json:"description"`
	Location           string `json:"location"`
	Architecture       string `json:"architecture"`
	Offline            bool   `json:"offline"`
	Adopt              bool   `json:"adopt_matching_queue"`
	DriverPrerequisite bool   `json:"driver_prerequisite"`
	BinarySHA256       string `json:"binary_sha256"`
	ProfileSHA256      string `json:"profile_sha256"`
	DriverSHA256       string `json:"driver_sha256,omitempty"`
	ConfigSHA256       string `json:"configuration_sha256"`
	// ProfileSource records the reviewed profile's origin for the README. It
	// is always "bundle" now that a profile has exactly one on-disk shape
	// (internal/bundle, extension .ssb); kept as a field rather than a
	// literal in the template so a future distinct source is a one-line
	// change here, not a template rewrite.
	ProfileSource string `json:"profile_source"`
	// BundleSHA256 pins the original .ssb file when it carries a driver
	// payload; empty otherwise. The bundle itself (not an extracted copy)
	// travels with the package so `apply`'s own catalog-signature trust chain
	// runs unchanged at install time.
	BundleSHA256 string `json:"bundle_sha256,omitempty"`
	// BundleSourceHost is the bundle manifest's own recorded source host, shown
	// for operator provenance only; it is never part of the trust decision.
	BundleSourceHost string          `json:"bundle_source_host,omitempty"`
	Profile          install.Profile `json:"profile"`
	InstallCommand   string          `json:"install_command"`
	UninstallCommand string          `json:"uninstall_command"`
	Files            []string        `json:"files"`
}

// Prepared retains the reviewed bytes. Export verifies payload pins again,
// refusing a changed source instead of exporting content different from preview.
type Prepared struct {
	Manifest Manifest
	files    map[string][]byte
	sources  map[string]string
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// loadedProfile is a validated profile plus the provenance and driver-payload
// facts Prepare needs to package it.
type loadedProfile struct {
	Profile    install.Profile
	SourceHost string // bundle.Manifest.SourceHost
	HasDriver  bool   // bundle.Manifest.Driver != nil
	BundleHash string // sha256 of the .ssb file itself; "" unless HasDriver
}

// loadProfileSource opens the .ssb bundle written by `profile capture` or
// `spoolsmith copy` -- there is exactly one on-disk profile format now, so
// there is nothing left to dispatch on. It returns an install.Profile that
// has already passed Profile.Validate(), both when the bundle was written
// (bundle.Write calls Manifest.Validate) and again here on open.
func loadProfileSource(path string) (loadedProfile, error) {
	b, err := bundle.Open(path)
	if err != nil {
		return loadedProfile{}, err
	}
	defer b.Close()
	if b.Manifest.Profile.DriverPackage != nil && b.Manifest.Driver != nil {
		return loadedProfile{}, errors.New("intune: this bundle carries both a driver payload and a separate vendor-archive reference; run profile edit --clear-package to drop one before packaging")
	}
	profile := b.Manifest.Profile
	if err := profile.ResolvePackagePath(path); err != nil {
		return loadedProfile{}, err
	}
	result := loadedProfile{Profile: profile, SourceHost: b.Manifest.SourceHost, HasDriver: b.Manifest.Driver != nil}
	if result.HasDriver {
		hash, err := HashBinary(path)
		if err != nil {
			return loadedProfile{}, err
		}
		result.BundleHash = hash
	}
	return result, nil
}

// HasLocalPayload reports whether the bundle at path already carries a driver
// payload (a vendor archive reference or an embedded exported driver store),
// so callers can decide whether to ask for the separately managed
// registered-driver prerequisite instead.
func HasLocalPayload(path string) (bool, error) {
	loaded, err := loadProfileSource(path)
	if err != nil {
		return false, err
	}
	return loaded.Profile.DriverPackage != nil || loaded.HasDriver, nil
}

func Prepare(o Options) (*Prepared, error) {
	if !identifier.MatchString(o.ID) {
		return nil, errors.New("deployment ID must be 1–64 lowercase letters, digits or hyphens, starting with a letter or digit")
	}
	if o.Revision < 1 {
		return nil, errors.New("deployment revision must be a positive integer")
	}
	if strings.TrimSpace(o.DisplayName) == "" {
		return nil, errors.New("app display name is required")
	}
	for _, s := range []string{o.DisplayName, o.Description, o.Location} {
		if len(s) > 4096 || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
			return nil, errors.New("app metadata must be at most 4096 bytes without control characters")
		}
	}
	loaded, err := loadProfileSource(o.ProfilePath)
	if err != nil {
		return nil, err
	}
	p := loaded.Profile
	hasLocalPayload := p.DriverPackage != nil || loaded.HasDriver
	if !hasLocalPayload && !o.DriverPrerequisite {
		return nil, errors.New("profile has no supported local payload: explicitly accept the separately managed registered-driver prerequisite")
	}
	if hasLocalPayload && o.DriverPrerequisite {
		return nil, errors.New("choose a local payload or a separately managed driver prerequisite, not both")
	}
	binaryHash := strings.ToLower(o.BinarySHA256)
	if binaryHash == "" {
		binaryHash, err = HashBinary(o.BinaryPath)
		if err != nil {
			return nil, err
		}
	}
	if b, e := hex.DecodeString(binaryHash); e != nil || len(b) != sha256.Size {
		return nil, errors.New("provide the reviewed Windows CLI binary SHA-256")
	}
	if err = verifyFile(o.BinaryPath, binaryHash); err != nil {
		return nil, err
	}
	f, err := pe.Open(o.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("Windows CLI binary: %w", err)
	}
	x64 := f.Machine == pe.IMAGE_FILE_MACHINE_AMD64
	f.Close()
	if !x64 {
		return nil, errors.New("only Windows x64 binaries and endpoints are supported")
	}
	info, err := buildinfo.ReadFile(o.BinaryPath)
	if err != nil || info.Path != "github.com/spilloid/spoolsmith/cmd/spoolsmith" {
		return nil, errors.New("select the SpoolSmith CLI, not the GUI or another executable")
	}
	if err := checkCapability(o.BinaryPath); err != nil {
		return nil, err
	}
	m := Manifest{Format: 2, ID: o.ID, Revision: o.Revision, DisplayName: o.DisplayName, Description: o.Description, Location: o.Location, Architecture: "amd64", Offline: o.Offline, Adopt: o.Adopt, DriverPrerequisite: o.DriverPrerequisite, BinarySHA256: binaryHash}
	sources := map[string]string{"spoolsmith.exe": o.BinaryPath}
	if p.DriverPackage != nil {
		hash, e := p.DriverPackage.PackageSHA256(p.DriverName)
		if e != nil {
			return nil, e
		}
		if e = verifyFile(p.DriverPackage.Archive, hash); e != nil {
			return nil, e
		}
		m.DriverSHA256 = hash
		sources["driver.exe"] = p.DriverPackage.Archive
		p.DriverPackage = &install.PackageSelection{ID: p.DriverPackage.ID, Archive: "driver.exe"}
	}
	m.ProfileSource = "bundle"
	m.BundleSourceHost = loaded.SourceHost
	if loaded.HasDriver {
		m.BundleSHA256 = loaded.BundleHash
		sources["bundle.ssb"] = o.ProfilePath
	}
	profileBytes, err := printerFileBytes(p)
	if err != nil {
		return nil, err
	}
	m.Profile = p
	m.ProfileSHA256 = digest(profileBytes)
	// The config digest binds deployment policy and payloads, excluding presentation.
	config, _ := json.Marshal(struct {
		Profile install.Profile
		Binary  string
		Offline bool
	}{p, binaryHash, o.Offline})
	m.ConfigSHA256 = digest(config)
	m.InstallCommand = `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File install.ps1`
	m.UninstallCommand = `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "& (Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'SpoolSmith\Deployments\` + o.ID + `\uninstall.ps1')"`
	m.Files = []string{"deployment.json", "profile.ssb", "spoolsmith.exe", "install.ps1", "uninstall.ps1", "detect.ps1", "runtime.ps1", "README.txt"}
	if p.DriverPackage != nil {
		m.Files = append(m.Files, "driver.exe")
	}
	if loaded.HasDriver {
		m.Files = append(m.Files, "bundle.ssb")
	}
	manifestBytes, _ := json.MarshalIndent(m, "", "  ")
	files := map[string][]byte{"profile.ssb": profileBytes, "deployment.json": append(manifestBytes, '\n')}
	for _, name := range []string{"install.ps1", "uninstall.ps1", "detect.ps1", "runtime.ps1", "README.txt"} {
		source, e := templates.ReadFile("templates/" + name)
		if e != nil {
			return nil, e
		}
		t, e := template.New(name).Funcs(template.FuncMap{"json64": func(v any) string { b, _ := json.Marshal(v); return base64.StdEncoding.EncodeToString(b) }}).Parse(string(source))
		if e != nil {
			return nil, e
		}
		var b bytes.Buffer
		if e = t.Execute(&b, m); e != nil {
			return nil, e
		}
		files[name] = b.Bytes()
		if strings.HasSuffix(name, ".ps1") {
			files[name] = append([]byte{0xef, 0xbb, 0xbf}, files[name]...)
		}
	}
	return &Prepared{Manifest: m, files: files, sources: sources}, nil
}

// Export creates a new directory only; existing administrator content is preserved.
func (p *Prepared) Export(destination string) error {
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(destination)
		}
	}()
	for name, data := range p.files {
		if err := os.WriteFile(filepath.Join(destination, name), data, 0600); err != nil {
			return err
		}
	}
	for name, source := range p.sources {
		expected := p.Manifest.BinarySHA256
		if name == "driver.exe" {
			expected = p.Manifest.DriverSHA256
		}
		if name == "bundle.ssb" {
			expected = p.Manifest.BundleSHA256
		}
		if err := copyPinned(source, filepath.Join(destination, name), expected); err != nil {
			return err
		}
	}
	success = true
	return nil
}

// Use the same writer as capture and editing; the endpoint CLI reads only bundles.
func printerFileBytes(p install.Profile) ([]byte, error) {
	dir, err := os.MkdirTemp("", "spoolsmith-intune-profile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "profile.ssb")
	// This generated projection has no capture time of its own. A fixed timestamp
	// keeps its bytes and payload hash stable across identical packaging runs.
	if err := bundle.Write(path, bundle.Manifest{Profile: p, Created: "1980-01-01T00:00:00Z"}, ""); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func checkCapability(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	carry := []byte{}
	for {
		n, e := f.Read(buf)
		data := append(carry, buf[:n]...)
		if bytes.Contains(data, []byte(EndpointCapability)) {
			return nil
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if len(data) > len(EndpointCapability) {
			carry = append([]byte(nil), data[len(data)-len(EndpointCapability):]...)
		} else {
			carry = data
		}
	}
	return errors.New("CLI lacks .ssb endpoint support; select the v1.1.0 or newer CLI from the same release as the packager")
}

// HashBinary computes a local payload pin for review. Prepare additionally
// verifies the PE architecture, Go command path and endpoint capability marker.
func HashBinary(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return "", errors.New("expected a regular payload no larger than 2 GiB")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, (2<<30)+1))
	if e != nil {
		return "", e
	}
	if n > 2<<30 {
		return "", errors.New("expected a payload no larger than 2 GiB")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyFile(path, expected string) error {
	actual, err := HashBinary(path)
	if err != nil {
		return err
	}
	if actual != strings.ToLower(expected) {
		return fmt.Errorf("SHA-256 mismatch: %s", path)
	}
	return nil
}
func copyPinned(source, destination, expected string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return errors.New("invalid payload file")
	}
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, (2<<30)+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(expected) {
		return errors.New("payload changed after preview; export refused")
	}
	return nil
}

// CheckContentPrep validates a content-prep request without running anything,
// so a caller can refuse before it exports: the tool must be an existing file,
// and the output must be a folder outside the source bundle so the tool cannot
// package its own result. It returns the absolute source and output paths.
func CheckContentPrep(tool, source, output string) (src, out string, err error) {
	if strings.TrimSpace(output) == "" {
		return "", "", errors.New("select an output folder for the .intunewin package")
	}
	if src, err = filepath.Abs(source); err != nil {
		return "", "", err
	}
	if out, err = filepath.Abs(output); err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(src, out)
	if err != nil {
		return "", "", err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", "", errors.New("content-prep output must be outside the bundle directory")
	}
	if strings.TrimSpace(tool) == "" {
		return "", "", errors.New("select the Microsoft Content Prep tool (" + ContentPrepToolName + ")")
	}
	if info, statErr := os.Stat(tool); statErr != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("content prep tool not found: %s", tool)
	}
	return src, out, nil
}

// PrepareContent invokes an explicitly selected Microsoft tool after export.
// Keep its output outside the source directory so it cannot package itself.
func PrepareContent(ctx context.Context, tool, source, output string) (string, error) {
	src, out, e := CheckContentPrep(tool, source, output)
	if e != nil {
		return "", e
	}
	data, e := exec.CommandContext(ctx, tool, "-c", src, "-s", "install.ps1", "-o", out, "-q").CombinedOutput()
	if e == nil {
		// The tool can exit 0 without producing a package; never report that as success.
		if _, statErr := os.Stat(filepath.Join(out, PreparedPackageName)); statErr != nil {
			e = fmt.Errorf("content prep tool finished but %s was not created in %s", PreparedPackageName, out)
		}
	}
	return string(data), e
}
