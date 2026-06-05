# Distribution

gcf-proxy is distributed across 5 channels:

## Package managers

| Channel | Package | Install command |
|---------|---------|----------------|
| **Go** | `github.com/blackwell-systems/gcf-proxy` | `go install github.com/blackwell-systems/gcf-proxy@latest` |
| **npm** | `@blackwell-systems/gcf-proxy` | `npm install -g @blackwell-systems/gcf-proxy` |
| **PyPI** | `gcf-proxy` | `pip install gcf-proxy` |
| **GitHub Releases** | Binary downloads | [Releases page](https://github.com/blackwell-systems/gcf-proxy/releases) |
| **pkg.go.dev** | Documentation | [pkg.go.dev/github.com/blackwell-systems/gcf-proxy](https://pkg.go.dev/github.com/blackwell-systems/gcf-proxy) |

## How it works

The Go binary is the single source of truth. npm and PyPI packages are thin wrappers that bundle the pre-built binary for each platform:

- **npm**: Root package (`@blackwell-systems/gcf-proxy`) has optionalDependencies on 6 platform-specific packages (darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-arm64, win32-x64). Each contains the binary in `bin/`. A Node.js shim detects the platform and executes the correct binary.
- **PyPI**: Platform-specific wheels (manylinux, macosx, win) each contain the binary in `gcf_proxy/bin/`. A Python entry point detects the platform and executes it.
- **Go**: Standard `go install` from source.
- **GitHub Releases**: Pre-built tarballs/zips for each platform.

## Release process

1. Tag a release: `git tag v0.X.Y && git push origin v0.X.Y`
2. The `release.yml` workflow:
   - Builds cross-platform binaries (6 platforms)
   - Creates a GitHub Release with all binaries attached
   - Runs `scripts/npm-publish.sh` to publish platform packages + root package to npm
   - Runs `scripts/pypi-build-wheels.sh` to build platform wheels, then `twine upload` to PyPI

## Secrets required

| Secret | Repository | Purpose |
|--------|-----------|---------|
| `NPM_TOKEN` | gcf-proxy | npm publish (granular token, must include `@blackwell-systems/gcf-proxy*`) |
| `PYPI_TOKEN` | gcf-proxy | PyPI publish (API token) |
