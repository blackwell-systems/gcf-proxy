<p align="center">
  <a href="https://github.com/blackwell-systems"><img src="https://raw.githubusercontent.com/blackwell-systems/blackwell-docs-theme/main/badge-trademark.svg" alt="Blackwell Systems"></a>
  <a href="https://github.com/blackwell-systems/gcf-proxy"><img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/blackwell-systems/gcf-proxy/main/assets/downloads-badge.json" alt="Downloads"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
</p>

# gcf-proxy

**MCP proxy that re-encodes JSON tool responses as GCF. Drop-in, zero changes to your server.**

84% fewer tokens. 100% comprehension accuracy. One line change in your MCP config.

## Install

```bash
pip install gcf-proxy                                         # PyPI
npm install -g @blackwell-systems/gcf-proxy                   # npm
go install github.com/blackwell-systems/gcf-proxy@latest      # Go
```

## Usage

```json
{
  "mcpServers": {
    "memory": {
      "command": "gcf-proxy",
      "args": ["memory-mcp-server-go"]
    }
  }
}
```

That's it. Your server keeps outputting JSON. The LLM receives GCF. Nothing else changes.

### Before (your server outputs JSON, 12,000 tokens):

```json
{"tool":"context_for_task","symbols":[{"qualified_name":"pkg.Auth","kind":"function","score":0.78,...},...]}
```

### After (LLM receives GCF, 1,900 tokens):

```
GCF tool=context_for_task budget=5000 tokens=1900 symbols=50
## targets
@0 fn pkg.Auth 0.78 lsp_resolved
...
## edges
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

## Links

- [GCF Specification](https://github.com/blackwell-systems/gcf)
- [Documentation](https://gcformat.com/)
- [Go library](https://github.com/blackwell-systems/gcf-go)
- [TypeScript library](https://github.com/blackwell-systems/gcf-typescript)
- [Python library](https://github.com/blackwell-systems/gcf-python)

## License

MIT
