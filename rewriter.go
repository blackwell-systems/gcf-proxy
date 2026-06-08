package main

import (
	"bytes"
	"encoding/json"
	"strings"

	gcf "github.com/blackwell-systems/gcf-go"
)

// RewriterConfig controls streaming behavior.
type RewriterConfig struct {
	StreamThreshold int    // Min symbols before triggering incremental mode (default 5)
	EnableProgress  bool   // Whether to emit progress notifications
	Stats           *Stats // Optional stats tracker
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
}

// NewRewriter creates a Rewriter with the given config.
func NewRewriter(config RewriterConfig) *Rewriter {
	if config.StreamThreshold <= 0 {
		config.StreamThreshold = 5
	}
	return &Rewriter{config: config}
}

// RewriteToolResult attempts to convert a JSON text content block to GCF.
// If progressFn is non-nil and the payload is large enough, it emits
// incremental GCF fragments via the callback.
func (r *Rewriter) RewriteToolResult(text string, progressFn ProgressFunc) RewriteResult {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return RewriteResult{Original: text}
	}

	// Try graph profile first (has tool + symbols).
	result := r.tryGraphProfile(trimmed, progressFn)
	if result.Converted {
		return result
	}

	// Fall back to generic profile (any structured JSON).
	var generic any
	if err := json.Unmarshal([]byte(trimmed), &generic); err != nil {
		return RewriteResult{Original: text}
	}
	encoded := gcf.EncodeGeneric(generic)
	if encoded == "" {
		return RewriteResult{Original: text}
	}
	if r.config.Stats != nil {
		r.config.Stats.Record(len(trimmed), len(encoded), 0, 0)
	}
	return RewriteResult{Original: text, Rewritten: encoded, Converted: true}
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
	encoded := gcf.Encode(p)
	if r.config.Stats != nil {
		r.config.Stats.Record(len(text), len(encoded), len(p.Symbols), len(p.Edges))
	}
	return RewriteResult{
		Original:    text,
		Rewritten:   encoded,
		Converted:   true,
		SymbolCount: len(p.Symbols),
		EdgeCount:   len(p.Edges),
	}
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
