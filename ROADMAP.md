# Roadmap

## Phase 1: Streaming Progress (stdio) ✓ DONE

The proxy emits GCF fragments as MCP progress notifications while encoding large graph payloads. The LLM gets partial context immediately instead of waiting for the full response.

**Shipped:**
- `rewriter.go`: core JSON-to-GCF conversion with StreamEncoder integration
- `jsonrpc.go`: JSON-RPC message types and progress notification builder
- `main.go`: tracks progressTokens from tool call requests, emits incremental notifications
- `--stream-threshold N`: min symbols before streaming activates (default 5)
- `--no-progress`: disable progress notifications
- 6 unit tests, mock MCP server for testing

**How it works:**
1. Client sends `tools/call` with `progressToken` in `_meta`
2. Upstream server returns JSON with `tool` + `symbols` fields
3. Proxy uses `StreamEncoder` to encode incrementally (batch size = threshold)
4. Each batch emits a progress notification with GCF fragment in `message`
5. Final response contains the complete GCF payload with `## _summary`

Without `progressToken`: behaves exactly as before (backward compatible).

---

## Phase 2: HTTP/SSE Frontend

The proxy becomes a Streamable HTTP server. Any stdio-based MCP server gets upgraded to a remote HTTP service with SSE streaming.

**Planned:**
- `frontend_http.go`: HTTP server with POST handler and SSE response writer
- `--http :9090`: listen on address as Streamable HTTP MCP endpoint
- Client POSTs tool call -> proxy forwards to upstream (stdio subprocess) -> proxy streams GCF fragments back as SSE events -> final SSE event contains complete response
- Session management via `Mcp-Session-Id` header
- Backward compatible: stdio frontend remains the default

**Use case:** Deploy any existing stdio MCP server as a remote service. Claude Desktop, web clients, and other HTTP-capable MCP clients connect directly. No changes to the upstream server.

**Architecture:**
```
HTTP Client  ──POST──▶  Proxy (:9090)  ──stdin──▶  Upstream (stdio)
             ◀──SSE───                  ◀──stdout──
```

---

## Phase 3: HTTP Backend + Session Dedup

The proxy connects to upstream servers over HTTP (not just stdio subprocess). Cross-request session deduplication via `gcf.Session`.

**Planned:**
- `backend_http.go`: HTTP client that POSTs to upstream MCP server, handles SSE responses
- `--upstream http://localhost:3000/mcp`: connect to upstream via HTTP instead of spawning subprocess
- `session.go`: cross-request `gcf.Session` state management
- `--session`: enable session deduplication (previously-transmitted symbols become bare refs)
- Session scoped to `Mcp-Session-Id` (HTTP) or process lifetime (stdio)

**Use case:** The proxy sits between a client and a remote MCP server. The upstream server retransmits full payloads every call. The proxy tracks what's been sent and replaces known symbols with bare references, delivering session savings (92.7% by 5th call) without any upstream code changes.

**Architecture:**
```
Client  ──▶  Proxy  ──HTTP POST──▶  Remote MCP Server
        ◀──         ◀──SSE/JSON───
```

---

## Phase 4: Production Hardening

Polish for production deployment.

**Planned:**
- Graceful shutdown (drain in-flight requests, close upstream connections)
- Connection pooling for HTTP backend
- Rate limiting on progress notifications (avoid flooding slow clients)
- Metrics endpoint (encoding time, savings ratio, session cache hit rate)
- SSE resume support (event IDs per spec for reconnection)
- Health check endpoint
- Configurable logging levels

---

## Mode Matrix

| Frontend | Backend | Command |
|----------|---------|---------|
| stdio | stdio subprocess | `gcf-proxy server-binary` (Phase 1, current) |
| HTTP/SSE | stdio subprocess | `gcf-proxy --http :9090 server-binary` (Phase 2) |
| stdio | HTTP upstream | `gcf-proxy --upstream http://host/mcp` (Phase 3) |
| HTTP/SSE | HTTP upstream | `gcf-proxy --http :9090 --upstream http://host/mcp` (Phase 3) |
