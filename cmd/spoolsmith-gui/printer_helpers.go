package main

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

func discoveryNetwork(input string) (string, error) {
	input = strings.TrimSpace(input)
	if ip, err := netip.ParseAddr(input); err == nil && ip.Is4() {
		return ip.String() + "/32", nil
	}
	prefix, err := netip.ParsePrefix(input)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 24 {
		return "", fmt.Errorf("Enter an IPv4 address, such as 192.168.1.25, or a network, such as 192.168.1.0/24. Network sizes /24 through /32 are supported.")
	}
	return prefix.Masked().String(), nil
}

func printerIdentity(e evidence.Evidence) string {
	for _, value := range []string{e.HTTPModelString, e.HTTPTitle, e.PJLID, e.SNMPSysDescr, e.Hostname} {
		if text := strings.Join(strings.Fields(value), " "); text != "" {
			return text
		}
	}
	return "Printer " + e.IP
}

func suggestedProfileFile(dir, target string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "profiles"
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(target))
	if err != nil {
		return ""
	}
	base := "printer-" + strings.NewReplacer(".", "-", ":", "-", "%", "-").Replace(ip.String())
	for suffix := 0; ; suffix++ {
		name := base
		if suffix > 0 {
			name += fmt.Sprintf("-%d", suffix+1)
		}
		path := filepath.Join(dir, name+".json")
		if _, err := os.Stat(path); err != nil {
			return path
		}
	}
}

func profileSummary(p install.Profile, path string) string {
	text := fmt.Sprintf("%s\r\n\r\nIP address: %s\r\nDriver: %s\r\nDetected printer: %s\r\nSaved file: %s", p.PrinterName, p.Target, p.DriverName, printerIdentity(p.Evidence), path)
	if p.DriverPackage != nil {
		text += "\r\nDriver package: " + p.DriverPackage.ID
	}
	return text + "\r\n\r\nChoose an action to review its changes before applying them."
}

func sameProfilePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func defaultProfileDirectory(executable string) string {
	dir := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(dir), "dist") {
		parent := filepath.Dir(dir)
		if _, err := os.Stat(filepath.Join(parent, "go.mod")); err == nil {
			return filepath.Join(parent, "profiles")
		}
		if info, err := os.Stat(filepath.Join(parent, "profiles")); err == nil && info.IsDir() {
			return filepath.Join(parent, "profiles")
		}
	}
	return filepath.Join(dir, "profiles")
}

// Only a valid profile with an exact address match can supply saved settings.
func savedProfilesForIP(dir, target string) []string {
	ip, err := netip.ParseAddr(strings.TrimSpace(target))
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		p, err := install.LoadProfile(path)
		if err != nil {
			continue
		}
		savedIP, err := netip.ParseAddr(p.Target)
		if err == nil && savedIP.Unmap() == ip.Unmap() {
			matches = append(matches, path)
		}
	}
	return matches
}

// Words that appear in almost every printer and driver name carry no evidence
// about which model this is, so they must not create a match on their own.
var genericIdentityWords = map[string]bool{
	"PRINTER": true, "PRINT": true, "SERIES": true, "LASER": true, "LASERJET": true,
	"INKJET": true, "COLOR": true, "COLOUR": true, "MONO": true, "PRO": true,
	"UNIVERSAL": true, "DRIVER": true, "CLASS": true, "PCL": true, "PS": true,
	"POSTSCRIPT": true, "WINDOWS": true, "MFP": true, "NETWORK": true, "THE": true,
	"AND": true, "FOR": true, "V4": true, "V3": true,
}

// identityWords reduces what a printer reported about itself to the distinctive
// uppercase words a driver name could plausibly share with it.
func identityWords(e evidence.Evidence) []string {
	seen := map[string]bool{}
	var words []string
	for _, source := range []string{e.HTTPModelString, e.PJLID, e.HTTPTitle, e.SNMPSysDescr} {
		for _, word := range strings.Fields(normalizedIdentity(source)) {
			if len(word) < 2 || genericIdentityWords[word] || seen[word] {
				continue
			}
			seen[word] = true
			words = append(words, word)
		}
	}
	return words
}

func normalizedIdentity(value string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToUpper(value) {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func hasWord(haystack, word string) bool {
	return strings.Contains(" "+haystack+" ", " "+word+" ")
}

func hasDigit(word string) bool {
	return strings.ContainsAny(word, "0123456789")
}

// suggestDriver offers a starting point only: it requires the printer's own
// reported model words, including a model-number-like word, to appear in the
// driver name, and declines when two drivers match equally well. The operator
// confirms the choice, and install-time preflight verifies the driver exists.
func suggestDriver(names []string, e evidence.Evidence) string {
	words := identityWords(e)
	if len(words) == 0 {
		return ""
	}
	best, bestScore, tied := "", 0, false
	for _, candidate := range names {
		normalized := normalizedIdentity(candidate)
		matched, model := 0, false
		for _, word := range words {
			if hasWord(normalized, word) {
				matched++
				model = model || hasDigit(word)
			}
		}
		if matched < 2 || !model {
			continue
		}
		switch {
		case matched > bestScore:
			best, bestScore, tied = candidate, matched, false
		case matched == bestScore && !strings.EqualFold(candidate, best):
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
