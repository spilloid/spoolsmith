package catalog

// DriverPackage describes the selected package and non-executable strategy.
// Volatile package metadata intentionally remains separate from family data.
type DriverPackage struct {
	FamilyID          string
	Name              string
	WindowsDriverName string
	Source            string
	Version           string
	SHA256            string
	Strategy          string
}

var drivers = map[string]DriverPackage{
	"hp-laserjet-m4xx": {
		FamilyID: "hp-laserjet-m4xx",
		Name:     "HP Universal Print Driver for Windows PCL 6",
		Source:   "HP Universal Print Driver (PCL 6), listed by HP for the LaserJet Pro M404/M405 series",
		Strategy: "vendor-universal-pcl6",
	},
	"brother-hl-l2xxx": {
		FamilyID: "brother-hl-l2xxx",
		Name:     "Brother model-specific Full Driver & Software Package",
		Source:   "Brother Full Driver & Software Package for the resolved HL-L2xxx model; this is model-specific, not a universal driver",
		Strategy: "vendor-model-specific-full-package",
	},
}

// DriverFor returns the package strategy associated with familyID. The returned
// WindowsDriverName is always empty: a family covers several models and Windows
// registers a separate driver name for each, so the name cannot be a property of
// the family. Resolve binds the per-model name.
func DriverFor(familyID string) (DriverPackage, bool) {
	driver, ok := drivers[familyID]
	return driver, ok
}

// verifiedWindowsDriverNames maps a normalized model to the exact name Windows
// registers for it. An entry may only be added after that name has been read
// from Get-PrinterDriver on real hardware and the evidence recorded in
// docs/real-hardware-verification.md.
//
// This is deliberately not a per-model driver database. It is a register of what
// has actually been proven, and it stays small by construction: a sibling model
// in the same family can never inherit a name verified for a different model,
// because Brother and HP both name drivers per model. A model with no entry
// resolves with an empty WindowsDriverName and every plan built from it fails
// closed.
var verifiedWindowsDriverNames = map[string]string{
	// Verified 2026-09-06 on Windows x64 from the operator-supplied signed
	// Brother package (BROHL13A.INF, staged as oem15.inf), then confirmed by a
	// successful physical test print through the queue it created.
	"Brother HL-L2315D": "Brother HL-L2315D series",
}

// VerifiedWindowsDriverName returns the hardware-confirmed Windows driver name
// for normalizedModel, and reports whether one has been verified at all.
func VerifiedWindowsDriverName(normalizedModel string) (string, bool) {
	name, ok := verifiedWindowsDriverNames[normalizedModel]
	return name, ok
}
