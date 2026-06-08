# Changelog

## v0.3.0

- **Session stats**: the proxy now tracks cumulative conversion metrics and prints a summary to stderr on exit. Shows tool calls rewritten, JSON bytes in, GCF bytes out, bytes saved (with percentage), and estimated tokens saved.
- Fix comprehension accuracy figure (90.5% -> 90.7%)

## v0.2.0

- **Streaming progress**: large graph payloads emit GCF fragments as MCP progress notifications for immediate partial context delivery.
- **Generic profile support**: non-graph JSON tool responses are re-encoded via `EncodeGeneric`.
- `--stream-threshold N`: min symbols before streaming activates (default 5).
- `--no-progress`: disable progress notifications.

## v0.1.0

- Initial release. Proxies stdio MCP servers and re-encodes JSON tool responses as GCF.
