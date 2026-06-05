#!/usr/bin/env bash
# Queries PyPI, npm, GitHub releases for gcf-proxy download stats.
# Generates assets/downloads-badge.json for shields.io endpoint badge.
set -euo pipefail

NPM_PKG="@blackwell-systems/gcf-proxy"
PYPI_PKG="gcf-proxy"
REPO="blackwell-systems/gcf-proxy"
OUT="${1:-assets/downloads-badge.json}"
CACHE="${OUT%.json}.cache"

UA="gcf-proxy-stats/1.0 (https://github.com/blackwell-systems/gcf-proxy)"

npm_total=$(curl -sf --max-time 10 "https://api.npmjs.org/downloads/point/2000-01-01:2030-01-01/${NPM_PKG}" \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['downloads'])" 2>/dev/null || echo "?")

pypi_total=$(curl -sf -A "$UA" --max-time 10 "https://pypistats.org/api/packages/${PYPI_PKG}/overall" \
  | python3 -c "import json,sys; print(sum(r['downloads'] for r in json.load(sys.stdin).get('data',[])))" 2>/dev/null || echo "?")

gh_total=$(gh api "repos/${REPO}/releases" --jq '[.[].assets[].download_count] | add // 0' 2>/dev/null || echo "?")

# High-water mark cache
read_cache() { local key="$1"; [[ -f "$CACHE" ]] && grep "^${key}=" "$CACHE" 2>/dev/null | cut -d= -f2; }
use_or_cache() {
  local key="$1" val="$2" prev; prev=$(read_cache "$key"); prev="${prev:-0}"
  if [[ "$val" != "?" && "$val" != "--" ]]; then
    if [[ "$prev" != "?" && "$prev" != "--" && "$prev" != "0" ]]; then
      if (( val >= prev )); then echo "$val"; else echo "$prev"; fi
    else echo "$val"; fi; return; fi
  if [[ "$prev" != "0" && "$prev" != "--" && "$prev" != "?" ]]; then echo "$prev"; else echo "$val"; fi
}

npm_total=$(use_or_cache npm "$npm_total")
pypi_total=$(use_or_cache pypi "$pypi_total")
gh_total=$(use_or_cache gh "$gh_total")

cat > "$CACHE" << EOF
npm=${npm_total}
pypi=${pypi_total}
gh=${gh_total}
EOF

cumulative=0
for v in "$npm_total" "$pypi_total" "$gh_total"; do
  [[ "$v" != "?" && "$v" != "--" ]] && cumulative=$((cumulative + v))
done
(( cumulative == 0 )) && cumulative="?"

fmt() { printf "%'d" "$1" 2>/dev/null || echo "$1"; }
cumulative_fmt=$(fmt "$cumulative" 2>/dev/null || echo "$cumulative")

mkdir -p "$(dirname "$OUT")"
cat > "$OUT" << EOF
{
  "schemaVersion": 1,
  "label": "downloads",
  "message": "${cumulative_fmt}",
  "color": "1e3a5f"
}
EOF

echo "gcf-proxy downloads: npm=${npm_total} pypi=${pypi_total} gh=${gh_total} total=${cumulative}"
