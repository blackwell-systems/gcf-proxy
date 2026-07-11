package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	gcf "github.com/blackwell-systems/gcf-go"
)

func getIterations(defaultN int) int {
	if s := os.Getenv("GCF_ITERATIONS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return defaultN
}

// genString produces a random string with adversarial characters including >.
func genString(rng *rand.Rand) string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789 |,=\"\\#@\n\t~^+-.>"
	n := rng.Intn(20)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(chars[rng.Intn(len(chars))])
	}
	return b.String()
}

func genBareKey(rng *rand.Rand) string {
	chars := "abcdefghijklmnopqrstuvwxyz_>"
	n := 1 + rng.Intn(8)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(chars[rng.Intn(len(chars))])
	}
	return b.String()
}

func genScalar(rng *rand.Rand) any {
	switch rng.Intn(5) {
	case 0:
		return nil
	case 1:
		return rng.Float64() < 0.5
	case 2:
		return rng.Intn(2000) - 1000
	case 3:
		return rng.Float64()*2000 - 1000
	default:
		return genString(rng)
	}
}

func genValue(rng *rand.Rand, depth, maxDepth int) any {
	if depth >= maxDepth {
		return genScalar(rng)
	}
	switch rng.Intn(8) {
	case 0:
		return nil
	case 1:
		return rng.Float64() < 0.5
	case 2:
		return genScalar(rng)
	case 3:
		return genString(rng)
	case 4, 5:
		n := rng.Intn(5)
		m := make(map[string]any, n)
		for i := 0; i < n; i++ {
			m[genBareKey(rng)] = genValue(rng, depth+1, maxDepth)
		}
		return m
	case 6, 7:
		n := rng.Intn(5)
		arr := make([]any, n)
		for i := range arr {
			arr[i] = genValue(rng, depth+1, maxDepth)
		}
		return arr
	default:
		return genScalar(rng)
	}
}

// flatShape is a fixed nested schema (scalar leaf or named sub-shapes) used to
// generate aligned arrays whose shared fields are nested objects with null at
// depth — the v3.2 flatten path that genValue (random keys, scalar tabular) never
// produces, and where a nested-null losslessness bug otherwise hides.
type flatShape struct {
	scalar bool
	sub    []flatEntry
}
type flatEntry struct {
	key   string
	shape flatShape
}

func genFlatShape(rng *rand.Rand, depth, maxDepth int) flatShape {
	if depth >= maxDepth || rng.Float64() < 0.45 {
		return flatShape{scalar: true}
	}
	seen := map[string]bool{}
	var sub []flatEntry
	for n := 1 + rng.Intn(3); n > 0; n-- {
		k := genBareKey(rng)
		if seen[k] {
			continue
		}
		seen[k] = true
		sub = append(sub, flatEntry{k, genFlatShape(rng, depth+1, maxDepth)})
	}
	if len(sub) == 0 {
		return flatShape{scalar: true}
	}
	return flatShape{sub: sub}
}

func materializeFlatShape(rng *rand.Rand, s flatShape) any {
	if s.scalar {
		return genScalar(rng)
	}
	m := make(map[string]any, len(s.sub))
	for _, e := range s.sub {
		// A nested sub-object is sometimes null (intermediate null) instead of an object.
		if !e.shape.scalar && rng.Float64() < 0.3 {
			m[e.key] = nil
		} else {
			m[e.key] = materializeFlatShape(rng, e.shape)
		}
	}
	return m
}

func genFlattenableArray(rng *rand.Rand) any {
	schema := []flatEntry{{"id", flatShape{scalar: true}}}
	seen := map[string]bool{"id": true}
	hasNested := false
	for n := 1 + rng.Intn(3); n > 0; n-- {
		k := genBareKey(rng)
		if seen[k] {
			continue
		}
		seen[k] = true
		s := genFlatShape(rng, 1, 3)
		if !s.scalar {
			hasNested = true
		}
		schema = append(schema, flatEntry{k, s})
	}
	if !hasNested {
		schema = append(schema, flatEntry{genBareKey(rng), flatShape{sub: []flatEntry{
			{genBareKey(rng), flatShape{sub: []flatEntry{{genBareKey(rng), flatShape{scalar: true}}}}},
		}}})
	}
	// Keep rows below the stream threshold so the output stays generic-profile and
	// is round-trip-checkable below.
	rows := 2 + rng.Intn(3)
	arr := make([]any, 0, rows)
	for i := 0; i < rows; i++ {
		row := map[string]any{}
		for _, e := range schema {
			x := rng.Float64()
			switch {
			case x < 0.12: // field absent this row
			case x < 0.24:
				row[e.key] = nil // field present-null (top-level null)
			default:
				row[e.key] = materializeFlatShape(rng, e.shape)
			}
		}
		arr = append(arr, row)
	}
	return arr
}

// TestProxyFuzzRoundTrip runs random JSON (and aligned nested-object arrays with
// null at depth) through the proxy rewriter in both flatten modes, decodes the GCF
// back, and verifies it round-trips to an equivalent value. Decoding + comparing
// (not just checking the header) is what catches a losslessness regression in the
// underlying codec, e.g. a null nested object silently dropped on decode.
func TestProxyFuzzRoundTrip(t *testing.T) {
	iterations := getIterations(10_000)
	rng := rand.New(rand.NewSource(42))

	for _, noFlatten := range []bool{false, true} {
		rw := NewRewriter(RewriterConfig{
			StreamThreshold: 5,
			MinSize:         0,
			NoFlatten:       noFlatten,
		})

		for i := 0; i < iterations; i++ {
			// Every third value is an aligned nested array to exercise the flatten path.
			var val any
			if i%3 == 0 {
				val = genFlattenableArray(rng)
			} else {
				val = genValue(rng, 0, 4)
			}
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				continue
			}

			result := rw.RewriteToolResult(string(jsonBytes), nil)
			if !result.Converted {
				continue
			}
			if !strings.HasPrefix(result.Rewritten, "GCF ") {
				t.Fatalf("iteration %d noFlatten=%v: missing GCF header\n  json: %s",
					i, noFlatten, string(jsonBytes))
			}

			// Deep round-trip check for generic-profile output (streaming/graph
			// outputs use a different shape and keep the header check only).
			if !strings.HasPrefix(result.Rewritten, "GCF profile=generic") {
				continue
			}
			decoded, err := gcf.DecodeGeneric(result.Rewritten)
			if err != nil {
				t.Fatalf("iteration %d noFlatten=%v: decode failed: %v\n  json: %s\n  gcf: %q",
					i, noFlatten, err, string(jsonBytes), result.Rewritten[:min(300, len(result.Rewritten))])
			}
			// Normalize both through JSON so number types and key order don't matter.
			decBytes, _ := json.Marshal(decoded)
			var want, got any
			_ = json.Unmarshal(jsonBytes, &want)
			_ = json.Unmarshal(decBytes, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("iteration %d noFlatten=%v: round-trip mismatch\n  in:  %s\n  out: %s\n  gcf: %q",
					i, noFlatten, string(jsonBytes), string(decBytes), result.Rewritten[:min(300, len(result.Rewritten))])
			}
		}
		t.Logf("PASS: %d iterations (round-trip verified) with noFlatten=%v", iterations, noFlatten)
	}
}
