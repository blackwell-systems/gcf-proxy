package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gcf "github.com/blackwell-systems/gcf-go"
)

// RewriterConfig controls streaming, session, and caching behavior.
type RewriterConfig struct {
	StreamThreshold int          // Min symbols before triggering incremental mode (default 5)
	EnableProgress  bool         // Whether to emit progress notifications
	Stats           *Stats       // Optional stats tracker
	Verbose         bool         // Log per-call savings to stderr
	Session         *gcf.Session // Optional session for cross-call dedup (nil = disabled)
	EnableCache     bool         // Cache encoded responses for identical tool calls
	MinSize         int          // Skip encoding for responses smaller than this (bytes, 0 = no minimum)
}

// ProgressFunc is called with partial GCF output and progress info.
// progress is the current count, total is estimated total (0 if unknown).
type ProgressFunc func(gcfFragment string, progress, total int)

// RewriteResult holds the outcome of a rewrite attempt.
type RewriteResult struct {
	Original    string // original text content
	Rewritten   string // GCF-encoded text (empty if not convertible)
	Converted   bool   // whether conversion happened
	SymbolCount int
	EdgeCount   int
}

// Rewriter handles JSON-to-GCF conversion with optional streaming progress.
type Rewriter struct {
	config RewriterConfig
	cache  map[string]RewriteResult // tool+args hash -> cached result
}

// NewRewriter creates a Rewriter with the given config.
func NewRewriter(config RewriterConfig) *Rewriter {
	if config.StreamThreshold <= 0 {
		config.StreamThreshold = 5
	}
	rw := &Rewriter{config: config}
	if config.EnableCache {
		rw.cache = make(map[string]RewriteResult)
	}
	return rw
}

// RewriteToolResult attempts to convert a JSON text content block to GCF.
// If progressFn is non-nil and the payload is large enough, it emits
// incremental GCF fragments via the callback.
func (r *Rewriter) RewriteToolResult(text string, progressFn ProgressFunc) RewriteResult {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) == 0 {
		return RewriteResult{Original: text}
	}

	// Min-size bypass: skip encoding for tiny responses where header overhead > savings.
	if r.config.MinSize > 0 && len(trimmed) < r.config.MinSize {
		return RewriteResult{Original: text}
	}

	// Response cache: return cached result for identical content.
	if r.cache != nil {
		if cached, ok := r.cache[trimmed]; ok {
			if r.config.Verbose {
				fmt.Fprintf(os.Stderr, "gcf-proxy: cache hit (%d bytes)\n", len(cached.Rewritten))
			}
			if r.config.Stats != nil {
				r.config.Stats.CacheHits.Add(1)
			}
			return cached
		}
	}

	// GCF-in: if the upstream already produces GCF graph profile and we have
	// a session, decode it, re-encode with session dedup (bare refs for
	// previously-transmitted symbols).
	if r.config.Verbose && strings.HasPrefix(trimmed, "GCF") {
		fmt.Fprintf(os.Stderr, "gcf-proxy: GCF-in detected, first 60 chars: %q, session=%v\n", trimmed[:min(60, len(trimmed))], r.config.Session != nil)
	}
	if strings.HasPrefix(trimmed, "GCF profile=graph") && r.config.Session != nil {
		p, err := gcf.Decode(trimmed)
		if err != nil && r.config.Verbose {
			fmt.Fprintf(os.Stderr, "gcf-proxy: GCF decode failed: %v\n", err)
		}
		if err == nil && p != nil {
			if r.config.Verbose {
				fmt.Fprintf(os.Stderr, "gcf-proxy: decoded %d symbols, session size before: %d\n", len(p.Symbols), r.config.Session.Size())
			}
			encoded := gcf.EncodeWithSession(p, r.config.Session)
			if r.config.Verbose {
				fmt.Fprintf(os.Stderr, "gcf-proxy: session size after: %d, bare refs in output: %d\n", r.config.Session.Size(), strings.Count(encoded, "previously transmitted"))
			}
			if r.config.Stats != nil {
				r.config.Stats.Record(len(trimmed), len(encoded), len(p.Symbols), len(p.Edges))
			}
			return r.cacheResult(trimmed, RewriteResult{
				Original:    text,
				Rewritten:   encoded,
				Converted:   true,
				SymbolCount: len(p.Symbols),
				EdgeCount:   len(p.Edges),
			})
		}
	}

	// Not JSON, not dedup-able GCF: pass through.
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return RewriteResult{Original: text}
	}

	// Try graph profile first (has tool + symbols). Objects only.
	if trimmed[0] == '{' {
		result := r.tryGraphProfile(trimmed, progressFn)
		if result.Converted {
			return result
		}
	}

	// Fall back to generic profile (any structured JSON, including arrays).
	// Use ParseJSONOrdered to preserve key insertion order from the source JSON.
	generic, err := gcf.ParseJSONOrdered([]byte(trimmed))
	if err != nil {
		return RewriteResult{Original: text}
	}
	encoded := gcf.EncodeGeneric(generic)
	if encoded == "" {
		return RewriteResult{Original: text}
	}
	if r.config.Stats != nil {
		r.config.Stats.Record(len(trimmed), len(encoded), 0, 0)
	}
	return r.cacheResult(trimmed, RewriteResult{Original: text, Rewritten: encoded, Converted: true})
}

