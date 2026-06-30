# gcf-proxy

Bidirectional MCP proxy that translates between [GCF](https://gcformat.com/) and JSON. Drop-in, zero code changes to your server or client.

**50-92% fewer tokens. 100% comprehension on every frontier model and 3 providers. 2,400+ LLM evaluations. Zero training.**

Docs: [gcformat.com](https://gcformat.com/) · [Proxy Guide](https://gcformat.com/guide/proxy.html) · [Playground](https://gcformat.com/playground.html) · [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)

## Install

```bash
pip install gcf-proxy
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
Responses:  Your Server (JSON) -> gcf-proxy encodes -> LLM reads GCF   (50-92% savings)
Requests:   LLM writes GCF    -> gcf-proxy decodes -> Your Server (JSON) (63% output savings)
```

- **Responses**: JSON tool results from the server are encoded as GCF
- **Requests**: GCF strings in tool call arguments are decoded to JSON (4-byte prefix check, zero overhead)
- Non-convertible content passes through unchanged in both directions
- Neither the server nor the client needs to know about GCF
- `--no-flatten` uses expanded encoding for nested objects (open-weight models currently comprehend this form better; GCF still outperforms JSON either way)

## Savings

```
JSON   2,506 bytes  ~626 tokens
GCF      916 bytes  ~229 tokens   (63% fewer)
```

With session dedup (92.7% by 5th call) and delta encoding (81.2%), use the [GCF libraries](https://gcformat.com/ecosystem/implementations.html) directly.

## Benchmarks

| Eval | GCF | TOON | JSON |
|------|-----|------|------|
| **General comprehension** | **100%** | 100% | 100% |
| **Adversarial code graphs** (500 symbols) | **91.2%** | 68.8% | 54.1% |
| **Token efficiency** (16 datasets) | **15/16 wins** | 1/16 | baseline |

## Also available on

- npm: `npm install -g @blackwell-systems/gcf-proxy`
- Go: `go install github.com/blackwell-systems/gcf-proxy@latest`

## Links

- [Full Setup Guide](https://gcformat.com/guide/proxy.html)
- [GCF Specification](https://gcformat.com/reference/spec.html)
- [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)
- [GitHub](https://github.com/blackwell-systems/gcf-proxy)
