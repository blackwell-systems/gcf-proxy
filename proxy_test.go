package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestHTTPBackend_JSONResponse(t *testing.T) {
	// Mock HTTP server that returns a JSON-RPC response.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		json.Unmarshal(body, &msg)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "test-session-123")
		resp := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"employees\":[{\"id\":1,\"name\":\"Alice\"},{\"id\":2,\"name\":\"Bob\"}]}"}]}}`
		w.Write([]byte(resp))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 response line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "employees") {
		t.Errorf("expected employees in response, got: %s", lines[0])
	}
	// Session ID should be captured.
	if backend.sessionID != "test-session-123" {
		t.Errorf("expected session ID 'test-session-123', got %q", backend.sessionID)
	}
}

func TestHTTPBackend_SSEResponse(t *testing.T) {
	// Mock HTTP server that returns SSE stream.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Two events: a progress notification and a final result.
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1,\"total\":2}}\n\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n")
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 SSE events, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "progress") {
		t.Errorf("expected progress notification, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "done") {
		t.Errorf("expected final result, got: %s", lines[1])
	}
}

func TestHTTPBackend_RewritesResponse(t *testing.T) {
	// Mock server returns a graph payload as JSON.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"tool\":\"test\",\"tokensUsed\":50,\"tokenBudget\":500,\"symbols\":[{\"qualifiedName\":\"a.A\",\"kind\":\"function\",\"score\":0.9,\"provenance\":\"lsp\",\"distance\":0}],\"edges\":[]}"}]}}`
		w.Write([]byte(resp))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})

	lines, _ := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)

	// Rewrite the response (same as the stdio path).
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	rewritten := rewriteResponse(lines[0], rw, &outputMu, &tokenMu, tokens, names)
	if !strings.Contains(rewritten, "GCF profile=graph") {
		t.Errorf("expected GCF rewrite, got: %s", rewritten[:min(len(rewritten), 200)])
	}
}

func TestSessionDedup_BareRefsOnSecondCall(t *testing.T) {
	sess := gcf.NewSession()
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, Session: sess})

	// First call: 3 symbols with realistic qualified names.
	payload1 := `{"tool":"test","tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"github.com/org/repo/internal/auth.Middleware","kind":"function","score":0.9,"provenance":"lsp_resolved","distance":0},{"qualifiedName":"github.com/org/repo/internal/auth.ValidateToken","kind":"type","score":0.8,"provenance":"lsp_resolved","distance":0},{"qualifiedName":"github.com/org/repo/internal/server.NewServer","kind":"method","score":0.7,"provenance":"lsp_resolved","distance":1}],"edges":[]}`
	r1 := rw.RewriteToolResult(payload1, nil)
	if !r1.Converted {
		t.Fatal("expected conversion on call 1")
	}
	if strings.Contains(r1.Rewritten, "previously transmitted") {
		t.Error("call 1 should have no bare refs")
	}
	if !strings.Contains(r1.Rewritten, "session=true") {
		t.Error("call 1 should have session=true header")
	}

	// Second call: same 3 symbols + 1 new.
	payload2 := `{"tool":"test","tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"github.com/org/repo/internal/auth.Middleware","kind":"function","score":0.9,"provenance":"lsp_resolved","distance":0},{"qualifiedName":"github.com/org/repo/internal/auth.ValidateToken","kind":"type","score":0.8,"provenance":"lsp_resolved","distance":0},{"qualifiedName":"github.com/org/repo/internal/server.NewServer","kind":"method","score":0.7,"provenance":"lsp_resolved","distance":1},{"qualifiedName":"github.com/org/repo/internal/handler.ProcessRequest","kind":"function","score":0.6,"provenance":"lsp_resolved","distance":1}],"edges":[]}`
	r2 := rw.RewriteToolResult(payload2, nil)
	if !r2.Converted {
		t.Fatal("expected conversion on call 2")
	}

	// Should have bare refs for pkg.A, pkg.B, pkg.C.
	bareCount := strings.Count(r2.Rewritten, "previously transmitted")
	if bareCount != 3 {
		t.Errorf("expected 3 bare refs on call 2, got %d\noutput:\n%s", bareCount, r2.Rewritten)
	}

	// Should have full declaration for the new symbol.
	if !strings.Contains(r2.Rewritten, "fn github.com/org/repo/internal/handler.ProcessRequest") {
		t.Errorf("expected full declaration for ProcessRequest on call 2\noutput:\n%s", r2.Rewritten)
	}

	// Call 2 should be smaller than call 1.
	if len(r2.Rewritten) >= len(r1.Rewritten) {
		t.Errorf("call 2 (%d bytes) should be smaller than call 1 (%d bytes) due to dedup",
			len(r2.Rewritten), len(r1.Rewritten))
	}
}

