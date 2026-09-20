package intune

import (
	"os"
	"path/filepath"
)

// ContentPrepToolName is the file name Microsoft ships its Win32 Content Prep
// Tool under.
const ContentPrepToolName = "IntuneWinAppUtil.exe"

// PreparedPackageName is the file the tool writes into its output folder; it is
// named after the setup file (install.ps1) that PrepareContent passes.
const PreparedPackageName = "install.intunewin"

// FindContentPrepTool returns the first IntuneWinAppUtil.exe found directly in
// one of dirs, or "" if none is there. It only looks beside the given
// directories (typically the running executable); it never searches PATH or
// the rest of the disk, so what gets run is always something the administrator
// put next to SpoolSmith or chose by hand.
func FindContentPrepTool(dirs ...string) string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, ContentPrepToolName)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

// SuggestPrepOutput names a sibling of the export folder for the .intunewin
// file. PrepareContent refuses an output inside the source bundle, so the
// default must be beside it rather than under it.
func SuggestPrepOutput(exportFolder string) string {
	if exportFolder == "" {
		return ""
	}
	return filepath.Clean(exportFolder) + "-intunewin"
}
