package main

import (
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

// The driver suggestion is a convenience over an operator-selected value, so
// the cases that matter most are the ones where it must decline: a guess that
// looks confident but is wrong is worse than leaving the field empty.
func TestSuggestDriver(t *testing.T) {
	installed := []string{
		"Brother HL-L2315D series",
		"Brother HL-L2340D series",
		"HP Universal Printing PCL 6",
		"Microsoft Print To PDF",
		"Microsoft XPS Document Writer",
	}
	tests := []struct {
		name     string
		names    []string
		evidence evidence.Evidence
		want     string
	}{
		{
			name:     "exact model reported by the printer",
			names:    installed,
			evidence: evidence.Evidence{HTTPModelString: "Brother HL-L2315D series"},
			want:     "Brother HL-L2315D series",
		},
		{
			name:     "model recovered from a PJL identity string",
			names:    installed,
			evidence: evidence.Evidence{PJLID: `"BROTHER HL-L2340D series"`},
			want:     "Brother HL-L2340D series",
		},
		{
			name:     "manufacturer alone is not enough to choose a model",
			names:    installed,
			evidence: evidence.Evidence{HTTPTitle: "Brother printer"},
			want:     "",
		},
		{
			name:     "a model with no matching driver is not forced onto one",
			names:    installed,
			evidence: evidence.Evidence{HTTPModelString: "Brother HL-L9999ZZ series"},
			want:     "",
		},
		{
			name:     "generic words shared by every driver never match",
			names:    installed,
			evidence: evidence.Evidence{HTTPTitle: "Laser Printer Series"},
			want:     "",
		},
		{
			name:     "no reported identity yields no suggestion",
			names:    installed,
			evidence: evidence.Evidence{IP: "192.168.1.50"},
			want:     "",
		},
		{
			name:     "no installed drivers yields no suggestion",
			names:    nil,
			evidence: evidence.Evidence{HTTPModelString: "Brother HL-L2315D series"},
			want:     "",
		},
		{
			name:  "equally good matches are left for the operator to choose",
			names: []string{"Brother HL-L2315D series", "Brother HL-L2315D series v4"},
			// Both drivers contain every reported word, so neither is a
			// better answer than the other and the field stays empty.
			evidence: evidence.Evidence{HTTPModelString: "Brother HL-L2315D"},
			want:     "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := suggestDriver(test.names, test.evidence); got != test.want {
				t.Errorf("suggestDriver() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIdentityWordsDropsGenericTerms(t *testing.T) {
	words := identityWords(evidence.Evidence{HTTPModelString: "Brother HL-L2315D series printer"})
	for _, unwanted := range []string{"SERIES", "PRINTER"} {
		for _, word := range words {
			if word == unwanted {
				t.Errorf("identityWords() kept generic word %q: %v", unwanted, words)
			}
		}
	}
	var found bool
	for _, word := range words {
		if word == "L2315D" {
			found = true
		}
	}
	if !found {
		t.Errorf("identityWords() dropped the model word: %v", words)
	}
}
