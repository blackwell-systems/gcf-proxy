#!/bin/bash
# Build platform-specific wheels for PyPI distribution.
# Downloads Go binaries from GitHub Releases, injects into Python package.
#
# Usage: ./scripts/pypi-build-wheels.sh [TAG]
set -euo pipefail

TAG="${1:-$(git describe --tags --abbrev=0)}"
VERSION="${TAG#v}"
REPO="blackwell-systems/gcf-proxy"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYPI_DIR="${SCRIPT_DIR}/../pypi"
DIST_DIR="${PYPI_DIR}/dist"

echo "Building gcf-proxy wheels for ${VERSION} (tag: ${TAG})"

sed -i.bak "s/^version = .*/version = \"${VERSION}\"/" "${PYPI_DIR}/pyproject.toml"
rm -f "${PYPI_DIR}/pyproject.toml.bak"
sed -i.bak "s/^__version__ = .*/__version__ = \"${VERSION}\"/" "${PYPI_DIR}/gcf_proxy/__init__.py"
rm -f "${PYPI_DIR}/gcf_proxy/__init__.py.bak"

declare -A PLATFORMS=(
  ["darwin_arm64"]="macosx_11_0_arm64:gcf-proxy:tar.gz"
  ["darwin_amd64"]="macosx_10_12_x86_64:gcf-proxy:tar.gz"
  ["linux_arm64"]="manylinux2014_aarch64:gcf-proxy:tar.gz"
  ["linux_amd64"]="manylinux2014_x86_64:gcf-proxy:tar.gz"
  ["windows_amd64"]="win_amd64:gcf-proxy.exe:zip"
  ["windows_arm64"]="win_arm64:gcf-proxy.exe:zip"
)

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$DIST_DIR"

for GOKEY in "${!PLATFORMS[@]}"; do
  IFS=: read -r PLAT_TAG BINARY_NAME ARCHIVE_EXT <<< "${PLATFORMS[$GOKEY]}"

  ARCHIVE="gcf-proxy-${GOKEY}.${ARCHIVE_EXT}"
  URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE}"
  BIN_DIR="${PYPI_DIR}/gcf_proxy/bin"

  echo "  [${PLAT_TAG}] Downloading ${ARCHIVE}..."
  curl -fsSL "$URL" -o "${TMP_DIR}/${ARCHIVE}"

  rm -rf "$BIN_DIR"
  mkdir -p "$BIN_DIR"

  if [ "$ARCHIVE_EXT" = "tar.gz" ]; then
    tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "$TMP_DIR" "$BINARY_NAME"
  else
    unzip -o "${TMP_DIR}/${ARCHIVE}" "$BINARY_NAME" -d "$TMP_DIR"
  fi

  cp "${TMP_DIR}/${BINARY_NAME}" "${BIN_DIR}/${BINARY_NAME}"
  chmod +x "${BIN_DIR}/${BINARY_NAME}"
  rm -f "${TMP_DIR}/${BINARY_NAME}"

  echo "  [${PLAT_TAG}] Building wheel..."
  cd "$PYPI_DIR"
  python3 -m pip wheel . --no-deps --wheel-dir "$TMP_DIR/wheels"

  SRC_WHEEL=$(ls "$TMP_DIR/wheels"/gcf_proxy-*.whl | head -1)
  python3 -m wheel tags --platform-tag "$PLAT_TAG" --remove "$SRC_WHEEL"
  TAGGED_WHEEL=$(ls "$TMP_DIR/wheels"/gcf_proxy-*.whl | head -1)
  cp "$TAGGED_WHEEL" "$DIST_DIR/"
  rm -rf "$TMP_DIR/wheels"

  echo "  [${PLAT_TAG}] -> $(basename "$TAGGED_WHEEL")"
done

rm -rf "${PYPI_DIR}/gcf_proxy/bin"

echo ""
echo "Wheels built in ${DIST_DIR}:"
ls -1 "$DIST_DIR"/*.whl
echo ""
echo "Publish with: twine upload ${DIST_DIR}/*.whl"
