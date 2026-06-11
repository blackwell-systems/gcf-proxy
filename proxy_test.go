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
	if !strings.Contains(result.Rewritten, "GCF profile=graph tool=test") {
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

func TestDecodeRequestGCF_ToolCallWithGCFArg(t *testing.T) {
	gcfPayload := "GCF profile=generic\nname=Alice\nage=30\n"
	gcfEscaped, _ := json.Marshal(gcfPayload)
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"process_data","arguments":{"data":` + string(gcfEscaped) + `}}}`

	result := decodeRequestGCF(request)

	// The GCF string should be decoded to a JSON object inline.
	if strings.Contains(result, "GCF profile=") {
		t.Errorf("GCF should have been decoded, got:\n%s", result)
	}
	if !strings.Contains(result, `"name":"Alice"`) {
		t.Errorf("expected decoded JSON with name=Alice, got:\n%s", result)
	}
	if !strings.Contains(result, `"age":30`) {
		t.Errorf("expected decoded JSON with age=30, got:\n%s", result)
	}
}

func TestDecodeRequestGCF_NonGCFArgUntouched(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello world"}}}`

	result := decodeRequestGCF(request)
	if result != request {
		t.Errorf("non-GCF request should pass through unchanged:\n  got:  %s\n  want: %s", result, request)
	}
}

func TestDecodeRequestGCF_NonToolCallUntouched(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"file:///tmp/test"}}`

	result := decodeRequestGCF(request)
	if result != request {
		t.Errorf("non-tools/call request should pass through unchanged")
	}
}

func TestDecodeRequestGCF_MixedArgs(t *testing.T) {
	gcfPayload := "GCF profile=generic\n## items [2]{id,name}\n1|Alice\n2|Bob\n"
	gcfEscaped, _ := json.Marshal(gcfPayload)
	request := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"update","arguments":{"path":"/tmp/file.txt","data":` + string(gcfEscaped) + `,"force":true}}}`

	result := decodeRequestGCF(request)

	// GCF arg should be decoded, others untouched.
	if strings.Contains(result, "GCF profile=") {
		t.Errorf("GCF should have been decoded")
	}
	if !strings.Contains(result, `"/tmp/file.txt"`) {
		t.Errorf("non-GCF string arg should be preserved")
	}
	if !strings.Contains(result, `"Alice"`) {
		t.Errorf("expected decoded tabular data with Alice")
	}
}

func TestRewriter_V2GraphHeader(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	payload := `{"tool":"test","tokensUsed":50,"tokenBudget":500,"symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if !strings.HasPrefix(result.Rewritten, "GCF profile=graph ") {
		t.Errorf("graph output missing v2.0 profile header:\n%s", result.Rewritten)
	}
}

func TestRewriter_GenericKeyOrder(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	// Keys in JSON insertion order: name, age, active
	payload := `{"name":"Alice","age":30,"active":true}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	// GCF output should preserve key order from JSON (name before age before active)
	lines := strings.Split(result.Rewritten, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d:\n%s", len(lines), result.Rewritten)
	}
	if lines[1] != "name=Alice" {
		t.Errorf("expected name=Alice on line 2, got: %s", lines[1])
	}
	if lines[2] != "age=30" {
		t.Errorf("expected age=30 on line 3, got: %s", lines[2])
	}
	if lines[3] != "active=true" {
		t.Errorf("expected active=true on line 4, got: %s", lines[3])
	}
}

func TestRewriter_V2GenericHeader(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	payload := `{"employees":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if !strings.HasPrefix(result.Rewritten, "GCF profile=generic\n") {
		t.Errorf("generic output missing v2.0 profile header:\n%s", result.Rewritten)
	}
}

func TestRewriter_V2StreamingTrailer(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 2, EnableProgress: true})
	symbols := make([]map[string]any, 4)
	for i := 0; i < 4; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": "pkg.S" + string(rune('A'+i)),
			"kind": "function", "score": 0.9, "provenance": "lsp", "distance": 0,
		}
	}
	payload := map[string]any{"tool": "test", "tokensUsed": 100, "tokenBudget": 1000, "symbols": symbols, "edges": []any{}}
	payloadJSON, _ := json.Marshal(payload)
	result := rw.RewriteToolResult(string(payloadJSON), func(string, int, int) {})
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if !strings.Contains(result.Rewritten, "##! summary") {
		t.Errorf("streaming output missing v2.0 ##! trailer:\n%s", result.Rewritten)
	}
	if strings.Contains(result.Rewritten, "## _summary") {
		t.Errorf("streaming output has old ## _summary trailer:\n%s", result.Rewritten)
	}
}