func TestSessionDedup_NoSessionNoBareRefs(t *testing.T) {
	// Without --session, no bare refs even on repeated calls.
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100})

	payload := `{"tool":"test","tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"pkg.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`

	r1 := rw.RewriteToolResult(payload, nil)
	r2 := rw.RewriteToolResult(payload, nil)

	if strings.Contains(r1.Rewritten, "session=true") {
		t.Error("no session flag should mean no session=true header")
	}
	if strings.Contains(r2.Rewritten, "previously transmitted") {
		t.Error("no session flag should mean no bare refs")
	}
}

func TestSessionDedup_CompoundingSavings(t *testing.T) {
	sess := gcf.NewSession()
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, Session: sess})

	// Build a 10-symbol payload.
	symbols := make([]map[string]any, 10)
	for i := 0; i < 10; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": fmt.Sprintf("github.com/org/repo/pkg.Symbol%d", i),
			"kind": "function", "score": 0.9 - float64(i)*0.05, "provenance": "lsp", "distance": i / 4,
		}
	}
	edges := []map[string]any{
		{"source": "github.com/org/repo/pkg.Symbol1", "target": "github.com/org/repo/pkg.Symbol0", "edgeType": "calls"},
		{"source": "github.com/org/repo/pkg.Symbol2", "target": "github.com/org/repo/pkg.Symbol0", "edgeType": "references"},
	}
	payload := map[string]any{
		"tool": "test", "tokensUsed": 200, "tokenBudget": 2000,
		"symbols": symbols, "edges": edges,
	}
	payloadJSON, _ := json.Marshal(payload)

	// Call 1: all new.
	r1 := rw.RewriteToolResult(string(payloadJSON), nil)

	// Call 2: same payload, all symbols become bare refs.
	r2 := rw.RewriteToolResult(string(payloadJSON), nil)

	// Call 3: add 2 new symbols, 10 old become bare refs.
	symbols3 := append(symbols, map[string]any{
		"qualifiedName": "github.com/org/repo/pkg.New1",
		"kind": "type", "score": 0.3, "provenance": "ast", "distance": 2,
	}, map[string]any{
		"qualifiedName": "github.com/org/repo/pkg.New2",
		"kind": "method", "score": 0.25, "provenance": "ast", "distance": 2,
	})
	payload3 := map[string]any{
		"tool": "test", "tokensUsed": 250, "tokenBudget": 2000,
		"symbols": symbols3, "edges": edges,
	}
	payload3JSON, _ := json.Marshal(payload3)
	r3 := rw.RewriteToolResult(string(payload3JSON), nil)

	// Verify compounding savings.
	t.Logf("Call 1: %d bytes (all new)", len(r1.Rewritten))
	t.Logf("Call 2: %d bytes (all bare refs)", len(r2.Rewritten))
	t.Logf("Call 3: %d bytes (10 bare + 2 new)", len(r3.Rewritten))

	if len(r2.Rewritten) >= len(r1.Rewritten) {
		t.Errorf("call 2 should be smaller than call 1")
	}
	if len(r3.Rewritten) >= len(r1.Rewritten) {
		t.Errorf("call 3 should be smaller than call 1")
	}

	// Call 2 should have 10 bare refs.
	if c := strings.Count(r2.Rewritten, "previously transmitted"); c != 10 {
		t.Errorf("call 2: expected 10 bare refs, got %d", c)
	}

	// Call 3 should have 10 bare refs + 2 new.
	if c := strings.Count(r3.Rewritten, "previously transmitted"); c != 10 {
		t.Errorf("call 3: expected 10 bare refs, got %d", c)
	}

	savings2 := 100.0 * (1.0 - float64(len(r2.Rewritten))/float64(len(r1.Rewritten)))
	savings3 := 100.0 * (1.0 - float64(len(r3.Rewritten))/float64(len(r1.Rewritten)))
	t.Logf("Savings: call 2 = %.0f%%, call 3 = %.0f%%", savings2, savings3)
}

func TestHTTPFrontend_HealthCheck(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", frontend.handleHealth)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok") {
		t.Errorf("expected ok in body, got: %s", body)
	}
}

func TestHTTPFrontend_MCPRequest(t *testing.T) {
	// Mock upstream HTTP server that returns a tool result.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"employees\":[{\"id\":1,\"name\":\"Alice\"},{\"id\":2,\"name\":\"Bob\"}]}"}]}}`))
	}))
	defer upstream.Close()

	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	frontend.SetHTTPBackend(upstream.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Send a tool call.
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list"}}`
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Response should contain GCF-encoded content.
	if !strings.Contains(bodyStr, "GCF profile=generic") {
		t.Errorf("expected GCF in response, got: %s", bodyStr[:min(len(bodyStr), 200)])
	}
}

