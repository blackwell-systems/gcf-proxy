// gcf-proxy is a streaming MCP proxy that re-encodes JSON tool responses as GCF.
//
// Usage:
//
//	gcf-proxy your-mcp-server [args...]
//
// It spawns the given MCP server as a subprocess, proxies stdin/stdout,
// and rewrites JSON content blocks in tool responses to GCF format.
// When a progressToken is present, it streams GCF fragments via progress
// notifications for immediate partial context delivery.
//
// Zero changes required to the underlying server.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		fmt.Fprintf(os.Stderr, `gcf-proxy - streaming MCP proxy that re-encodes JSON tool responses as GCF

Usage:
  gcf-proxy [flags] <mcp-server-command> [args...]

Flags:
  --stream-threshold N   Min symbols before streaming mode activates (default: 5)
  --no-progress          Disable progress notifications
  --verbose              Log per-call savings to stderr

Example:
  gcf-proxy memory-mcp-server-go
  gcf-proxy npx -y @modelcontextprotocol/server-filesystem /tmp
  gcf-proxy --stream-threshold 10 knowing

MCP config (before):
  {"mcpServers": {"memory": {"command": "memory-mcp-server-go"}}}

MCP config (after):
  {"mcpServers": {"memory": {"command": "gcf-proxy", "args": ["memory-mcp-server-go"]}}}

Features:
  - Re-encodes JSON tool responses as GCF (79%% fewer tokens)
  - Streams GCF fragments via progress notifications (immediate partial context)
  - Zero changes to the upstream server

Version: %s
`, version)
		if len(os.Args) < 2 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Parse flags.
	streamThreshold := 5
	enableProgress := true
	verbose := false
	args := os.Args[1:]

	for len(args) > 0 {
		switch {
		case args[0] == "--stream-threshold" && len(args) > 1:
			if n, err := strconv.Atoi(args[1]); err == nil {
				streamThreshold = n
			}
			args = args[2:]
		case args[0] == "--no-progress":
			enableProgress = false
			args = args[1:]
		case args[0] == "--verbose":
			verbose = true
			args = args[1:]
		default:
			goto done
		}
	}
done:

	if len(args) == 0 {
		fatal("no server command specified")
	}

	serverCmd := args[0]
	serverArgs := args[1:]

	stats := &Stats{}
	rewriter := NewRewriter(RewriterConfig{
		StreamThreshold: streamThreshold,
		EnableProgress:  enableProgress,
		Stats:           stats,
		Verbose:         verbose,
	})

	cmd := exec.Command(serverCmd, serverArgs...)
	cmd.Stderr = os.Stderr

	serverStdin, err := cmd.StdinPipe()
	if err != nil {
		fatal("failed to create stdin pipe: %v", err)
	}

	serverStdout, err := cmd.StdoutPipe()
	if err != nil {
		fatal("failed to create stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		fatal("failed to start server: %v", err)
	}

	// Print stats on shutdown signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		stats.WriteSummary(os.Stderr)
		cmd.Process.Signal(syscall.SIGTERM)
	}()

	// Output mutex: ensures progress notifications and responses don't interleave.
	var outputMu sync.Mutex

	// Track active progress tokens and tool names from tool call requests.
	var tokenMu sync.Mutex
	activeTokens := make(map[string]json.RawMessage) // request ID -> progressToken
	toolNames := make(map[string]string)              // request ID -> tool name

	// Proxy client stdin -> server stdin, capturing progress tokens from requests.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()

			// Try to extract progressToken and tool name from tool call requests.
			extractRequestMeta(line, &tokenMu, activeTokens, toolNames)

			serverStdin.Write([]byte(line))
			serverStdin.Write([]byte("\n"))
		}
		serverStdin.Close()
	}()

	// Proxy server stdout -> client stdout (with GCF rewriting + progress).
	scanner := bufio.NewScanner(serverStdout)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		rewritten := rewriteResponse(line, rewriter, &outputMu, &tokenMu, activeTokens, toolNames)
		outputMu.Lock()
		fmt.Println(rewritten)
		outputMu.Unlock()
	}

	if err := cmd.Wait(); err != nil {
		stats.WriteSummary(os.Stderr)
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	stats.WriteSummary(os.Stderr)
}

// extractRequestMeta looks for tools/call requests and caches their progressToken and tool name.
func extractRequestMeta(line string, mu *sync.Mutex, tokens map[string]json.RawMessage, names map[string]string) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return
	}

	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
			Meta struct {
				ProgressToken json.RawMessage `json:"progressToken"`
			} `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return
	}
	if msg.Method != "tools/call" || msg.ID == nil {
		return
	}

	mu.Lock()
	if msg.Params.Name != "" {
		names[string(msg.ID)] = msg.Params.Name
	}
	if msg.Params.Meta.ProgressToken != nil && string(msg.Params.Meta.ProgressToken) != "null" {
		tokens[string(msg.ID)] = msg.Params.Meta.ProgressToken
	}
	mu.Unlock()
}

