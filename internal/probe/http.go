package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxHTTPBody = 64 << 10

func probeHTTP(ctx context.Context, host string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client := newHTTPProbeClient(host)
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+net.JoinHostPort(host, "80")+"/", nil)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("HTTP GET: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPBody))
	if err != nil {
		return "", fmt.Errorf("HTTP body: %w", err)
	}
	title, err := extractHTTPTitle(string(body))
	if err != nil {
		return "", fmt.Errorf("HTTP title: %w", err)
	}
	return title, nil
}

// titleOpen and titleClose match case-insensitively against the original
// bytes; lowering a copy first changes byte lengths on invalid UTF-8 and
// characters such as U+0130, so its offsets cannot index the original.
var (
	titleOpen  = regexp.MustCompile(`(?i)<title(?:\s[^>]*)?>`)
	titleClose = regexp.MustCompile(`(?i)</title\s*>`)
)

// newHTTPProbeClient talks to the printer directly, never through a proxy
// from the environment, and follows a redirect only while it stays on the
// target host: a title served by some other host is not this printer's.
func newHTTPProbeClient(host string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 2 {
				return fmt.Errorf("more than two redirects")
			}
			if !strings.EqualFold(request.URL.Hostname(), host) {
				return fmt.Errorf("redirect leaves %s for %s", host, request.URL.Hostname())
			}
			return nil
		},
	}
}

func extractHTTPTitle(document string) (string, error) {
	open := titleOpen.FindStringIndex(document)
	if open == nil {
		return "", fmt.Errorf("opening tag not found")
	}
	start := open[1]
	end := titleClose.FindStringIndex(document[start:])
	if end == nil {
		return "", fmt.Errorf("closing tag not found")
	}
	title := strings.TrimSpace(document[start : start+end[0]])
	if title == "" {
		return "", fmt.Errorf("title is empty")
	}
	return title, nil
}