func TestHTTPFrontend_SSEResponse(t *testing.T) {
	// Mock upstream that returns SSE.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hello\"}]}}\n\n")
	}))
	defer upstream.Close()

	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	frontend.SetHTTPBackend(upstream.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected SSE content type, got: %s", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "data:") {
		t.Errorf("expected SSE data events, got: %s", string(body))
	}
}

func TestCache_HitOnIdenticalContent(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, EnableCache: true})

	payload := `{"employees":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`

	r1 := rw.RewriteToolResult(payload, nil)
	if !r1.Converted {
		t.Fatal("expected conversion on first call")
	}

	r2 := rw.RewriteToolResult(payload, nil)
	if !r2.Converted {
		t.Fatal("expected conversion on second call")
	}

	// Second call should return identical result (cache hit).
	if r1.Rewritten != r2.Rewritten {
		t.Error("cache hit should return identical output")
	}
}

func TestCache_MissOnDifferentContent(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, EnableCache: true})

	r1 := rw.RewriteToolResult(`{"name":"Alice"}`, nil)
	r2 := rw.RewriteToolResult(`{"name":"Bob"}`, nil)

	if r1.Rewritten == r2.Rewritten {
		t.Error("different input should produce different output")
	}
}

func TestCache_DisabledByDefault(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100})
	if rw.cache != nil {
		t.Error("cache should be nil when not enabled")
	}
}

func TestMinSize_SkipsSmallResponses(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, MinSize: 100})

	// 16 bytes, below threshold.
	r := rw.RewriteToolResult(`{"x":1}`, nil)
	if r.Converted {
		t.Error("small response should be skipped")
	}
}

func TestMinSize_EncodesLargeResponses(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, MinSize: 10})

	payload := `{"employees":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`
	r := rw.RewriteToolResult(payload, nil)
	if !r.Converted {
		t.Error("large response should be encoded")
	}
}

func TestMinSize_ZeroMeansNoMinimum(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, MinSize: 0})

	r := rw.RewriteToolResult(`{"x":1}`, nil)
	if !r.Converted {
		t.Error("with min-size 0, all JSON should be encoded")
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

// ---- rewriter.go: graph profile edge cases ----

func TestRewriter_GraphWithEdges(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100})
	payload := `{"tool":"test","tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"lsp","distance":0},{"qualifiedName":"a.B","kind":"function","score":0.8,"provenance":"lsp","distance":1}],"edges":[{"source":"a.B","target":"a.A","edgeType":"calls"}]}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if result.EdgeCount != 1 {
		t.Errorf("expected 1 edge, got %d", result.EdgeCount)
	}
	if !strings.Contains(result.Rewritten, "calls") {
		t.Errorf("expected edge type 'calls' in output:\n%s", result.Rewritten)
	}
}

func TestRewriter_GraphMissingTool(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	// Has symbols but no tool field: should fall through to generic.
	payload := `{"tokensUsed":100,"tokenBudget":1000,"symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected generic fallback conversion")
	}
	// Should be generic profile, not graph.
	if strings.Contains(result.Rewritten, "profile=graph") {
		t.Error("missing tool field should not produce graph profile")
	}
}