// rewriteResponse processes a JSON-RPC response line, converting tool result
// content to GCF and optionally emitting progress notifications.
func rewriteResponse(line string, rw *Rewriter, outputMu *sync.Mutex, tokenMu *sync.Mutex, tokens map[string]json.RawMessage, names map[string]string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line
	}

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return line
	}

	// Only process JSON-RPC responses (have "result" and "id" fields).
	resultRaw, hasResult := msg["result"]
	idRaw, hasID := msg["id"]
	if !hasResult || !hasID {
		return line
	}

	// Look up progressToken and tool name for this response's request ID.
	var progressToken json.RawMessage
	var toolName string
	tokenMu.Lock()
	if tok, ok := tokens[string(idRaw)]; ok {
		progressToken = tok
		delete(tokens, string(idRaw))
	}
	if name, ok := names[string(idRaw)]; ok {
		toolName = name
		delete(names, string(idRaw))
	}
	tokenMu.Unlock()

	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return line
	}

	contentRaw, hasContent := result["content"]
	if !hasContent {
		return line
	}

	var content []map[string]interface{}
	if err := json.Unmarshal(contentRaw, &content); err != nil {
		return line
	}

	modified := false
	for i, block := range content {
		typ, _ := block["type"].(string)
		if typ != "text" {
			continue
		}

		text, _ := block["text"].(string)
		if text == "" {
			continue
		}

		// Build progressFn if we have a token.
		var progressFn ProgressFunc
		if progressToken != nil {
			progressFn = func(fragment string, progress, total int) {
				notif, err := makeProgressNotification(progressToken, progress, total, fragment)
				if err != nil {
					return
				}
				outputMu.Lock()
				fmt.Println(string(notif))
				outputMu.Unlock()
			}
		}

		res := rw.RewriteToolResult(text, progressFn)
		if res.Converted {
			content[i]["text"] = res.Rewritten
			modified = true
			if rw.config.Verbose && toolName != "" {
				jsonSize := len(text)
				gcfSize := len(res.Rewritten)
				saved := jsonSize - gcfSize
				pct := float64(saved) / float64(jsonSize) * 100
				fmt.Fprintf(os.Stderr, "gcf-proxy: %-30s %s -> %s (%.0f%% saved)\n",
					toolName, fmtBytes(int64(jsonSize)), fmtBytes(int64(gcfSize)), pct)
			}
		}
	}

	if !modified {
		return line
	}

	// Rebuild the response.
	contentBytes, _ := json.Marshal(content)
	result["content"] = contentBytes
	resultBytes, _ := json.Marshal(result)
	msg["result"] = resultBytes
	output, _ := json.Marshal(msg)
	return string(output)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "gcf-proxy: "+format+"\n", args...)
	os.Exit(1)
}

// Ensure io is used (needed for stdin copy goroutine).
var _ = io.Discard
