package probe

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestHTTPProbeClientNeverUsesAProxy(t *testing.T) {
	transport, ok := newHTTPProbeClient("192.0.2.10").Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("HTTP probe transport = %#v, want an *http.Transport with no proxy", transport)
	}
}

func TestHTTPProbeClientFollowsOnlySameHostRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<title>Somebody else</title>")
	}))
	defer other.Close()
	otherURL, err := url.Parse(other.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Same listener, different host name: the redirect leaves the target.
	elsewhere := "http://localhost:" + otherURL.Port() + "/"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/home", http.StatusFound)
		case "/home":
			fmt.Fprint(w, "<title>Brother HL</title>")
		case "/away":
			http.Redirect(w, r, elsewhere, http.StatusFound)
		}
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newHTTPProbeClient(targetURL.Hostname())

	response, err := client.Get(target.URL + "/")
	if err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "Brother HL") {
		t.Fatalf("same-host redirect body = %q", body)
	}

	if response, err := client.Get(target.URL + "/away"); err == nil {
		response.Body.Close()
		t.Fatal("redirect to a different host was followed")
	}
}