func TestRewriter_GraphNilSymbols(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	// Has tool but symbols is null: should fall through to generic.
	payload := `{"tool":"test","tokensUsed":100,"tokenBudget":1000}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected generic fallback conversion")
	}
	if strings.Contains(result.Rewritten, "profile=graph") {
		t.Error("null symbols should not produce graph profile")
	}
}

func TestRewriter_EmptySymbols(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	// Has tool and empty symbols array: still valid graph payload.
	payload := `{"tool":"test","tokensUsed":50,"tokenBudget":500,"symbols":[],"edges":[]}`
	result := rw.RewriteToolResult(payload, nil)
	// Empty symbols is falsy for the nil check (len 0), but not nil after unmarshal.
	// Behavior depends on implementation: symbols != nil but len == 0 passes the check.
	if result.Converted && strings.Contains(result.Rewritten, "profile=graph") {
		if result.SymbolCount != 0 {
			t.Errorf("expected 0 symbols, got %d", result.SymbolCount)
		}
	}
}

func TestRewriter_EmptyString(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	result := rw.RewriteToolResult("", nil)
	if result.Converted {
		t.Error("empty string should not be converted")
	}
}

func TestRewriter_WhitespaceOnly(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	result := rw.RewriteToolResult("   \n\t  ", nil)
	if result.Converted {
		t.Error("whitespace-only should not be converted")
	}
}

func TestRewriter_InvalidJSON(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	result := rw.RewriteToolResult(`{"broken":`, nil)
	if result.Converted {
		t.Error("invalid JSON should not be converted")
	}
}

func TestRewriter_ArrayInput(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	result := rw.RewriteToolResult(`[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]`, nil)
	if !result.Converted {
		t.Fatal("array input should be converted via generic profile")
	}
	if !strings.Contains(result.Rewritten, "GCF profile=generic") {
		t.Errorf("expected generic profile for array input:\n%s", result.Rewritten)
	}
}

func TestRewriter_GraphWithPackRoot(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100})
	payload := `{"tool":"test","tokensUsed":50,"tokenBudget":500,"packRoot":"/home/user/project","symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`
	result := rw.RewriteToolResult(payload, nil)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if !strings.Contains(result.Rewritten, "GCF profile=graph") {
		t.Errorf("expected graph profile:\n%s", result.Rewritten)
	}
}

// ---- rewriter.go: streaming encode with edges ----

func TestRewriter_StreamingWithEdges(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 2, EnableProgress: true})

	symbols := make([]map[string]any, 4)
	for i := 0; i < 4; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": "pkg.S" + string(rune('A'+i)),
			"kind":          "function",
			"score":         0.9 - float64(i)*0.05,
			"provenance":    "lsp",
			"distance":      0,
		}
	}
	payload := map[string]any{
		"tool": "test", "tokensUsed": 100, "tokenBudget": 1000,
		"symbols": symbols,
		"edges": []map[string]any{
			{"source": "pkg.SB", "target": "pkg.SA", "edgeType": "calls"},
			{"source": "pkg.SC", "target": "pkg.SA", "edgeType": "references"},
		},
	}
	payloadJSON, _ := json.Marshal(payload)

	var fragments []string
	progressFn := func(fragment string, progress, total int) {
		fragments = append(fragments, fragment)
	}

	result := rw.RewriteToolResult(string(payloadJSON), progressFn)
	if !result.Converted {
		t.Fatal("expected conversion")
	}
	if result.EdgeCount != 2 {
		t.Errorf("expected 2 edges, got %d", result.EdgeCount)
	}
	if len(fragments) < 2 {
		t.Errorf("expected at least 2 progress fragments, got %d", len(fragments))
	}
}

// ---- rewriter.go: GCF-in re-encode with session ----

func TestRewriter_GCFInWithSession(t *testing.T) {
	sess := gcf.NewSession()
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, Session: sess})

	// First encode a payload to get valid GCF, then pass that GCF-in.
	p := &gcf.Payload{
		Tool:        "test",
		TokensUsed:  100,
		TokenBudget: 1000,
		Symbols: []gcf.Symbol{
			{QualifiedName: "pkg.A", Kind: "function", Score: 0.9, Provenance: "lsp", Distance: 0},
		},
	}
	gcfText := gcf.Encode(p)

	result := rw.RewriteToolResult(gcfText, nil)
	if !result.Converted {
		t.Fatal("expected GCF-in to be re-encoded with session")
	}
	if !strings.Contains(result.Rewritten, "session=true") {
		t.Errorf("expected session=true in re-encoded output:\n%s", result.Rewritten)
	}
}

func TestRewriter_GCFInWithoutSession(t *testing.T) {
	// Without a session, GCF-in prefix should still start with GCF but won't be re-encoded.
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100})
	p := &gcf.Payload{
		Tool:        "test",
		TokensUsed:  100,
		TokenBudget: 1000,
		Symbols: []gcf.Symbol{
			{QualifiedName: "pkg.A", Kind: "function", Score: 0.9, Provenance: "lsp", Distance: 0},
		},
	}
	gcfText := gcf.Encode(p)

	// Starts with "GCF" but no session, so it won't enter the GCF-in path.
	// It also isn't JSON (doesn't start with '{'), so it should pass through.
	result := rw.RewriteToolResult(gcfText, nil)
	// GCF text doesn't start with '{' or '[', so it's not JSON; should pass through.
	if result.Converted {
		t.Error("GCF-in without session should pass through (not JSON)")
	}
}

// ---- rewriter.go: stats recording ----

func TestRewriter_StatsRecorded(t *testing.T) {
	stats := &Stats{}
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, Stats: stats})

	payload := `{"tool":"test","tokensUsed":50,"tokenBudget":500,"symbols":[{"qualifiedName":"a.A","kind":"function","score":0.9,"provenance":"lsp","distance":0}],"edges":[]}`
	rw.RewriteToolResult(payload, nil)

	if stats.Calls.Load() != 1 {
		t.Errorf("expected 1 call recorded, got %d", stats.Calls.Load())
	}
	if stats.Symbols.Load() != 1 {
		t.Errorf("expected 1 symbol recorded, got %d", stats.Symbols.Load())
	}
}

