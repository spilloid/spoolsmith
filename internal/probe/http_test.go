package probe

import "testing"

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
