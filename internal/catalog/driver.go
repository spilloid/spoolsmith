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

// DriverFor returns the package strategy associated with familyID.
func DriverFor(familyID string) (DriverPackage, bool) {
	driver, ok := drivers[familyID]
	return driver, ok
}