func TestRewriter_StatsRecordedGeneric(t *testing.T) {
	stats := &Stats{}
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, Stats: stats})

	payload := `{"employees":[{"id":1,"name":"Alice"}]}`
	rw.RewriteToolResult(payload, nil)

	if stats.Calls.Load() != 1 {
		t.Errorf("expected 1 call recorded, got %d", stats.Calls.Load())
	}
}

// ---- rewriter.go: cache with stats ----

func TestCache_StatsTrackHits(t *testing.T) {
	stats := &Stats{}
	rw := NewRewriter(RewriterConfig{StreamThreshold: 100, EnableCache: true, Stats: stats})

	payload := `{"employees":[{"id":1,"name":"Alice"}]}`
	rw.RewriteToolResult(payload, nil) // miss
	rw.RewriteToolResult(payload, nil) // hit

	if stats.CacheHits.Load() != 1 {
		t.Errorf("expected 1 cache hit, got %d", stats.CacheHits.Load())
	}
}

// ---- backend_http.go: error cases ----

func TestHTTPBackend_ServerError500(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)
	// The backend reads the body regardless of status code; it's just text.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The 500 response body is "internal server error\n", which is not valid JSON.
	// So it should still return the line.
	found := false
	for _, l := range lines {
		if strings.Contains(l, "internal server error") {
			found = true
		}
	}
	if !found && len(lines) > 0 {
		t.Errorf("expected error text in response lines, got: %v", lines)
	}
}

func TestHTTPBackend_ConnectionRefused(t *testing.T) {
	// Connect to a port that's not listening.
	backend := NewHTTPBackend("http://127.0.0.1:1")
	_, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err == nil {
		t.Error("expected error for connection refused")
	}
}

func TestHTTPBackend_Timeout(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	backend.client.Timeout = 50 * time.Millisecond

	_, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestHTTPBackend_EmptyResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// Empty body.
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil lines for empty response, got %v", lines)
	}
}

func TestHTTPBackend_MalformedJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"broken`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still return the line (even if malformed).
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != `{"broken` {
		t.Errorf("expected malformed JSON passthrough, got: %s", lines[0])
	}
}

func TestHTTPBackend_SessionIDPersists(t *testing.T) {
	var receivedSessionID string
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Mcp-Session-Id", "session-abc")
		} else {
			receivedSessionID = r.Header.Get("Mcp-Session-Id")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	backend.Send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	backend.Send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	if receivedSessionID != "session-abc" {
		t.Errorf("expected session ID 'session-abc' on second request, got %q", receivedSessionID)
	}
}

func TestHTTPBackend_SSEWithoutTrailingBlankLine(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Event without trailing blank line (edge case).
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n")
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still capture the final event.
	if len(lines) != 1 {
		t.Fatalf("expected 1 line from final-event handling, got %d: %v", len(lines), lines)
	}
}

func TestHTTPBackend_SSEInvalidJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: not-json\n\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid JSON events should be skipped.
	if len(lines) != 1 {
		t.Fatalf("expected 1 valid line (invalid skipped), got %d: %v", len(lines), lines)
	}
}

func TestHTTPBackend_SSEWithEventAndIDFields(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message\n")
		fmt.Fprint(w, "id: 42\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (event/id fields ignored), got %d", len(lines))
	}
}

func TestHTTPBackend_MultiLineJSONResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Two JSON-RPC messages separated by newline.
		w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	backend := NewHTTPBackend(server.URL)
	lines, err := backend.Send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
}

// ---- frontend_http.go: method not allowed ----

func TestHTTPFrontend_MethodNotAllowed(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHTTPFrontend_EmptyBody(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", resp.StatusCode)
	}
}

func TestHTTPFrontend_Notification(t *testing.T) {
	// Upstream that never gets called (notification has no response).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	frontend.SetHTTPBackend(upstream.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Notification: no "id" field.
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202 for notification, got %d", resp.StatusCode)
	}
}

func TestHTTPFrontend_BackendError(t *testing.T) {
	// No upstream configured, no stdio backend: should return error response.
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	// Set an unreachable upstream to trigger an error.
	frontend.SetHTTPBackend("http://127.0.0.1:1")

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "error") {
		t.Errorf("expected error response, got: %s", string(body))
	}
}

func TestHTTPFrontend_NoContentResponse(t *testing.T) {
	// Upstream returns empty body (no response lines).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// empty body
	}))
	defer upstream.Close()

	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	frontend.SetHTTPBackend(upstream.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for no response lines, got %d", resp.StatusCode)
	}
}

