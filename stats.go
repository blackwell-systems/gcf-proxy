package main

import (
	"fmt"
	"io"
	"sync/atomic"
)

// Stats tracks cumulative JSON-to-GCF conversion metrics.
type Stats struct {
	Calls       atomic.Int64
	JSONBytes   atomic.Int64
	GCFBytes    atomic.Int64
	Symbols     atomic.Int64
	Edges       atomic.Int64
	SessionDedup bool // whether session dedup is active
}

// Record adds a single conversion result to the running totals.
func (s *Stats) Record(jsonSize, gcfSize, symbols, edges int) {
	s.Calls.Add(1)
	s.JSONBytes.Add(int64(jsonSize))
	s.GCFBytes.Add(int64(gcfSize))
	s.Symbols.Add(int64(symbols))
	s.Edges.Add(int64(edges))
}

// SavedBytes returns the total bytes saved.
func (s *Stats) SavedBytes() int64 {
	return s.JSONBytes.Load() - s.GCFBytes.Load()
}

// SavedPct returns the percentage reduction.
func (s *Stats) SavedPct() float64 {
	j := s.JSONBytes.Load()
	if j == 0 {
		return 0
	}
	return float64(s.SavedBytes()) / float64(j) * 100
}

// EstTokens estimates token count using bytes/4 heuristic (cl100k_base approximation).
func estTokens(bytes int64) int64 {
	return bytes / 4
}

// WriteSummary prints the session stats to the given writer.
func (s *Stats) WriteSummary(w io.Writer) {
	calls := s.Calls.Load()
	if calls == 0 {
		return
	}
	jsonB := s.JSONBytes.Load()
	gcfB := s.GCFBytes.Load()
	saved := s.SavedBytes()
	pct := s.SavedPct()

	fmt.Fprintf(w, "\n--- gcf-proxy session stats ---\n")
	fmt.Fprintf(w, "Tool calls rewritten:  %d\n", calls)
	fmt.Fprintf(w, "Symbols processed:     %d\n", s.Symbols.Load())
	fmt.Fprintf(w, "Edges processed:       %d\n", s.Edges.Load())
	fmt.Fprintf(w, "JSON bytes in:         %s\n", fmtBytes(jsonB))
	fmt.Fprintf(w, "GCF bytes out:         %s\n", fmtBytes(gcfB))
	fmt.Fprintf(w, "Bytes saved:           %s (%.1f%%)\n", fmtBytes(saved), pct)
	fmt.Fprintf(w, "Est. tokens saved:     ~%s\n", fmtInt(estTokens(saved)))
	fmt.Fprintf(w, "-------------------------------\n")
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1_000_000:
		return fmt.Sprintf("%.1fMB", float64(b)/1_000_000)
	case b >= 1_000:
		return fmt.Sprintf("%.1fKB", float64(b)/1_000)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func fmtInt(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
