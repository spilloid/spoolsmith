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
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > 2 {
				return fmt.Errorf("more than two redirects")
			}
			return nil
		},
	}
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