func TestHTTPFrontend_GCFDecodingInRequest(t *testing.T) {
	// Verify that GCF arguments in the request body are decoded before forwarding.
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer upstream.Close()

	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	frontend := NewHTTPFrontend(":0", rw, &Stats{}, false)
	frontend.SetHTTPBackend(upstream.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", frontend.handleMCP)
	server := httptest.NewServer(mux)
	defer server.Close()

	gcfPayload := "GCF profile=generic\nname=Alice\nage=30\n"
	gcfEscaped, _ := json.Marshal(gcfPayload)
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"process","arguments":{"data":` + string(gcfEscaped) + `}}}`

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// The upstream should have received decoded JSON, not GCF.
	if strings.Contains(receivedBody, "GCF profile=") {
		t.Errorf("expected GCF to be decoded before forwarding to upstream, got: %s", receivedBody)
	}
}

// ---- stats.go ----

func TestStats_WriteSummary_ZeroCalls(t *testing.T) {
	stats := &Stats{}
	var buf bytes.Buffer
	stats.WriteSummary(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected empty output for zero calls, got: %s", buf.String())
	}
}

func TestStats_WriteSummary_WithData(t *testing.T) {
	stats := &Stats{}
	stats.Record(1000, 300, 10, 5)
	stats.Record(2000, 600, 20, 8)

	var buf bytes.Buffer
	stats.WriteSummary(&buf)
	output := buf.String()

	if !strings.Contains(output, "Tool calls rewritten:  2") {
		t.Errorf("expected 2 calls in summary:\n%s", output)
	}
	if !strings.Contains(output, "Symbols processed:     30") {
		t.Errorf("expected 30 symbols in summary:\n%s", output)
	}
	if !strings.Contains(output, "Edges processed:       13") {
		t.Errorf("expected 13 edges in summary:\n%s", output)
	}
	if !strings.Contains(output, "Bytes saved:") {
		t.Errorf("expected bytes saved in summary:\n%s", output)
	}
}

func TestStats_WriteSummary_WithCacheHits(t *testing.T) {
	stats := &Stats{}
	stats.Record(100, 50, 1, 0)
	stats.CacheHits.Add(3)

	var buf bytes.Buffer
	stats.WriteSummary(&buf)
	output := buf.String()

	if !strings.Contains(output, "Cache hits:            3") {
		t.Errorf("expected cache hits in summary:\n%s", output)
	}
}

func TestStats_SavedBytes(t *testing.T) {
	stats := &Stats{}
	stats.Record(1000, 300, 5, 2)
	if stats.SavedBytes() != 700 {
		t.Errorf("expected 700 saved bytes, got %d", stats.SavedBytes())
	}
}

func TestStats_SavedPct(t *testing.T) {
	stats := &Stats{}
	stats.Record(1000, 300, 5, 2)
	pct := stats.SavedPct()
	if pct < 69.9 || pct > 70.1 {
		t.Errorf("expected ~70%% saved, got %.1f%%", pct)
	}
}

func TestStats_SavedPctZero(t *testing.T) {
	stats := &Stats{}
	if stats.SavedPct() != 0 {
		t.Errorf("expected 0%% for no data, got %.1f%%", stats.SavedPct())
	}
}

func TestFmtBytes_Bytes(t *testing.T) {
	result := fmtBytes(42)
	if result != "42B" {
		t.Errorf("expected '42B', got %q", result)
	}
}

func TestFmtBytes_KB(t *testing.T) {
	result := fmtBytes(1500)
	if result != "1.5KB" {
		t.Errorf("expected '1.5KB', got %q", result)
	}
}

func TestFmtBytes_MB(t *testing.T) {
	result := fmtBytes(2_500_000)
	if result != "2.5MB" {
		t.Errorf("expected '2.5MB', got %q", result)
	}
}

func TestFmtBytes_Zero(t *testing.T) {
	result := fmtBytes(0)
	if result != "0B" {
		t.Errorf("expected '0B', got %q", result)
	}
}

func TestFmtInt_Small(t *testing.T) {
	result := fmtInt(42)
	if result != "42" {
		t.Errorf("expected '42', got %q", result)
	}
}

func TestFmtInt_Thousands(t *testing.T) {
	result := fmtInt(1500)
	if result != "1.5K" {
		t.Errorf("expected '1.5K', got %q", result)
	}
}

func TestFmtInt_Millions(t *testing.T) {
	result := fmtInt(2_500_000)
	if result != "2.5M" {
		t.Errorf("expected '2.5M', got %q", result)
	}
}

func TestEstTokens(t *testing.T) {
	if estTokens(400) != 100 {
		t.Errorf("expected 100 tokens for 400 bytes, got %d", estTokens(400))
	}
	if estTokens(0) != 0 {
		t.Errorf("expected 0 tokens for 0 bytes, got %d", estTokens(0))
	}
}

// ---- main.go: decodeRequestGCF edge cases ----

func TestDecodeRequestGCF_NestedGCFInArguments(t *testing.T) {
	// GCF with nested/tabular data in arguments.
	gcfPayload := "GCF profile=generic\n## items [2]{id,name}\n1|Alice\n2|Bob\n"
	gcfEscaped, _ := json.Marshal(gcfPayload)
	request := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"process","arguments":{"table":` + string(gcfEscaped) + `}}}`

	result := decodeRequestGCF(request)
	if strings.Contains(result, "GCF profile=") {
		t.Errorf("nested GCF should have been decoded:\n%s", result)
	}
}

