// mock_server.go is a standalone MCP server that returns large JSON payloads.
// Used for testing gcf-proxy's streaming behavior.
//
// Run: go run testdata/mock_server.go
//
// It responds to tools/call requests with a configurable number of symbols.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		response := handleMessage(line)
		if response != "" {
			fmt.Println(response)
		}
	}
}

func handleMessage(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ""
	}

	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return ""
	}

	switch msg.Method {
	case "initialize":
		return makeResponse(msg.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{"tools": map[string]any{}},
			"serverInfo":     map[string]any{"name": "mock-server", "version": "1.0.0"},
		})
	case "tools/list":
		return makeResponse(msg.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "blast_radius",
					"description": "Returns a large graph payload for testing",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				},
			},
		})
	case "tools/call":
		return makeToolResponse(msg.ID, 20) // 20 symbols, 10 edges
	case "notifications/initialized":
		return "" // notification, no response
	default:
		return ""
	}
}

func makeToolResponse(id json.RawMessage, numSymbols int) string {
	symbols := make([]map[string]any, numSymbols)
	for i := 0; i < numSymbols; i++ {
		distance := 0
		if i >= numSymbols/3 {
			distance = 1
		}
		if i >= 2*numSymbols/3 {
			distance = 2
		}
		symbols[i] = map[string]any{
			"qualifiedName": fmt.Sprintf("github.com/org/repo/pkg.Symbol%d", i),
			"kind":          "function",
			"score":         1.0 - float64(i)*0.04,
			"provenance":    "lsp_resolved",
			"distance":      distance,
		}
	}

	edges := make([]map[string]any, numSymbols/2)
	for i := 0; i < numSymbols/2; i++ {
		edges[i] = map[string]any{
			"source":   fmt.Sprintf("github.com/org/repo/pkg.Symbol%d", i+1),
			"target":   fmt.Sprintf("github.com/org/repo/pkg.Symbol%d", i),
			"edgeType": "calls",
		}
	}

	payload := map[string]any{
		"tool":        "blast_radius",
		"tokensUsed":  500,
		"tokenBudget": 5000,
		"symbols":     symbols,
		"edges":       edges,
	}

	payloadJSON, _ := json.Marshal(payload)
	return makeResponse(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(payloadJSON)},
		},
	})
}

func makeResponse(id json.RawMessage, result any) string {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	}
	out, _ := json.Marshal(resp)
	return string(out)
}
