package probe

import (
	"strings"
	"testing"
)

func TestExtractHTTPTitle(t *testing.T) {
	got, err := extractHTTPTitle("<!doctype html><HTML><HEAD><TITLE>  Brother HL-L2350DW  </TITLE></HEAD></HTML>")
	if err != nil {
		t.Fatalf("extractHTTPTitle() error = %v", err)
	}
	if got != "Brother HL-L2350DW" {
		t.Fatalf("extractHTTPTitle() = %q, want %q", got, "Brother HL-L2350DW")
	}
}

func TestExtractHTTPTitleRejectsTruncatedTitle(t *testing.T) {
	if _, err := extractHTTPTitle("<html><title>unfinished"); err == nil {
		t.Fatal("extractHTTPTitle() error = nil, want closing-tag error")
	}
}

func TestExtractHTTPTitleKeepsByteOffsetsOnNonUTF8AndCaseFoldingPages(t *testing.T) {
	// strings.ToLower rewrites invalid UTF-8 and U+0130 to a different byte
	// length, so offsets found in a lowered copy used to slice the original
	// out of range and panic.
	for _, document := range []string{
		strings.Repeat("\xe9", 10) + "<title>Brother HL</title>",
		strings.Repeat("İ", 10) + "<TITLE>Brother HL</Title>",
		"<html><title lang=\"en\">Brother HL</title>",
	} {
		got, err := extractHTTPTitle(document)
		if err != nil {
			t.Fatalf("extractHTTPTitle(%q) error = %v", document, err)
		}
		if got != "Brother HL" {
			t.Fatalf("extractHTTPTitle(%q) = %q, want %q", document, got, "Brother HL")
		}
	}
}

func TestExtractHTTPTitleErrors(t *testing.T) {
	for document, want := range map[string]string{
		"<html><body>no title</body>":                    "opening tag not found",
		strings.Repeat("\xe9", 10) + "<title>unfinished": "closing tag not found",
		"<title>   </title>":                             "title is empty",
		"<titlebar>x</titlebar>":                         "opening tag not found",
	} {
		_, err := extractHTTPTitle(document)
		if err == nil || err.Error() != want {
			t.Fatalf("extractHTTPTitle(%q) error = %v, want %q", document, err, want)
		}
	}
}
