# Changelog

## v0.6.0

- **HTTP backend**: `--upstream http://host:3000/mcp` connects to remote MCP servers over Streamable HTTP
- Supports both JSON and SSE responses from upstream
- Session ID tracking via `Mcp-Session-Id` header
- Same bidirectional GCF translation as stdio mode
- MCP config: `{"command": "gcf-proxy", "args": ["--upstream", "http://host:3000/mcp"]}`

## v0.5.0

- **Bidirectional GCF proxy**: tool call arguments containing GCF strings are automatically decoded to JSON before forwarding to the upstream server. The LLM can now produce GCF output (63% fewer output tokens) while the server receives JSON unchanged.
- **Key order preservation**: generic profile encoding now preserves JSON key insertion order via `ParseJSONOrdered`
- Bumped `gcf-go` to v1.0.2

## v0.4.0

- **GCF v2.0**: bumped `gcf-go` dependency from v0.5.0 to v1.0.0
- All output now uses v2.0 wire format: `GCF profile=graph` headers, `##! summary` trailers, scalar quoting, lossless generic profile
- No proxy code changes needed; the library handles all v2.0 encoding
- Test expectations updated for `profile=graph` header

## v0.3.0

- **Session stats**: the proxy tracks cumulative conversion metrics and prints a summary to stderr on exit. Shows tool calls rewritten, JSON bytes in, GCF bytes out, bytes saved (with percentage), and estimated tokens saved.
- **`--verbose`**: per-call savings logged to stderr with tool name, JSON size, GCF size, and percentage saved.
- Fix comprehension accuracy figure (90.5% -> 90.7%)

## v0.2.0

- **Streaming progress**: large graph payloads emit GCF fragments as MCP progress notifications for immediate partial context delivery.
- **Generic profile support**: non-graph JSON tool responses are re-encoded via `EncodeGeneric`.
- `--stream-threshold N`: min symbols before streaming activates (default 5).
- `--no-progress`: disable progress notifications.

## v0.1.0

- Initial release. Proxies stdio MCP servers and re-encodes JSON tool responses as GCF.