// cacheResult stores a result in the cache if caching is enabled.
func (r *Rewriter) cacheResult(key string, result RewriteResult) RewriteResult {
	if r.cache != nil && result.Converted {
		r.cache[key] = result
	}
	return result
}

// decodeRequestGCF scans a JSON-RPC request line for GCF strings in tool call
// arguments and decodes them to JSON. This enables bidirectional proxying:
// the LLM can produce GCF output (63% fewer output tokens), and the upstream
// server receives JSON without modification.
func decodeRequestGCF(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line
	}

	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return line
	}
	if msg.Method != "tools/call" || msg.Params == nil {
		return line
	}

	var params struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
		Meta      json.RawMessage            `json:"_meta"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return line
	}

	modified := false
	for key, val := range params.Arguments {
		// Check if the argument value is a string starting with "GCF "
		var s string
		if err := json.Unmarshal(val, &s); err != nil {
			continue
		}
		if !strings.HasPrefix(s, "GCF ") {
			continue
		}

		// Decode GCF to native value, then re-serialize as JSON.
		decoded, err := gcf.DecodeGeneric(s)
		if err != nil {
			continue
		}
		jsonBytes, err := json.Marshal(decoded)
		if err != nil {
			continue
		}
		// Replace the GCF string with the decoded JSON value (inline, not stringified).
		params.Arguments[key] = jsonBytes
		modified = true
	}

	if !modified {
		return line
	}

	// Rebuild the request.
	paramsBytes, _ := json.Marshal(params)
	rebuilt := map[string]any{
		"jsonrpc": msg.JSONRPC,
		"id":      msg.ID,
		"method":  msg.Method,
		"params":  json.RawMessage(paramsBytes),
	}
	output, _ := json.Marshal(rebuilt)
	return string(output)
}

// tryGraphProfile attempts to parse and encode as a GCF graph payload.
func (r *Rewriter) tryGraphProfile(text string, progressFn ProgressFunc) RewriteResult {
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

	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return RewriteResult{Original: text}
	}
	if payload.Tool == "" || payload.Symbols == nil {
		return RewriteResult{Original: text}
	}

	// Decide: streaming or buffered.
	useStreaming := progressFn != nil &&
		r.config.EnableProgress &&
		len(payload.Symbols) >= r.config.StreamThreshold

	if useStreaming {
		return r.encodeStreaming(&payload, progressFn)
	}

	// Buffered encode (standard path).
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
	// Use session dedup if available (bare refs for previously-transmitted symbols).
	var encoded string
	if r.config.Session != nil {
		encoded = gcf.EncodeWithSession(p, r.config.Session)
	} else {
		encoded = gcf.Encode(p)
	}
	if r.config.Stats != nil {
		r.config.Stats.Record(len(text), len(encoded), len(p.Symbols), len(p.Edges))
	}
	return r.cacheResult(text, RewriteResult{
		Original:    text,
		Rewritten:   encoded,
		Converted:   true,
		SymbolCount: len(p.Symbols),
		EdgeCount:   len(p.Edges),
	})
}

// encodeStreaming uses StreamEncoder and emits progress callbacks.
func (r *Rewriter) encodeStreaming(payload *struct {
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
}, progressFn ProgressFunc) RewriteResult {
	var buf bytes.Buffer
	enc := gcf.NewStreamEncoder(&buf, payload.Tool, gcf.StreamOptions{
		TokenBudget: payload.TokenBudget,
		TokensUsed:  payload.TokensUsed,
	})

	total := len(payload.Symbols)
	lastPos := 0
	batchSize := r.config.StreamThreshold

	for i, s := range payload.Symbols {
		enc.WriteSymbol(gcf.Symbol{
			QualifiedName: s.QualifiedName,
			Kind:          s.Kind,
			Score:         s.Score,
			Provenance:    s.Provenance,
			Distance:      s.Distance,
		})

		// Emit progress every batchSize symbols.
		if (i+1)%batchSize == 0 {
			fragment := buf.String()[lastPos:]
			lastPos = buf.Len()
			progressFn(fragment, i+1, total)
		}
	}

	for _, e := range payload.Edges {
		enc.WriteEdge(gcf.Edge{
			Source:   e.Source,
			Target:   e.Target,
			EdgeType: e.EdgeType,
			Status:   e.Status,
		})
	}
	enc.Close()

	// Emit final fragment (edges + summary).
	if lastPos < buf.Len() {
		fragment := buf.String()[lastPos:]
		progressFn(fragment, total, total)
	}

	output := buf.String()
	if r.config.Stats != nil {
		r.config.Stats.Record(0, len(output), total, len(payload.Edges))
	}
	return RewriteResult{
		Original:    "",
		Rewritten:   output,
		Converted:   true,
		SymbolCount: total,
		EdgeCount:   len(payload.Edges),
	}
}
