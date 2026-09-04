package catalog

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

// ResolutionResult is the deterministic result of mapping evidence through
// normalized model and family identity to driver metadata.
type ResolutionResult struct {
	NormalizedModel string
	Family          *Family
	Driver          *DriverPackage
	Confidence      float64
	Uncertain       []string
}

type modelMatch struct {
	family Family
	model  string
}

type manufacturerName struct {
	name    string
	aliases []string
}

var knownManufacturers = []manufacturerName{
	{name: "HP", aliases: []string{"HP", "HEWLETT PACKARD"}},
	{name: "Brother", aliases: []string{"BROTHER"}},
	{name: "Canon", aliases: []string{"CANON"}},
	{name: "Epson", aliases: []string{"EPSON"}},
	{name: "Lexmark", aliases: []string{"LEXMARK"}},
	{name: "Xerox", aliases: []string{"XEROX"}},
	{name: "Kyocera", aliases: []string{"KYOCERA"}},
	{name: "Ricoh", aliases: []string{"RICOH"}},
	{name: "Konica Minolta", aliases: []string{"KONICA MINOLTA"}},
	{name: "Dell", aliases: []string{"DELL"}},
	{name: "Samsung", aliases: []string{"SAMSUNG"}},
	{name: "OKI", aliases: []string{"OKI"}},
	{name: "Sharp", aliases: []string{"SHARP"}},
}

var pjlManufacturerPattern = regexp.MustCompile(`(?i)(?:^|[;\r\n])\s*MFG\s*:\s*([^;\r\n]+)`)

// Resolve is pure: it performs no network or filesystem I/O.
func Resolve(e evidence.Evidence) ResolutionResult {
	matches := make([]modelMatch, 0)
	for _, value := range identityValues(e) {
		matches = append(matches, matchIdentity(value)...)
	}

	if len(matches) == 0 {
		return unresolved("no fixture-backed printer model matched the observed identifiers")
	}

	familyIDs := make(map[string]struct{})
	models := make(map[string]struct{})
	for _, match := range matches {
		familyIDs[match.family.ID] = struct{}{}
		models[match.model] = struct{}{}
	}
	if len(familyIDs) != 1 {
		return unresolved("observed identifiers conflict across supported printer families")
	}
	if len(models) != 1 {
		return unresolved("observed identifiers conflict across printer models")
	}

	match := matches[0]
	if vendor := normalizedWords(e.MACVendor); vendor != "" && !vendorMatches(vendor, match.family.Manufacturer) {
		return unresolved(fmt.Sprintf("MAC vendor %q conflicts with matched manufacturer %q", e.MACVendor, match.family.Manufacturer))
	}
	if manufacturer := pjlManufacturer(e.PJLID); manufacturer != "" && !manufacturerMatches(manufacturer, match.family.Manufacturer) {
		return unresolved(fmt.Sprintf("PJL manufacturer %q conflicts with matched manufacturer %q", manufacturer, match.family.Manufacturer))
	}
	for _, observed := range []struct {
		name  string
		value string
	}{
		{name: "SNMP sysDescr", value: e.SNMPSysDescr},
		{name: "HTTP title", value: e.HTTPTitle},
		{name: "HTTP model string", value: e.HTTPModelString},
	} {
		if observed.value != "" && !manufacturerMatches(observed.value, match.family.Manufacturer) {
			return unresolved(fmt.Sprintf("%s %q conflicts with matched manufacturer %q", observed.name, observed.value, match.family.Manufacturer))
		}
	}

	driver, ok := DriverFor(match.family.ID)
	if !ok {
		return unresolved(fmt.Sprintf("printer family %q has no mapped driver package", match.family.ID))
	}

	family := match.family
	return ResolutionResult{
		NormalizedModel: match.model,
		Family:          &family,
		Driver:          &driver,
		Confidence:      1.0,
		Uncertain:       []string{},
	}
}

func unresolved(reason string) ResolutionResult {
	return ResolutionResult{
		Confidence: 0,
		Uncertain:  []string{reason},
	}
}

func identityValues(e evidence.Evidence) []string {
	return []string{
		e.SNMPSysDescr,
		e.SNMPSysObjectID,
		e.HTTPTitle,
		e.HTTPModelString,
		e.PJLID,
		e.Hostname,
	}
}

func matchIdentity(value string) []modelMatch {
	value = normalizedWords(value)
	if value == "" {
		return nil
	}

	var matches []modelMatch
	seen := make(map[string]struct{})
	for _, family := range Families() {
		for _, alias := range family.Aliases {
			normalizedAlias := normalizedWords(alias)
			if containsWords(value, normalizedAlias) {
				model := alias
				if family.Manufacturer == "Brother" && !strings.HasPrefix(model, "Brother ") {
					model = "Brother " + model
				}
				key := family.ID + "\x00" + model
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, modelMatch{family: family, model: model})
			}
		}
	}
	return matches
}

func normalizedWords(value string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToUpper(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func containsWords(value, candidate string) bool {
	return strings.Contains(" "+value+" ", " "+candidate+" ")
}

func vendorMatches(vendor, manufacturer string) bool {
	mentions := manufacturerMentions(vendor)
	return len(mentions) == 1 && mentions[0] == manufacturer
}

func manufacturerMatches(value, manufacturer string) bool {
	mentions := manufacturerMentions(normalizedWords(value))
	return len(mentions) == 0 || len(mentions) == 1 && mentions[0] == manufacturer
}

func manufacturerMentions(value string) []string {
	var mentions []string
	for _, manufacturer := range knownManufacturers {
		for _, alias := range manufacturer.aliases {
			if containsWords(value, alias) {
				mentions = append(mentions, manufacturer.name)
				break
			}
		}
	}
	return mentions
}

func pjlManufacturer(value string) string {
	match := pjlManufacturerPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
