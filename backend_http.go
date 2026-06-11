package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// HTTPBackend connects to a remote MCP server over Streamable HTTP.
type HTTPBackend struct {
	url       string
	sessionID string
	client    *http.Client
	mu        sync.Mutex
}

// NewHTTPBackend creates a backend that talks to a remote MCP server.
func NewHTTPBackend(url string) *HTTPBackend {
	return &HTTPBackend{
		url:    strings.TrimRight(url, "/"),
		client: &http.Client{},
	}
}

// Send posts a JSON-RPC message to the upstream server and returns response lines.
// The upstream may respond with a single JSON-RPC response or an SSE stream.
func (b *HTTPBackend) Send(requestLine string) ([]string, error) {
	req, err := http.NewRequest("POST", b.url, bytes.NewBufferString(requestLine))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	b.mu.Lock()
	if b.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", b.sessionID)
	}
	b.mu.Unlock()

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID from response.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		b.mu.Lock()
		b.sessionID = sid
		b.mu.Unlock()
	}

	contentType := resp.Header.Get("Content-Type")

	// SSE response: parse event stream.
	if strings.HasPrefix(contentType, "text/event-stream") {
		return b.parseSSE(resp.Body)
	}

	// JSON response: read body as a single line.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}

	// Could be multiple JSON-RPC messages (one per line).
	var lines []string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// parseSSE reads a Server-Sent Events stream and extracts JSON-RPC messages
// from "data:" fields. Each complete event (terminated by blank line) produces
// one response line.
func (b *HTTPBackend) parseSSE(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)

	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
			continue
		}

		// Blank line = end of event.
		if line == "" && dataBuf.Len() > 0 {
			data := strings.TrimSpace(dataBuf.String())
			dataBuf.Reset()

			if data == "" {
				continue
			}

			// Validate it's JSON before passing through.
			if json.Valid([]byte(data)) {
				lines = append(lines, data)
			}
			continue
		}

		// Skip event:, id:, retry: fields.
	}

	// Handle final event without trailing blank line.
	if dataBuf.Len() > 0 {
		data := strings.TrimSpace(dataBuf.String())
		if data != "" && json.Valid([]byte(data)) {
			lines = append(lines, data)
		}
	}

	return lines, scanner.Err()
}
