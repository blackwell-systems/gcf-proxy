# MCP Proxy Guide

gcf-proxy is a bidirectional proxy that translates between JSON and GCF for any MCP server, local or remote. Neither the server nor the client needs to know about GCF.

## Install

```bash
pip install gcf-proxy          # PyPI
npm install -g @blackwell-systems/gcf-proxy   # npm
go install github.com/blackwell-systems/gcf-proxy@latest   # Go
```

## Setup

Find your MCP config:

| Client | Config location |
|--------|----------------|
| Claude Code | `~/.claude/settings.json` or project `.claude/settings.json` |
| Claude Desktop | `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) |
| VS Code (Copilot) | `.vscode/mcp.json` |
| Cursor | `.cursor/mcp.json` |

### Local server (stdio)

Add `gcf-proxy` in front of your server command:

```json
{
  "mcpServers": {
    "my-server": {
      "command": "gcf-proxy",
      "args": ["my-mcp-server", "--port", "8080"]
    }
  }
}
```

The proxy spawns your server as a subprocess and pipes stdin/stdout.

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

## How it works

The proxy translates in both directions:

```
Responses:  Server (JSON) -> gcf-proxy encodes -> LLM reads GCF   (79% input savings)
Requests:   LLM writes GCF -> gcf-proxy decodes -> Server gets JSON (63% output savings)
```

**Encode direction** (responses):
1. Server responds with JSON
2. Proxy intercepts JSON-RPC responses containing tool results
3. Structured JSON is re-encoded as GCF (graph profile for code intelligence, generic profile for everything else)
4. Client receives GCF instead of JSON

**Decode direction** (requests):
1. Client sends a tool call with GCF strings in arguments
2. Proxy detects the `GCF ` prefix (4-byte check, zero overhead)
3. GCF strings are decoded to JSON objects inline
4. Server receives JSON, never sees GCF

Non-convertible content (plain text, HTML, errors, non-GCF strings) passes through unchanged in both directions.

## What gets converted

The proxy looks for JSON-RPC responses with `result.content[].text` fields containing JSON. If the JSON has structured data (objects, arrays), it's converted to GCF:

- **JSON with `tool` + `symbols` fields**: encoded with the graph profile (local IDs, edges, distance groups)
- **Any other structured JSON**: encoded with the generic profile (pipe-separated rows, section headers)
- **Plain text, HTML, markdown**: passed through unchanged

## Before and after

**Your server outputs (JSON, 2,506 bytes):**

```json
{"tool":"context_for_task","tokenBudget":10000,"tokensUsed":3200,
"symbols":[{"qualifiedName":"github.com/org/repo/pkg.AuthMiddleware",
"kind":"function","score":0.92,"provenance":"lsp_resolved","distance":0},
...10 symbols, 8 edges...]}
```

**The LLM receives (GCF, 942 bytes):**

```
GCF profile=graph tool=context_for_task budget=10000 tokens=3200 symbols=10 edges=8
## targets
@0 fn github.com/org/repo/pkg.AuthMiddleware 0.92 lsp_resolved
@1 fn github.com/org/repo/pkg.ValidateToken 0.87 lsp_resolved
@2 type github.com/org/repo/pkg.AuthConfig 0.71 ast_inferred
## related
@3 fn github.com/org/repo/pkg.NewServer 0.65 lsp_resolved
@4 method github.com/org/repo/pkg.Server.Start 0.58 lsp_resolved
@5 type github.com/org/repo/pkg.Router 0.52 ast_inferred
## extended
@6 type github.com/org/repo/internal.TokenCache 0.41 structural
@7 iface github.com/org/repo/internal.Logger 0.35 structural
## edges [8]
@0<@3 calls
@1<@0 calls
@6<@1 references
@5<@4 references
@2<@3 references
@7<@0 implements
@8<@3 calls
@9<@3 calls
```

62% fewer tokens. Same information. Zero code changes.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--upstream <url>` | (none) | Connect to a remote MCP server over HTTP |
| `--stream-threshold N` | 5 | Min symbols before streaming progress activates |
| `--no-progress` | false | Disable progress notifications |
| `--verbose` | false | Log per-call savings to stderr |

## Streaming progress

When a client sends a `progressToken` with a tool call and the response is large (5+ symbols by default), the proxy streams GCF fragments as MCP progress notifications. The LLM gets partial context immediately.

Without `progressToken`: no streaming, backward compatible.

## Verbose mode

`gcf-proxy --verbose` logs per-call savings to stderr:

```
gcf-proxy: get_price_history              54.0KB -> 28.1KB (48% saved)
gcf-proxy: get_ticker_info                10.0KB -> 7.4KB (26% saved)

--- gcf-proxy session stats ---
Tool calls rewritten:  2
JSON bytes in:         64.0KB
GCF bytes out:         35.5KB
Bytes saved:           28.5KB (44.5%)
Est. tokens saved:     ~7.1K
-------------------------------
```

## Test it

```bash
git clone https://github.com/blackwell-systems/gcf-proxy
cd gcf-proxy && bash test.sh
```

## Troubleshooting

**"command not found: gcf-proxy"**

Make sure your Python scripts or npm global bin directory is on PATH:

```bash
which gcf-proxy
gcf-proxy --help
```

**Server works without proxy but hangs with proxy**

The proxy buffers stdout line by line. Make sure your server flushes after each JSON-RPC response.

**Responses pass through unconverted**

The proxy only converts `text` content blocks containing valid JSON objects or arrays. Plain text, markdown, and HTML pass through unchanged. This is intentional.

**Remote server not connecting**

Check the URL includes the full MCP endpoint path (e.g., `http://host:3000/mcp`, not just `http://host:3000`). The proxy POSTs JSON-RPC messages to this URL.

## When to use the proxy vs the library

| Scenario | Use |
|----------|-----|
| You can't modify the server | Proxy |
| Server is remote (HTTP) | Proxy (`--upstream`) |
| You want to test GCF savings without code changes | Proxy |
| You control the server and want session dedup/delta | Library |
| You want zero-effort adoption | Proxy |

## Links

- [GitHub](https://github.com/blackwell-systems/gcf-proxy)
- [PyPI](https://pypi.org/project/gcf-proxy/)
- [npm](https://www.npmjs.com/package/@blackwell-systems/gcf-proxy)
- [Documentation](https://gcformat.com/guide/proxy.html)
