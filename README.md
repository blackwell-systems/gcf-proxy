<p align="center">
  <a href="https://gcformat.com/playground.html"><img src="https://img.shields.io/badge/playground-live-2563eb?style=for-the-badge" alt="Playground"></a>
  <a href="https://gcformat.com/guide/benchmarks.html"><img src="https://img.shields.io/badge/benchmarks-1%2C700%2B%20evals-22c55e?style=for-the-badge" alt="Benchmarks"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-333?style=for-the-badge" alt="License"></a>
</p>

# gcf-proxy

**Bidirectional MCP proxy that translates between JSON and GCF. Drop-in, zero changes to your server or client. Works with any structured data format.**

100% comprehension on every frontier model. 29% fewer tokens than TOON, 56% fewer than JSON ([2,400+ evals, 11 models, 3 providers](https://gcformat.com/guide/benchmarks.html)). Nested object flattening with opt-out for open-weight models. One line change in your MCP config.

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

### Local server (stdio)

Add `gcf-proxy` in front of any MCP server command:

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

### Remote server (HTTP)

Point `--upstream` at any Streamable HTTP MCP server:

```json
{
  "mcpServers": {
    "remote": {
      "command": "gcf-proxy",
      "args": ["--upstream", "http://host:3000/mcp"]
    }
  }
}
```

Supports JSON and SSE responses. Session ID tracking via `Mcp-Session-Id` is automatic.

### Deploy as HTTP service

`--http` turns the proxy into a remote Streamable HTTP server:

```bash
gcf-proxy --http :9090 --session your-mcp-server
```

Any MCP client that supports HTTP transport connects directly. Health check at `/health`. Chains with `--upstream` for fully remote deployments.

Both modes are bidirectional: server responses are encoded to GCF, GCF in tool call arguments is decoded to JSON. Neither side needs to change.

### Flags

| Flag | Description |
|------|-------------|
| `--session` | Enable session dedup (bare refs for previously-transmitted symbols) |
| `--cache` | Cache encoded responses for identical tool calls |
| `--delta` | Send only changed symbols when a tool's response changes slightly |
| `--no-flatten` | Disable nested object flattening (for open-weight models like LLaMA, Mistral) |
| `--min-size N` | Skip encoding for responses smaller than N bytes (default: 100) |
| `--stream-threshold N` | Min symbols before streaming mode activates (default: 5) |
| `--stats-file PATH` | Write JSON stats to file after each call |
| `--upstream URL` | Connect to a remote MCP server over HTTP |
| `--http ADDR` | Serve MCP over Streamable HTTP |
| `--no-progress` | Disable progress notifications |
| `--verbose` | Log per-call savings to stderr |

### Responses: Server (JSON) -> LLM (GCF)

```
Before: {"tool":"context_for_task","symbols":[{"qualified_name":"pkg.Auth","kind":"function","score":0.78,...},...]}
After:  GCF profile=graph tool=context_for_task budget=5000 tokens=1900 symbols=50 edges=20
        ## targets
        @0 fn pkg.Auth 0.78 lsp_resolved
        ...
```

53-71% fewer input tokens.

### Requests: LLM (GCF) -> Server (JSON)

If the LLM produces GCF in a tool call argument (63% fewer output tokens), the proxy decodes it to JSON before forwarding:

```
LLM sends:    {"tool": "process", "arguments": {"data": "GCF profile=generic\nname=Alice\nage=30\n"}}
Server gets:  {"tool": "process", "arguments": {"data": {"name": "Alice", "age": 30}}}
```

Detection is a 4-byte prefix check (`GCF `). Zero overhead. Non-GCF strings pass through untouched.

## How it works

1. Spawns your MCP server as a subprocess
2. Proxies stdin/stdout between client and server
3. **Responses**: intercepts JSON-RPC responses, re-encodes structured JSON as GCF
4. **Requests**: scans tool call arguments for GCF strings, decodes to JSON
5. Passes everything else through unchanged in both directions

## Why not modify the server?

Sometimes you can't. The server is a third-party binary, or it's maintained by another team, or you just don't want to add a dependency. gcf-proxy gives you the token savings without touching server code.

If you control the server, use the [GCF libraries](https://github.com/blackwell-systems/gcf) directly for better control over session deduplication and delta encoding.

## Benchmarks

100% general comprehension on every frontier model. 90.7% on adversarial code graphs (vs TOON 68.5%, JSON 53.6%). Wins 15/16 datasets on token benchmark.

| Eval | GCF | TOON | JSON |
|------|-----|------|------|
| **General comprehension** | **100%** | 100% | 100% |
| **Adversarial code graphs** (500 symbols) | **90.7%** | 68.5% | 53.6% |
| **Token efficiency** (16 datasets) | **15/16 wins** | 1/16 | baseline |

Reproduce comprehension eval: `git clone https://github.com/blackwell-systems/gcf-go && cd gcf-go/eval && GOWORK=off go test -run TestComprehension -v -timeout 0`

Reproduce token benchmark: `git clone https://github.com/blackwell-systems/toon && cd toon && git checkout gcf-comparison && cd benchmarks && pnpm install && pnpm benchmark:tokens`

## Links


- [Claude Code Plugin](https://github.com/blackwell-systems/gcf-claude-plugin) - one-command install for Claude Code users
- [Codex Plugin](https://github.com/blackwell-systems/gcf-codex-plugin) - one-command install for OpenAI Codex users
- [GCF Specification](https://github.com/blackwell-systems/gcf)
- [Documentation](https://gcformat.com/)
- [Cost Calculator](https://gcformat.com/calculator)
- [Go library](https://github.com/blackwell-systems/gcf-go)
- [TypeScript library](https://github.com/blackwell-systems/gcf-typescript)
- [Python library](https://github.com/blackwell-systems/gcf-python)
- [Rust library](https://github.com/blackwell-systems/gcf-rust)
- [Swift library](https://github.com/blackwell-systems/gcf-swift)
- [Kotlin library](https://github.com/blackwell-systems/gcf-kotlin)


<details>
<summary>More links</summary>

- [betterthanjson.com](https://betterthanjson.com)
- [jsonalternative.com](https://jsonalternative.com)
- [betterthantoon.com](https://betterthantoon.com)

</details>

## License

MIT - [Dayna Blackwell](https://github.com/blackwell-systems) / [GCF](https://github.com/blackwell-systems/gcf)