func TestDecodeRequestGCF_ArrayContainingGCF(t *testing.T) {
	// Array argument value: GCF strings in arrays won't be decoded (only map string values are checked).
	request := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":{"items":["GCF profile=generic\nname=test\n"]}}}`

	result := decodeRequestGCF(request)
	// Array values are not decoded (the code only checks map[string]json.RawMessage).
	// This should pass through unchanged.
	if result != request {
		t.Errorf("array GCF args should pass through unchanged:\n  got:  %s\n  want: %s", result, request)
	}
}

func TestDecodeRequestGCF_EmptyArguments(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ping","arguments":{}}}`
	result := decodeRequestGCF(request)
	if result != request {
		t.Errorf("empty arguments should pass through unchanged")
	}
}

func TestDecodeRequestGCF_NonStringArgValues(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"calc","arguments":{"x":42,"y":true,"z":null}}}`
	result := decodeRequestGCF(request)
	if result != request {
		t.Errorf("non-string args should pass through unchanged")
	}
}

func TestDecodeRequestGCF_EmptyString(t *testing.T) {
	result := decodeRequestGCF("")
	if result != "" {
		t.Errorf("empty string should pass through")
	}
}

func TestDecodeRequestGCF_WhitespaceOnly(t *testing.T) {
	result := decodeRequestGCF("   ")
	if result != "   " {
		t.Errorf("whitespace should pass through")
	}
}

func TestDecodeRequestGCF_InvalidJSON(t *testing.T) {
	result := decodeRequestGCF(`{"broken`)
	if result != `{"broken` {
		t.Errorf("invalid JSON should pass through")
	}
}

func TestDecodeRequestGCF_MalformedGCFPassesThrough(t *testing.T) {
	// GCF prefix but invalid content: DecodeGeneric will fail, so it stays as string.
	gcfPayload := "GCF garbage-that-wont-parse"
	gcfEscaped, _ := json.Marshal(gcfPayload)
	request := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"test","arguments":{"data":` + string(gcfEscaped) + `}}}`

	result := decodeRequestGCF(request)
	// If decode fails, the original value is kept, so the request is unchanged.
	if result != request {
		t.Errorf("malformed GCF should leave request unchanged:\n  got:  %s\n  want: %s", result, request)
	}
}

// ---- main.go: extractRequestMeta edge cases ----

func TestExtractRequestMeta_NonToolsCall(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)
	var mu sync.Mutex

	line := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file:///tmp"}}`
	extractRequestMeta(line, &mu, tokens, names)

	if len(tokens) != 0 || len(names) != 0 {
		t.Error("non-tools/call should not extract tokens or names")
	}
}

func TestExtractRequestMeta_NoProgressToken(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)
	var mu sync.Mutex

	line := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"blast_radius","arguments":{}}}`
	extractRequestMeta(line, &mu, tokens, names)

	if _, ok := tokens["2"]; ok {
		t.Error("should not have a token when no progressToken in request")
	}
	if names["2"] != "blast_radius" {
		t.Errorf("expected tool name 'blast_radius', got %q", names["2"])
	}
}

func TestExtractRequestMeta_NullProgressToken(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)
	var mu sync.Mutex

	line := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"test","arguments":{},"_meta":{"progressToken":null}}}`
	extractRequestMeta(line, &mu, tokens, names)

	if _, ok := tokens["3"]; ok {
		t.Error("null progressToken should not be stored")
	}
}

func TestExtractRequestMeta_EmptyLine(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)
	var mu sync.Mutex

	extractRequestMeta("", &mu, tokens, names)
	extractRequestMeta("   ", &mu, tokens, names)

	if len(tokens) != 0 || len(names) != 0 {
		t.Error("empty/whitespace lines should not produce tokens or names")
	}
}

func TestExtractRequestMeta_NotificationNoID(t *testing.T) {
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)
	var mu sync.Mutex

	line := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	extractRequestMeta(line, &mu, tokens, names)

	if len(tokens) != 0 || len(names) != 0 {
		t.Error("notification without id should not extract anything")
	}
}

// ---- main.go: rewriteResponse edge cases ----

func TestRewriteResponse_NonJSONRPC(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	line := "just plain text"
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	if result != line {
		t.Errorf("non-JSON should pass through, got: %s", result)
	}
}

