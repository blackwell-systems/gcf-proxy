<p align="center">
  <a href="https://gcformat.com/playground.html"><img src="https://img.shields.io/badge/playground-live-2563eb?style=for-the-badge" alt="Playground"></a>
  <a href="https://gcformat.com/guide/benchmarks.html"><img src="https://img.shields.io/badge/benchmarks-1%2C300%2B%20evals-22c55e?style=for-the-badge" alt="Benchmarks"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-333?style=for-the-badge" alt="License"></a>
</p>

# gcf-proxy

**MCP proxy that re-encodes JSON tool responses as GCF. Drop-in, zero changes to your server.**

79% fewer tokens than JSON. 90.7% comprehension accuracy where JSON averages 53.6% ([1,300+ evals, 10 models, 3 providers](https://gcformat.com/guide/benchmarks.html)). One line change in your MCP config.

## Install

```bash
pip install gcf-proxy                                         # PyPI
npm install -g @blackwell-systems/gcf-proxy                   # npm
go install github.com/blackwell-systems/gcf-proxy@latest      # Go
```

## Try it (30 seconds, no auth)

```bash
gcf-proxy --verbose uvx yfinance-mcp
```

Use it with any MCP client. When tools return structured JSON, the proxy re-encodes to GCF and logs savings to stderr:

```
gcf-proxy: get_price_history              54.0KB -> 28.1KB (48% saved)
gcf-proxy: get_ticker_info                10.0KB -> 7.4KB (26% saved)
gcf-proxy: get_price_history              53.8KB -> 27.9KB (48% saved)

--- gcf-proxy session stats ---
Tool calls rewritten:  3
JSON bytes in:         117.8KB
GCF bytes out:         63.4KB
Bytes saved:           54.4KB (46.2%)
Est. tokens saved:     ~13.6K
-------------------------------
```

Real live stock data from Yahoo Finance. 118KB of JSON reduced to 63KB. ~13,600 tokens saved in 3 tool calls.

## Usage

Add `gcf-proxy` in front of any MCP server command in your config:

```json
{
  "mcpServers": {
    "memory": {
      "command": "gcf-proxy",
      "args": ["npx", "-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

Your server keeps outputting JSON. The LLM receives GCF. Nothing else changes.

### Before (your server outputs JSON, 12,000 tokens):

```json
{"tool":"context_for_task","symbols":[{"qualified_name":"pkg.Auth","kind":"function","score":0.78,...},...]}
```

### After (LLM receives GCF, 1,900 tokens):

```
GCF tool=context_for_task budget=5000 tokens=1900 symbols=50 edges=20
## targets
@0 fn pkg.Auth 0.78 lsp_resolved
...
## edges [20]
@0<@1 calls
```

## How it works

1. Spawns your MCP server as a subprocess
2. Proxies stdin/stdout between client and server
3. Intercepts JSON-RPC responses containing tool results
4. Detects JSON payloads with `tool` + `symbols` fields
5. Re-encodes them as GCF
6. Passes everything else through unchanged

Non-convertible responses (plain text, HTML, errors) pass through untouched.

## Why not modify the server?

Sometimes you can't. The server is a third-party binary, or it's maintained by another team, or you just don't want to add a dependency. gcf-proxy gives you the token savings without touching server code.

If you control the server, use the [GCF libraries](https://github.com/blackwell-systems/gcf) directly for better control over session deduplication and delta encoding.

## Benchmarks

GCF achieves 100% comprehension accuracy at 500 symbols where JSON scores 76.9% and TOON scores 92.3%. On TOON's own benchmark, GCF wins all 6 datasets.

| Format | Accuracy | Tokens | vs JSON |
|--------|----------|--------|---------|
| **GCF** | **90.7%** avg (10 models) | **11,090** | **79% fewer** |
| TOON | 68.5% avg | 16,378 | 69% fewer |
| JSON | 53.6% avg | 53,341 | baseline |

Reproduce comprehension eval: `git clone https://github.com/blackwell-systems/gcf-go && cd gcf-go/eval && GOWORK=off go test -run TestComprehension -v -timeout 0`

Reproduce token benchmark: `git clone https://github.com/blackwell-systems/toon && cd toon && git checkout gcf-comparison && cd benchmarks && pnpm install && pnpm benchmark:tokens`

## Links


- [GCF Specification](https://github.com/blackwell-systems/gcf)
- [Documentation](https://gcformat.com/)
- [Go library](https://github.com/blackwell-systems/gcf-go)
- [TypeScript library](https://github.com/blackwell-systems/gcf-typescript)
- [Python library](https://github.com/blackwell-systems/gcf-python)
- [Rust library](https://github.com/blackwell-systems/gcf-rust)
- [Swift library](https://github.com/blackwell-systems/gcf-swift)
- [Kotlin library](https://github.com/blackwell-systems/gcf-kotlin)

- [betterthanjson.com](https://betterthanjson.com)

## License

MIT - [Dayna Blackwell](https://github.com/blackwell-systems) / [GCF](https://github.com/blackwell-systems/gcf)
