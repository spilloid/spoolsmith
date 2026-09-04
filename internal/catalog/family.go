package catalog

// Family is a stable identity grouping for models that share a driver
// selection strategy.
type Family struct {
	ID           string
	Manufacturer string
	Aliases      []string
}

var families = []Family{
	{
		ID:           "hp-laserjet-m4xx",
		Manufacturer: "HP",
		Aliases: []string{
			"HP LaserJet Pro M404",
			"HP LaserJet Pro M404d",
			"HP LaserJet Pro M404dn",
			"HP LaserJet Pro M404dw",
			"HP LaserJet Pro M404n",
			"HP LaserJet Pro M404m",
			"HP LaserJet Pro M405",
			"HP LaserJet Pro M405dn",
			"HP LaserJet Pro M405dw",
			"HP LaserJet Pro M405n",
		},
	},
	{
		ID:           "brother-hl-l2xxx",
		Manufacturer: "Brother",
		Aliases: []string{
			"Brother HL-L2325DW",
			"Brother HL-L2350DW",
			"Brother HL-L2370DW",
			"Brother HL-L2370DWXL",
			"HL-L2325DW",
			"HL-L2350DW",
			"HL-L2370DW",
			"HL-L2370DWXL",
		},
	},
}

// Families returns the fixed, hand-written registry for this milestone.
// Returned values are copies so callers cannot mutate the registry.
func Families() []Family {
	result := make([]Family, len(families))
	for i, family := range families {
		result[i] = family
		result[i].Aliases = append([]string(nil), family.Aliases...)
	}
	return result
}
