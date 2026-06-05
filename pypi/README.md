# gcf-proxy

MCP proxy that re-encodes JSON tool responses as [GCF](https://gcformat.com/). Drop-in, zero code changes to your server. 79% fewer tokens than JSON.

## Install

```bash
pip install gcf-proxy
```

## Usage

```json
{
  "mcpServers": {
    "yours": {
      "command": "gcf-proxy",
      "args": ["your-mcp-server"]
    }
  }
}
```

Your server keeps outputting JSON. The LLM receives GCF. Nothing else changes.

## How it works

1. Spawns your MCP server as a subprocess
2. Proxies stdin/stdout
3. Detects JSON payloads with structured data in tool responses
4. Re-encodes them as GCF (graph profile for symbols+edges, tabular profile for everything else)
5. Passes non-convertible responses through unchanged

## Links

- [Documentation](https://gcformat.com/)
- [Playground](https://gcformat.com/playground.html)
- [GCF vs TOON](https://gcformat.com/guide/vs-toon.html)
- [GitHub](https://github.com/blackwell-systems/gcf-proxy)
