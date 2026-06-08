package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	gcf "github.com/blackwell-systems/gcf-go"
)

func TestRewriter_BasicConversion(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5, EnableProgress: true})

	payload := `{"tool":"test","tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"pkg.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`

	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if !strings.Contains(result.Rewritten, "GCF tool=test") {
		t.Errorf("expected GCF header, got:\n%s", result.Rewritten)
	}
	if !strings.Contains(result.Rewritten, "@0 fn pkg.A 0.90 lsp") {
		t.Errorf("expected symbol line, got:\n%s", result.Rewritten)
	}
}

func TestRewriter_StreamingProgress(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 3, EnableProgress: true})

	// Build a payload with 9 symbols (3 batches of 3).
	symbols := make([]map[string]any, 9)
	for i := 0; i < 9; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": "pkg.S" + string(rune('A'+i)),
			"kind":          "function",
			"score":         0.9 - float64(i)*0.05,
			"provenance":    "lsp",
			"distance":      0,
		}
	}
	payload := map[string]any{
		"tool":        "test",
		"tokensUsed":  100,
		"tokenBudget": 1000,
		"symbols":     symbols,
		"edges":       []any{},
	}
	payloadJSON, _ := json.Marshal(payload)

	var progressCalls []struct {
		fragment string
		progress int
		total    int
	}

	progressFn := func(fragment string, progress, total int) {
		progressCalls = append(progressCalls, struct {
			fragment string
			progress int
			total    int
		}{fragment, progress, total})
	}

	result := rw.RewriteToolResult(string(payloadJSON), progressFn)
	if !result.Converted {
		t.Fatal("expected conversion")
	}

	// Should have at least 3 progress calls (9 symbols / 3 batch size = 3, plus final).
	if len(progressCalls) < 3 {
		t.Errorf("expected at least 3 progress calls, got %d", len(progressCalls))
	}

	// Each fragment should contain GCF lines.
	for i, call := range progressCalls {
		if call.fragment == "" {
			t.Errorf("progress call %d has empty fragment", i)
		}
		if call.total != 9 {
			t.Errorf("progress call %d has total=%d, want 9", i, call.total)
		}
	}

	// Final output should decode correctly.
	decoded, err := gcf.Decode(result.Rewritten)
	if err != nil {
		t.Fatalf("decode failed: %v\noutput:\n%s", err, result.Rewritten)
	}
	if len(decoded.Symbols) != 9 {
		t.Errorf("decoded %d symbols, want 9", len(decoded.Symbols))
	}
}

func TestRewriter_NoProgressWithoutToken(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 3, EnableProgress: true})

	symbols := make([]map[string]any, 9)
	for i := 0; i < 9; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": "pkg.S" + string(rune('A'+i)),
			"kind":          "function",
			"score":         0.9,
			"provenance":    "lsp",
			"distance":      0,
		}
	}
	payload := map[string]any{
		"tool":        "test",
		"tokensUsed":  100,
		"tokenBudget": 1000,
		"symbols":     symbols,
		"edges":       []any{},
	}
	payloadJSON, _ := json.Marshal(payload)

	// nil progressFn = no streaming, should still convert.
	result := rw.RewriteToolResult(string(payloadJSON), nil)
	if !result.Converted {
		t.Fatal("expected conversion even without progress")
	}
	// Should use buffered encode (has symbols= in header, not streaming).
	if !strings.Contains(result.Rewritten, "symbols=9") {
		t.Errorf("expected buffered encode with symbols=9, got:\n%s", result.Rewritten)
	}
}

func TestRewriter_SmallPayloadNoStreaming(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 10, EnableProgress: true})

	// Only 3 symbols, threshold is 10, so no streaming even with progressFn.
	payload := `{"tool":"test","tokensUsed":50,"tokenBudget":500,"symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"x","distance":0},{"qualifiedName":"a.B","kind":"type","score":0.8,"provenance":"x","distance":0},{"qualifiedName":"a.C","kind":"method","score":0.7,"provenance":"x","distance":1}],"edges":[]}`

	called := false
	progressFn := func(fragment string, progress, total int) { called = true }

	result := rw.RewriteToolResult(payload, progressFn)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if called {
		t.Error("progress should not be called when below threshold")
	}
	// Should be buffered encode.
	if !strings.Contains(result.Rewritten, "symbols=3") {
		t.Errorf("expected symbols=3 in header")
	}
}

func TestRewriter_TabularFallback(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5, EnableProgress: true})

	payload := `{"employees":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`

	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected tabular conversion")
	}
	if !strings.Contains(result.Rewritten, "## employees") {
		t.Errorf("expected tabular output, got:\n%s", result.Rewritten)
	}
}

func TestRewriter_NonJSON_PassThrough(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5, EnableProgress: true})

	result := rw.RewriteToolResult("just plain text", nil)
	if result.Converted {
		t.Error("plain text should not be converted")
	}
}

func TestExtractProgressToken(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	var mu sync.Mutex

	line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"blast_radius","arguments":{},"_meta":{"progressToken":"tok123"}}}`

	names := make(map[string]string)
	extractRequestMeta(line, &mu, tokens, names)

	if _, ok := tokens["1"]; !ok {
		t.Error("expected token for request ID 1")
	}
	if names["1"] != "blast_radius" {
		t.Errorf("expected tool name 'blast_radius', got %q", names["1"])
	}
}
