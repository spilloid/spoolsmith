package intune

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// ProfileDefaults suggests presentation and identity for a new deployment. The
// queue name alone determines identity: changing its address, driver or profile
// filename must not silently create a different deployment. Existing deployments
// with a custom ID must continue to use that ID.
func ProfileDefaults(path string) (Options, error) {
	p, err := install.LoadProfile(path)
	if err != nil {
		return Options{}, err
	}
	var slug strings.Builder
	for _, r := range strings.ToLower(p.PrinterName) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			slug.WriteRune(r)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
	}
	stem := strings.Trim(slug.String(), "-")
	if len(stem) > 47 {
		stem = strings.TrimRight(stem[:47], "-")
	}
	if stem == "" {
		stem = "printer"
	}
	// Preserve distinctions lost through punctuation, Unicode or truncation.
	id := stem + "-" + digest([]byte(p.PrinterName))[:16]
	return Options{ProfilePath: path, ID: id, Revision: 1,
		DisplayName: p.PrinterName,
		Description: "Printer: " + p.PrinterName + " (" + p.Target + ")"}, nil
}

// SuggestOutput finds an unused child of an existing parent without creating
// anything. Export still exclusively creates that exact reviewed path; a race
// never overwrites a folder or silently changes the reviewed destination.
func SuggestOutput(parent, id string, revision int) (string, error) {
	if !identifier.MatchString(id) || revision < 1 {
		return "", fmt.Errorf("a valid deployment ID and positive revision are required for the export folder")
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("export parent must be a directory: %s", parent)
	}
	base := fmt.Sprintf("%s-r%d", id, revision)
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		path := filepath.Join(parent, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
}
