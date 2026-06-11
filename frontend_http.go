package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// HTTPFrontend serves MCP over Streamable HTTP, proxying to a stdio or HTTP backend.
type HTTPFrontend struct {
	addr     string
	rewriter *Rewriter
	stats    *Stats
	verbose  bool

	// Backend: either stdio subprocess or HTTP upstream.
	upstreamURL string   // if set, use HTTP backend
	serverCmd   string   // if set, spawn stdio subprocess
	serverArgs  []string

	// Session management.
	sessionCounter atomic.Int64
}

// NewHTTPFrontend creates an HTTP frontend.
func NewHTTPFrontend(addr string, rw *Rewriter, stats *Stats, verbose bool) *HTTPFrontend {
	return &HTTPFrontend{
		addr:     addr,
		rewriter: rw,
		stats:    stats,
		verbose:  verbose,
	}
}

// SetStdioBackend configures the frontend to spawn a subprocess.
func (f *HTTPFrontend) SetStdioBackend(cmd string, args []string) {
	f.serverCmd = cmd
	f.serverArgs = args
}

// SetHTTPBackend configures the frontend to connect to a remote server.
func (f *HTTPFrontend) SetHTTPBackend(url string) {
	f.upstreamURL = url
}

// ListenAndServe starts the HTTP server.
func (f *HTTPFrontend) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.handleMCP)
	mux.HandleFunc("/health", f.handleHealth)

	if f.verbose {
		fmt.Fprintf(os.Stderr, "gcf-proxy: HTTP frontend listening on %s\n", f.addr)
	}
	return http.ListenAndServe(f.addr, mux)
}

func (f *HTTPFrontend) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (f *HTTPFrontend) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	requestLine := strings.TrimSpace(string(body))
	if requestLine == "" {
		http.Error(w, "empty request", http.StatusBadRequest)
		return
	}

	// Decode GCF in tool call arguments (bidirectional).
	requestLine = decodeRequestGCF(requestLine)

	// Determine if this is a request (has id) or notification (no id).
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	json.Unmarshal([]byte(requestLine), &msg)

	// Route to backend.
	var responseLines []string
	if f.upstreamURL != "" {
		responseLines, err = f.sendHTTPBackend(requestLine)
	} else {
		responseLines, err = f.sendStdioBackend(requestLine)
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		errResp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"%s"}}`,
			string(msg.ID), err.Error())
		w.Write([]byte(errResp))
		return
	}

	// Notification: no response expected.
	if msg.ID == nil || string(msg.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Rewrite responses through GCF translator.
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	// Extract metadata from the original request.
	extractRequestMeta(string(body), &tokenMu, tokens, names)

	// Decide: SSE or JSON response.
	if len(responseLines) > 1 || acceptsSSE(r) {
		// SSE response for multiple messages.
		f.writeSSE(w, responseLines, &outputMu, &tokenMu, tokens, names)
	} else if len(responseLines) == 1 {
		// Single JSON response.
		rewritten := rewriteResponse(responseLines[0], f.rewriter, &outputMu, &tokenMu, tokens, names)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rewritten))
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *HTTPFrontend) writeSSE(w http.ResponseWriter, lines []string, outputMu, tokenMu *sync.Mutex, tokens map[string]json.RawMessage, names map[string]string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fallback: write all at once.
		for _, line := range lines {
			rewritten := rewriteResponse(line, f.rewriter, outputMu, tokenMu, tokens, names)
			fmt.Fprintf(w, "data: %s\n\n", rewritten)
		}
		return
	}

	for _, line := range lines {
		rewritten := rewriteResponse(line, f.rewriter, outputMu, tokenMu, tokens, names)
		fmt.Fprintf(w, "data: %s\n\n", rewritten)
		flusher.Flush()
	}
}

func acceptsSSE(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream")
}

// sendHTTPBackend forwards the request to a remote MCP server.
func (f *HTTPFrontend) sendHTTPBackend(requestLine string) ([]string, error) {
	backend := NewHTTPBackend(f.upstreamURL)
	return backend.Send(requestLine)
}

// sendStdioBackend forwards the request to a stdio subprocess.
// Each request spawns a fresh interaction with the subprocess.
// For persistent connections, the subprocess stays alive across requests.
func (f *HTTPFrontend) sendStdioBackend(requestLine string) ([]string, error) {
	// For simplicity, use a per-request subprocess.
	// A production implementation would maintain a persistent subprocess pool.
	cmd := exec.Command(f.serverCmd, f.serverArgs...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Send request.
	stdin.Write([]byte(requestLine + "\n"))
	stdin.Close()

	// Read responses.
	var lines []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && json.Valid([]byte(line)) {
			lines = append(lines, line)
		}
	}

	cmd.Wait()
	return lines, nil
}
