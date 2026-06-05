#!/bin/bash
# Publish @blackwell-systems/gcf-proxy packages to npm.
# Downloads binaries from GitHub Releases, injects into platform packages.
#
# Usage: ./scripts/npm-publish.sh [TAG]
set -euo pipefail

TAG="${1:-$(git describe --tags --abbrev=0)}"
VERSION="${TAG#v}"
REPO="blackwell-systems/gcf-proxy"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NPM_DIR="${SCRIPT_DIR}/../npm"

echo "Publishing @blackwell-systems/gcf-proxy@${VERSION} (tag: ${TAG})"

declare -A PLATFORMS=(
  ["darwin_arm64"]="darwin-arm64:gcf-proxy:tar.gz"
  ["darwin_amd64"]="darwin-x64:gcf-proxy:tar.gz"
  ["linux_arm64"]="linux-arm64:gcf-proxy:tar.gz"
  ["linux_amd64"]="linux-x64:gcf-proxy:tar.gz"
  ["windows_amd64"]="win32-x64:gcf-proxy.exe:zip"
  ["windows_arm64"]="win32-arm64:gcf-proxy.exe:zip"
)

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

for GOKEY in "${!PLATFORMS[@]}"; do
  IFS=: read -r NPM_SUFFIX BINARY_NAME ARCHIVE_EXT <<< "${PLATFORMS[$GOKEY]}"

  ARCHIVE="gcf-proxy-${GOKEY}.${ARCHIVE_EXT}"
  URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE}"
  PKG_DIR="${NPM_DIR}/gcf-proxy-${NPM_SUFFIX}"
  BIN_DIR="${PKG_DIR}/bin"

  echo "  [${NPM_SUFFIX}] Downloading ${ARCHIVE}..."
  curl -fsSL "$URL" -o "${TMP_DIR}/${ARCHIVE}"

  mkdir -p "$BIN_DIR"

  if [ "$ARCHIVE_EXT" = "tar.gz" ]; then
    tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "$TMP_DIR" "$BINARY_NAME"
  else
    unzip -o "${TMP_DIR}/${ARCHIVE}" "$BINARY_NAME" -d "$TMP_DIR"
  fi

  cp "${TMP_DIR}/${BINARY_NAME}" "${BIN_DIR}/${BINARY_NAME}"
  chmod +x "${BIN_DIR}/${BINARY_NAME}"
  rm -f "${TMP_DIR}/${BINARY_NAME}"
  rm -f "${BIN_DIR}/.gitignore"

  node -e "
    const fs = require('fs');
    const p = '${PKG_DIR}/package.json';
    const pkg = JSON.parse(fs.readFileSync(p));
    pkg.version = '${VERSION}';
    fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + '\n');
  "

  PKG_NAME="@blackwell-systems/gcf-proxy-${NPM_SUFFIX}"
  if npm view "${PKG_NAME}@${VERSION}" version &>/dev/null 2>&1; then
    echo "  [${NPM_SUFFIX}] Already published, skipping."
  else
    echo "  [${NPM_SUFFIX}] Publishing ${PKG_NAME}@${VERSION}..."
    npm publish "${PKG_DIR}" --access public
  fi
done

# Root package
ROOT_PKG="${NPM_DIR}/gcf-proxy/package.json"
node -e "
  const fs = require('fs');
  const pkg = JSON.parse(fs.readFileSync('${ROOT_PKG}'));
  pkg.version = '${VERSION}';
  for (const dep of Object.keys(pkg.optionalDependencies)) {
    pkg.optionalDependencies[dep] = '${VERSION}';
  }
  fs.writeFileSync('${ROOT_PKG}', JSON.stringify(pkg, null, 2) + '\n');
"

ROOT_PKG_NAME="@blackwell-systems/gcf-proxy"
if npm view "${ROOT_PKG_NAME}@${VERSION}" version &>/dev/null 2>&1; then
  echo "  [root] Already published, skipping."
else
  echo "  [root] Publishing ${ROOT_PKG_NAME}@${VERSION}..."
  npm publish "${NPM_DIR}/gcf-proxy" --access public
fi

echo ""
echo "Done. Install with: npm install -g @blackwell-systems/gcf-proxy"
