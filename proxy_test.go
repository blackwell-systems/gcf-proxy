package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
