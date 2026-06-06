# gcf-proxy

MCP proxy that re-encodes JSON tool responses as [GCF](https://gcformat.com/) — the most token-efficient wire format for LLMs. Drop-in, zero code changes. A TOON/JSON alternative that saves 63-79% of tokens.

**79% fewer tokens than JSON. 34% fewer than TOON. 100% comprehension accuracy (13/13) where JSON scores 76.9% and TOON scores 92.3%.**

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

Your server keeps outputting JSON. The LLM receives GCF. Nothing else changes.

Works with Claude Code, Claude Desktop, VS Code, Cursor, and any MCP client.

## What it does

```
LLM  ←──  GCF  ←──  gcf-proxy  ←──  JSON  ←──  Your Server
```

1. Spawns your MCP server as a subprocess
2. Proxies stdin/stdout between client and server
3. Detects JSON payloads in tool responses
4. Re-encodes as GCF (graph profile for code intelligence, generic profile for everything else)
5. Non-convertible responses (text, HTML, errors) pass through unchanged

## Savings

Tested on a real MCP tool response (10 symbols, 8 edges):

```
JSON   2,506 bytes  ~626 tokens
GCF      916 bytes  ~229 tokens

Savings: 63% fewer tokens
```

On a real agent-lsp blast_radius response (7 symbols, 47 callers):

```
JSON   6,515 bytes  ~1,628 tokens
GCF    4,866 bytes  ~1,216 tokens

Savings: 25% fewer tokens (generic encoding, no graph profile)
```

## When to use

- You can't modify the server (third-party binary, another team's code)
- You want to test GCF savings without writing any code
- You want zero-effort adoption on any existing MCP server

For session deduplication (92.7% savings) and delta encoding (81.2% savings), use the [GCF libraries](https://gcformat.com/ecosystem/implementations.html) directly.

## Also available on

- npm: `npm install -g @blackwell-systems/gcf-proxy`
- Go: `go install github.com/blackwell-systems/gcf-proxy@latest`

## Benchmarks

| Format | Accuracy | Tokens | vs JSON |
|--------|----------|--------|---------|
| **GCF** | **100%** (13/13) | **11,090** | **79% fewer** |
| TOON | 92.3% (12/13) | 16,378 | 69% fewer |
| JSON | 76.9% (10/13) | 53,341 | baseline |

GCF wins all 6 datasets on TOON's own benchmark. 42% smaller on semi-uniform data, 34% on mixed-structure.

Reproduce comprehension eval: `git clone https://github.com/blackwell-systems/gcf-go && cd gcf-go/eval && GOWORK=off go test -run TestComprehension -v -timeout 0`

Reproduce token benchmark: `git clone https://github.com/blackwell-systems/toon && cd toon && git checkout gcf-comparison && cd benchmarks && pnpm install && pnpm benchmark:tokens`

## Links

- [Full Setup Guide](https://gcformat.com/guide/proxy.html)
- [GCF Specification](https://gcformat.com/reference/spec.html)
- [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)
- [Playground](https://gcformat.com/playground.html)
- [GitHub](https://github.com/blackwell-systems/gcf-proxy)
