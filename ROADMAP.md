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
5. Final response contains the complete GCF payload with `##! summary`

Without `progressToken`: behaves exactly as before (backward compatible).

---

## Phase 1.5: Generic Streaming

The proxy currently streams graph profile responses via `StreamEncoder` but encodes generic responses in one shot via `EncodeGeneric`. With `GenericStreamEncoder` now available in gcf-go, the proxy can stream large JSON arrays incrementally too.

**Planned:**
- Incremental JSON array parsing: detect large arrays in tool responses, parse element-by-element
- Use `GenericStreamEncoder.BeginArray`/`WriteRow` to emit rows as each JSON element is parsed
- Emit progress notifications with tabular GCF fragments (same mechanism as graph streaming)
- Threshold-based: only activate for arrays above `--stream-threshold` elements

**Use case:** MCP servers returning large JSON arrays (search results, database queries, log entries). The proxy re-encodes each row as GCF generic the instant it's parsed from JSON, without buffering the full array.

---

## Phase 2: HTTP/SSE Frontend ✓ DONE

The proxy serves MCP over Streamable HTTP. Any stdio-based MCP server becomes a remote HTTP service.

**Shipped:**
- `frontend_http.go`: HTTP server with POST handler, SSE response writer, health check
- `--http :9090`: listen on address as Streamable HTTP MCP endpoint
- Chains with `--upstream` for fully remote deployments
- All features work in HTTP mode: bidirectional translation, session dedup, streaming progress
- Health check at `/health`

**Architecture:**
```
HTTP Client  ──POST──▶  Proxy (:9090)  ──stdin──▶  Upstream (stdio)
             ◀──SSE───                  ◀──stdout──

HTTP Client  ──POST──▶  Proxy (:9090)  ──HTTP──▶  Remote MCP Server
             ◀──SSE───                  ◀──JSON──
```

---

## Phase 3: HTTP Backend ✓ DONE

The proxy connects to upstream servers over HTTP (not just stdio subprocess).

**Shipped:**
- `backend_http.go`: HTTP client that POSTs to upstream MCP server, handles both JSON and SSE responses
- `--upstream http://localhost:3000/mcp`: connect to upstream via HTTP instead of spawning subprocess
- `Mcp-Session-Id` header tracking (captured from upstream, sent on subsequent requests)
- Same bidirectional GCF translation as stdio mode

**Architecture:**
```
Client  ──▶  Proxy  ──HTTP POST──▶  Remote MCP Server
        ◀──         ◀──SSE/JSON───
```

---

## Phase 4: Session Dedup ✓ DONE

Cross-call session deduplication for graph payloads. Previously-transmitted symbols become bare references on subsequent calls.

**Shipped:**
- `--session`: enable session dedup (persists for proxy lifetime)
- GCF-in path: decode upstream GCF, re-encode with session bare refs
- JSON-in path: encode JSON as GCF with session tracking
- Proven end-to-end with agent-lsp on real TypeScript codebase

**Results (5 sequential blast_radius calls):**
```
Call 1: 5,730 bytes (94 symbols, baseline)
Call 2: 3,450 bytes (94 bare refs, 40% saved)
Call 3: 4,887 bytes (18 bare refs, 15% saved)
Call 4: 3,450 bytes (94 bare refs, 40% saved)
Call 5: 6,335 bytes (175 bare refs, 41% saved)
```

**Use case:** The proxy sits between a client and any MCP server (local or remote). The server retransmits full payloads every call. The proxy tracks what's been sent and replaces known symbols with bare references. Zero server changes required.

---

## Phase 5: Production Hardening

Polish for production deployment. Build when demand arrives.

**Planned:**
- Graceful shutdown (drain in-flight requests, close upstream connections)
- Connection pooling for HTTP backend
- Rate limiting on progress notifications (avoid flooding slow clients)
- Metrics endpoint (encoding time, savings ratio, session cache hit rate)
- SSE resume support (event IDs per spec for reconnection)
- Configurable logging levels

---

## Phase 6: Aggregate Proxy

Single proxy process wrapping multiple MCP servers. One shared session across all backends.

**Planned:**
- `gcf-proxy --aggregate server-a server-b server-c`: multiplex multiple servers behind one proxy
- Cross-server session dedup: symbols from server-a become bare refs when server-b returns the same symbols
- Unified stats across all backends
- Tool name routing: merge tool registrations from all servers, route calls to the correct backend
- Tool name collision handling

**Why it matters:** In a real agent workflow, the same code graph symbols appear across blast_radius, find_callers, explore_symbol, and find_references. A shared session across all tools compounds faster than per-server sessions.

---

## Mode Matrix

| Frontend | Backend | Session | Command |
|----------|---------|---------|---------|
| stdio | stdio subprocess | off | `gcf-proxy server-binary` |
| stdio | stdio subprocess | on | `gcf-proxy --session server-binary` |
| stdio | HTTP upstream | off | `gcf-proxy --upstream http://host/mcp` |
| stdio | HTTP upstream | on | `gcf-proxy --session --upstream http://host/mcp` |
| HTTP/SSE | stdio subprocess | on | `gcf-proxy --http :9090 --session server-binary` |
| HTTP/SSE | HTTP upstream | on | `gcf-proxy --http :9090 --session --upstream http://host/mcp` |
