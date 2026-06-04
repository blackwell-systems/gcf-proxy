// gcf-proxy is a transparent MCP proxy that re-encodes JSON tool responses as GCF.
//
// Usage:
//
//	gcf-proxy your-mcp-server [args...]
//
// It spawns the given MCP server as a subprocess, proxies stdin/stdout,
// and rewrites JSON content blocks in tool responses to GCF format.
// Zero changes required to the underlying server.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	gcf "github.com/blackwell-systems/gcf-go"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		fmt.Fprintf(os.Stderr, `gcf-proxy - MCP proxy that re-encodes JSON tool responses as GCF

Usage:
  gcf-proxy <mcp-server-command> [args...]

Example:
  gcf-proxy memory-mcp-server-go
  gcf-proxy npx -y @modelcontextprotocol/server-filesystem /tmp

MCP config (before):
  {"mcpServers": {"memory": {"command": "memory-mcp-server-go"}}}

MCP config (after):
  {"mcpServers": {"memory": {"command": "gcf-proxy", "args": ["memory-mcp-server-go"]}}}

Version: %s
`, version)
		if len(os.Args) < 2 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	serverCmd := os.Args[1]
	serverArgs := os.Args[2:]

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

	// Proxy client stdin -> server stdin
	go func() {
		io.Copy(serverStdin, os.Stdin)
		serverStdin.Close()
	}()

	// Proxy server stdout -> client stdout (with GCF rewriting)
	scanner := bufio.NewScanner(serverStdout)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Text()
		rewritten := rewriteLine(line)
		fmt.Println(rewritten)
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

// rewriteLine attempts to parse a JSON-RPC response and rewrite tool result
// content blocks from JSON to GCF.
func rewriteLine(line string) string {
	// Quick check: is this even JSON?
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line
	}

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return line
	}

	// Only process JSON-RPC responses (have "result" field)
	resultRaw, hasResult := msg["result"]
	if !hasResult {
		return line
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return line
	}

	// Look for content array in the result
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

		// Try to parse as JSON payload with "tool" and "symbols" fields
		converted := tryConvertToGCF(text)
		if converted != "" {
			content[i]["text"] = converted
			modified = true
		}
	}

	if !modified {
		return line
	}

	// Rebuild the response
	contentBytes, _ := json.Marshal(content)
	result["content"] = contentBytes
	resultBytes, _ := json.Marshal(result)
	msg["result"] = resultBytes
	output, _ := json.Marshal(msg)
	return string(output)
}

// tryConvertToGCF attempts to parse text as a JSON payload with GCF-compatible
// structure and convert it. Returns empty string if not convertible.
func tryConvertToGCF(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ""
	}

	var payload struct {
		Tool        string `json:"tool"`
		TokensUsed  int    `json:"tokensUsed"`
		TokenBudget int    `json:"tokenBudget"`
		PackRoot    string `json:"packRoot"`
		Symbols     []struct {
			QualifiedName string  `json:"qualifiedName"`
			Kind          string  `json:"kind"`
			Score         float64 `json:"score"`
			Provenance    string  `json:"provenance"`
			Distance      int     `json:"distance"`
		} `json:"symbols"`
		Edges []struct {
			Source   string `json:"source"`
			Target   string `json:"target"`
			EdgeType string `json:"edgeType"`
			Status   string `json:"status"`
		} `json:"edges"`
	}

	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}

	// Graph profile: payload has tool + symbols
	if payload.Tool != "" && payload.Symbols != nil {
		p := &gcf.Payload{
			Tool:        payload.Tool,
			TokensUsed:  payload.TokensUsed,
			TokenBudget: payload.TokenBudget,
			PackRoot:    payload.PackRoot,
		}

		for _, s := range payload.Symbols {
			p.Symbols = append(p.Symbols, gcf.Symbol{
				QualifiedName: s.QualifiedName,
				Kind:          s.Kind,
				Score:         s.Score,
				Provenance:    s.Provenance,
				Distance:      s.Distance,
			})
		}

		for _, e := range payload.Edges {
			p.Edges = append(p.Edges, gcf.Edge{
				Source:   e.Source,
				Target:   e.Target,
				EdgeType: e.EdgeType,
				Status:   e.Status,
			})
		}

		return gcf.Encode(p)
	}

	// Tabular profile: any structured JSON
	var generic any
	if err := json.Unmarshal([]byte(trimmed), &generic); err != nil {
		return ""
	}
	result := gcf.EncodeGeneric(generic)
	if result == "" {
		return ""
	}
	return result
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "gcf-proxy: "+format+"\n", args...)
	os.Exit(1)
}