func TestRewriteResponse_EmptyString(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	result := rewriteResponse("", rw, &outputMu, &tokenMu, tokens, names)
	if result != "" {
		t.Errorf("empty string should pass through")
	}
}

func TestRewriteResponse_NoResultField(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	// Error response: has "error" but no "result".
	line := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	if result != line {
		t.Errorf("error response should pass through unchanged")
	}
}

func TestRewriteResponse_NoContentInResult(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	// Result without content array.
	line := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	if result != line {
		t.Errorf("result without content should pass through unchanged")
	}
}

func TestRewriteResponse_NonTextContent(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	// Content with type=image, not text.
	line := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"image","data":"base64data"}]}}`
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	if result != line {
		t.Errorf("non-text content should pass through unchanged")
	}
}

func TestRewriteResponse_EmptyTextContent(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	line := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":""}]}}`
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	if result != line {
		t.Errorf("empty text content should pass through unchanged")
	}
}

func TestRewriteResponse_PlainTextContent(t *testing.T) {
	rw := NewRewriter(RewriterConfig{StreamThreshold: 5})
	var outputMu, tokenMu sync.Mutex
	tokens := make(map[string]json.RawMessage)
	names := make(map[string]string)

	line := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello world"}]}}`
	result := rewriteResponse(line, rw, &outputMu, &tokenMu, tokens, names)
	// "hello world" is not JSON, should pass through.
	if result != line {
		t.Errorf("plain text in content should pass through unchanged")
	}
}

// ---- main.go: makeProgressNotification ----

func TestMakeProgressNotification(t *testing.T) {
	token := json.RawMessage(`"tok-123"`)
	data, err := makeProgressNotification(token, 5, 10, "processing symbols")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var notif map[string]any
	json.Unmarshal(data, &notif)

	if notif["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", notif["jsonrpc"])
	}
	if notif["method"] != "notifications/progress" {
		t.Errorf("expected notifications/progress, got %v", notif["method"])
	}

	params, _ := notif["params"].(map[string]any)
	if params["progressToken"] != "tok-123" {
		t.Errorf("expected token tok-123, got %v", params["progressToken"])
	}
	if int(params["progress"].(float64)) != 5 {
		t.Errorf("expected progress 5, got %v", params["progress"])
	}
	if int(params["total"].(float64)) != 10 {
		t.Errorf("expected total 10, got %v", params["total"])
	}
}

// ---- acceptsSSE ----

func TestAcceptsSSE(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"text/event-stream", true},
		{"application/json, text/event-stream", true},
		{"application/json", false},
		{"", false},
	}
	for _, tt := range tests {
		r, _ := http.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", tt.accept)
		got := acceptsSSE(r)
		if got != tt.want {
			t.Errorf("acceptsSSE(%q) = %v, want %v", tt.accept, got, tt.want)
		}
	}
}

// ---- Rewriter with verbose ----

func TestRewriter_VerboseCacheHit(t *testing.T) {
	stats := &Stats{}
	rw := NewRewriter(RewriterConfig{
		StreamThreshold: 100,
		EnableCache:     true,
		Verbose:         true,
		Stats:           stats,
	})

	payload := `{"employees":[{"id":1,"name":"Alice"}]}`
	rw.RewriteToolResult(payload, nil) // miss
	rw.RewriteToolResult(payload, nil) // hit (verbose path)

	if stats.CacheHits.Load() != 1 {
		t.Errorf("expected 1 cache hit, got %d", stats.CacheHits.Load())
	}
}

// ---- Streaming with stats ----

func TestRewriter_StreamingStats(t *testing.T) {
	stats := &Stats{}
	rw := NewRewriter(RewriterConfig{
		StreamThreshold: 2,
		EnableProgress:  true,
		Stats:           stats,
	})

	symbols := make([]map[string]any, 4)
	for i := 0; i < 4; i++ {
		symbols[i] = map[string]any{
			"qualifiedName": "pkg.S" + string(rune('A'+i)),
			"kind":          "function",
			"score":         0.9,
			"provenance":    "lsp",
			"distance":      0,
		}
	}
	payload := map[string]any{
		"tool": "test", "tokensUsed": 100, "tokenBudget": 1000,
		"symbols": symbols, "edges": []any{},
	}
	payloadJSON, _ := json.Marshal(payload)

	rw.RewriteToolResult(string(payloadJSON), func(string, int, int) {})

	if stats.Calls.Load() != 1 {
		t.Errorf("expected 1 call recorded for streaming, got %d", stats.Calls.Load())
	}
	if stats.Symbols.Load() != 4 {
		t.Errorf("expected 4 symbols recorded for streaming, got %d", stats.Symbols.Load())
	}
}
