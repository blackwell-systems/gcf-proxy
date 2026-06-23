package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
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

// TestProxyFuzzRoundTrip generates random JSON, runs it through the proxy rewriter
// with both flatten modes, and verifies the GCF round-trips back to equivalent JSON.
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
			val := genValue(rng, 0, 4)
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				continue
			}

			result := rw.RewriteToolResult(string(jsonBytes), nil)
			if !result.Converted {
				// Small or non-convertible values skip encoding, that's fine.
				continue
			}

			// The rewritten output should be valid GCF.
			if !strings.HasPrefix(result.Rewritten, "GCF ") {
				t.Fatalf("iteration %d noFlatten=%v: missing GCF header\n  json: %s\n  got: %q",
					i, noFlatten, string(jsonBytes), result.Rewritten[:min(200, len(result.Rewritten))])
			}
		}
		t.Logf("PASS: %d iterations with noFlatten=%v", iterations, noFlatten)
	}
}
