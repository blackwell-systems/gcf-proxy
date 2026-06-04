#!/bin/bash
# gcf-proxy savings test
# Builds the proxy, runs a mock MCP server through it, and measures token savings.

set -e

cd "$(dirname "$0")"

# Build
GOWORK=off go build -o /tmp/gcf-proxy-test .

# Mock MCP server: outputs a realistic JSON-RPC tool response (10 symbols, 8 edges)
MOCK_SERVER=$(mktemp)
cat > "$MOCK_SERVER" << 'MOCKEOF'
#!/bin/bash
while IFS= read -r line; do
cat << 'JSON'
{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"tool\":\"context_for_task\",\"tokenBudget\":10000,\"tokensUsed\":3200,\"symbols\":[{\"qualifiedName\":\"github.com/org/repo/internal/auth.Middleware\",\"kind\":\"function\",\"score\":0.92,\"provenance\":\"lsp_resolved\",\"distance\":0},{\"qualifiedName\":\"github.com/org/repo/internal/auth.ValidateToken\",\"kind\":\"function\",\"score\":0.87,\"provenance\":\"lsp_resolved\",\"distance\":0},{\"qualifiedName\":\"github.com/org/repo/internal/auth.Config\",\"kind\":\"type\",\"score\":0.71,\"provenance\":\"ast_inferred\",\"distance\":0},{\"qualifiedName\":\"github.com/org/repo/internal/server.New\",\"kind\":\"function\",\"score\":0.65,\"provenance\":\"lsp_resolved\",\"distance\":1},{\"qualifiedName\":\"github.com/org/repo/internal/server.Server.Start\",\"kind\":\"method\",\"score\":0.58,\"provenance\":\"lsp_resolved\",\"distance\":1},{\"qualifiedName\":\"github.com/org/repo/internal/server.Router\",\"kind\":\"type\",\"score\":0.52,\"provenance\":\"ast_inferred\",\"distance\":1},{\"qualifiedName\":\"github.com/org/repo/internal/cache.TokenCache\",\"kind\":\"type\",\"score\":0.41,\"provenance\":\"structural\",\"distance\":2},{\"qualifiedName\":\"github.com/org/repo/internal/log.Logger\",\"kind\":\"interface\",\"score\":0.35,\"provenance\":\"structural\",\"distance\":2},{\"qualifiedName\":\"github.com/org/repo/internal/middleware.RateLimit\",\"kind\":\"function\",\"score\":0.31,\"provenance\":\"lsp_resolved\",\"distance\":2},{\"qualifiedName\":\"github.com/org/repo/internal/middleware.CORS\",\"kind\":\"function\",\"score\":0.28,\"provenance\":\"lsp_resolved\",\"distance\":2}],\"edges\":[{\"source\":\"github.com/org/repo/internal/server.New\",\"target\":\"github.com/org/repo/internal/auth.Middleware\",\"edgeType\":\"calls\"},{\"source\":\"github.com/org/repo/internal/auth.Middleware\",\"target\":\"github.com/org/repo/internal/auth.ValidateToken\",\"edgeType\":\"calls\"},{\"source\":\"github.com/org/repo/internal/auth.ValidateToken\",\"target\":\"github.com/org/repo/internal/cache.TokenCache\",\"edgeType\":\"references\"},{\"source\":\"github.com/org/repo/internal/server.Server.Start\",\"target\":\"github.com/org/repo/internal/server.Router\",\"edgeType\":\"references\"},{\"source\":\"github.com/org/repo/internal/server.New\",\"target\":\"github.com/org/repo/internal/auth.Config\",\"edgeType\":\"references\"},{\"source\":\"github.com/org/repo/internal/auth.Middleware\",\"target\":\"github.com/org/repo/internal/log.Logger\",\"edgeType\":\"implements\"},{\"source\":\"github.com/org/repo/internal/server.New\",\"target\":\"github.com/org/repo/internal/middleware.RateLimit\",\"edgeType\":\"calls\"},{\"source\":\"github.com/org/repo/internal/server.New\",\"target\":\"github.com/org/repo/internal/middleware.CORS\",\"edgeType\":\"calls\"}]}"}]}}
JSON
done
MOCKEOF
chmod +x "$MOCK_SERVER"

# Capture responses
echo "request" | bash "$MOCK_SERVER" > /tmp/gcf-test-json.txt
echo "request" | /tmp/gcf-proxy-test bash "$MOCK_SERVER" > /tmp/gcf-test-gcf.txt

# Extract text content
extract_text() {
  python3 -c "import json,sys; print(json.loads(open(sys.argv[1]).read())['result']['content'][0]['text'])" "$1"
}

JSON_TEXT=$(extract_text /tmp/gcf-test-json.txt)
GCF_TEXT=$(extract_text /tmp/gcf-test-gcf.txt)

JSON_BYTES=$(echo -n "$JSON_TEXT" | wc -c | tr -d ' ')
GCF_BYTES=$(echo -n "$GCF_TEXT" | wc -c | tr -d ' ')
JSON_TOKENS=$((JSON_BYTES / 4))
GCF_TOKENS=$((GCF_BYTES / 4))
SAVINGS=$(python3 -c "print(f'{100*(1-$GCF_BYTES/$JSON_BYTES):.1f}')")

echo ""
echo "gcf-proxy savings test: 10 symbols, 8 edges"
echo "============================================"
echo ""
echo "Without proxy (JSON, what the LLM receives):"
echo "----------------------------------------------"
echo "$JSON_TEXT" | python3 -m json.tool 2>/dev/null | head -12
echo "  ... ($(echo "$JSON_TEXT" | python3 -c "import json,sys; d=json.loads(sys.stdin.read()); print(len(d.get('symbols',[])))") symbols, $(echo "$JSON_TEXT" | python3 -c "import json,sys; d=json.loads(sys.stdin.read()); print(len(d.get('edges',[])))") edges)"
echo ""
echo "With proxy (GCF, what the LLM receives):"
echo "----------------------------------------------"
echo "$GCF_TEXT"
echo ""
echo "============================================"
printf "  %-6s %6d bytes  ~%d tokens\n" "JSON" "$JSON_BYTES" "$JSON_TOKENS"
printf "  %-6s %6d bytes  ~%d tokens\n" "GCF" "$GCF_BYTES" "$GCF_TOKENS"
echo ""
echo "  Savings: ${SAVINGS}% fewer tokens"
echo "============================================"

# Cleanup
rm -f "$MOCK_SERVER" /tmp/gcf-proxy-test /tmp/gcf-test-json.txt /tmp/gcf-test-gcf.txt
