# gcf-proxy

Bidirectional MCP proxy that translates between [GCF](https://gcformat.com/) and JSON. Drop-in, zero code changes to your server or client.

**79% fewer input tokens. 63% fewer output tokens. 90.7% comprehension accuracy across 10 models and 3 providers. 1,300+ LLM evaluations. Zero training.**

Docs: [gcformat.com](https://gcformat.com/) · [Proxy Guide](https://gcformat.com/guide/proxy.html) · [Playground](https://gcformat.com/playground.html) · [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)

## Install

```bash
npm install -g @blackwell-systems/gcf-proxy
```

## Setup (one line change)

**Before:**
```json
{"mcpServers": {"yours": {"command": "your-mcp-server"}}}
```

**After:**
```json
{"mcpServers": {"yours": {"command": "gcf-proxy", "args": ["your-mcp-server"]}}}
```

Works with Claude Code, Claude Desktop, VS Code, Cursor, and any MCP client.

## What it does

Translates in both directions:

```
Responses:  Your Server (JSON) -> gcf-proxy encodes -> LLM reads GCF   (79% input savings)
Requests:   LLM writes GCF    -> gcf-proxy decodes -> Your Server (JSON) (63% output savings)
```

- **Responses**: JSON tool results from the server are encoded as GCF
- **Requests**: GCF strings in tool call arguments are decoded to JSON (4-byte prefix check, zero overhead)
- Non-convertible content passes through unchanged in both directions
- Neither the server nor the client needs to know about GCF

## Benchmarks

| Format | Accuracy | Tokens | vs JSON |
|--------|----------|--------|---------|
| **GCF** | **90.7%** avg (10 models) | **11,090** | **79% fewer** |
| TOON | 68.5% avg | 16,378 | 69% fewer |
| JSON | 53.6% avg | 53,341 | baseline |

## Also available on

- PyPI: `pip install gcf-proxy`
- Go: `go install github.com/blackwell-systems/gcf-proxy@latest`

## Links

- [Full Setup Guide](https://gcformat.com/guide/proxy.html)
- [GCF Specification](https://gcformat.com/reference/spec.html)
- [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)
- [GitHub](https://github.com/blackwell-systems/gcf-proxy)
