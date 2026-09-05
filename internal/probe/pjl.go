package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	pjlRequest     = "\x1b%-12345X@PJL INFO ID\r\n\x1b%-12345X"
	maxPJLResponse = 4096
)

func probePJL(ctx context.Context, host string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", net.JoinHostPort(host, "9100"))
	if err != nil {
		return "", fmt.Errorf("PJL connect: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if parentDeadline, ok := probeCtx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("PJL deadline: %w", err)
	}
	if _, err := io.WriteString(conn, pjlRequest); err != nil {
		return "", fmt.Errorf("PJL write: %w", err)
	}

	response := make([]byte, 0, 512)
	buffer := make([]byte, 512)
	for len(response) < maxPJLResponse {
		n, readErr := conn.Read(buffer)
		response = append(response, buffer[:n]...)
		if bytes.Contains(response, []byte{'\f'}) {
			break
		}
		if readErr != nil {
			if readErr != io.EOF {
				return "", fmt.Errorf("PJL read: %w", readErr)
			}
			break
		}
	}
	return parsePJLInfoID(response)
}

func parsePJLInfoID(response []byte) (string, error) {
	const prefix = "@PJL INFO ID\r\n\""
	start := bytes.Index(response, []byte(prefix))
	if start < 0 {
		return "", fmt.Errorf("PJL INFO ID header not found")
	}
	modelStart := start + len(prefix)
	end := bytes.Index(response[modelStart:], []byte("\"\r\n\f"))
	if end < 0 {
		return "", fmt.Errorf("PJL INFO ID response is malformed or truncated")
	}
	model := strings.TrimSpace(string(response[modelStart : modelStart+end]))
	if model == "" {
		return "", fmt.Errorf("PJL INFO ID model is empty")
	}
	return model, nil
}
