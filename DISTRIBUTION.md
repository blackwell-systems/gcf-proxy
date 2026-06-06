# Distribution

gcf-proxy is distributed across 5 channels. This doc also covers the full GCF ecosystem distribution for maintainer reference.

## gcf-proxy channels

| Channel | Package | Install command |
|---------|---------|----------------|
| **Go** | `github.com/blackwell-systems/gcf-proxy` | `go install github.com/blackwell-systems/gcf-proxy@latest` |
| **npm** | `@blackwell-systems/gcf-proxy` | `npm install -g @blackwell-systems/gcf-proxy` |
| **PyPI** | `gcf-proxy` | `pip install gcf-proxy` |
| **GitHub Releases** | Binary downloads | [Releases page](https://github.com/blackwell-systems/gcf-proxy/releases) |
| **pkg.go.dev** | Documentation | [pkg.go.dev/github.com/blackwell-systems/gcf-proxy](https://pkg.go.dev/github.com/blackwell-systems/gcf-proxy) |

## How the proxy distributes

The Go binary is the single source of truth. npm and PyPI packages are thin wrappers that bundle the pre-built binary for each platform:

- **npm**: Root package (`@blackwell-systems/gcf-proxy`) has optionalDependencies on 6 platform-specific packages (darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-arm64, win32-x64). Each contains the binary in `bin/`. A Node.js shim detects the platform and executes the correct binary.
- **PyPI**: Platform-specific wheels (manylinux, macosx, win) each contain the binary in `gcf_proxy/bin/`. A Python entry point detects the platform and executes it.
- **Go**: Standard `go install` from source.
- **GitHub Releases**: Pre-built tarballs/zips for each platform.

## Proxy release process

1. Tag a release: `git tag v0.X.Y && git push origin v0.X.Y`
2. The `release.yml` workflow:
   - Builds cross-platform binaries (6 platforms)
   - Creates a GitHub Release with all binaries attached
   - Runs `scripts/npm-publish.sh` to publish platform packages + root package to npm
   - Runs `scripts/pypi-build-wheels.sh` to build platform wheels, then `twine upload` to PyPI

## Full GCF ecosystem distribution

| Repo | Registry | Publish trigger | Version file | Current |
|------|----------|-----------------|--------------|---------|
| gcf-go | pkg.go.dev | Auto on tag (Go module proxy) | N/A (uses git tag) | v0.4.0 |
| gcf-typescript | npm | `publish.yml` on `v*` tag | `package.json` | v0.3.0 |
| gcf-python | PyPI | `publish.yml` on `v*` tag | `pyproject.toml` | v0.3.0 |
| gcf-rust | crates.io | `publish.yml` on `v*` tag | `Cargo.toml` | v0.3.0 |
| gcf-swift | SPM | Auto on tag (GitHub) | N/A (uses git tag) | v0.3.0 |
| gcf-kotlin | JitPack | Auto on tag (GitHub) | N/A (uses git tag) | v0.3.0 |
| gcf-proxy | npm + PyPI + Go + GitHub Releases | `release.yml` on `v*` tag | `go.mod` | v0.1.0 |
| gcf (spec) | gcformat.com (GitHub Pages) | `Deploy Docs` on push to main | SPEC.md header | v1.3 |

## Library release checklist

For each library release:

1. Update version in version file (`package.json`, `pyproject.toml`, `Cargo.toml`)
2. Update CHANGELOG.md
3. Commit: `git add -A && git commit -m "chore: bump version to X.Y.Z"`
4. Push: `git push`
5. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`
6. Create GitHub release: `gh release create vX.Y.Z --title "vX.Y.Z" --notes "..."`
7. Verify publish workflow succeeded: `gh run list --workflow publish.yml --limit 1`

For Go and Swift/Kotlin (no publish workflow), step 5 is sufficient (registry auto-indexes from tag).

## Proxy release checklist

1. Bump gcf-go dependency: `go get github.com/blackwell-systems/gcf-go@vX.Y.Z`
2. Commit and push
3. Tag: `git tag v0.X.Y && git push origin v0.X.Y`
4. `release.yml` handles everything (binaries, npm, PyPI, GitHub Release)

## Secrets required

| Secret | Repository | Purpose |
|--------|-----------|---------|
| `NPM_TOKEN` | gcf-proxy | npm publish (granular token, must include `@blackwell-systems/gcf-proxy*`) |
| `NPM_TOKEN` | gcf-typescript | npm publish (granular token, must include `@blackwell-systems/gcf`) |
| `PYPI_TOKEN` | gcf-proxy | PyPI publish (API token, scoped to `gcf-proxy`) |
| `PYPI_TOKEN` | gcf-python | PyPI publish (API token, scoped to `gcf-python`) |
| `CARGO_REGISTRY_TOKEN` | gcf-rust | crates.io publish |

## Important notes

- **npm granular tokens**: must explicitly include each package name. New packages created after token creation are NOT auto-included. Update scope on npmjs.com if adding new packages.
- **Version files**: TypeScript, Python, and Rust require the version in source to match the tag. If you tag without bumping the version file, publish will fail with "already published" or version mismatch.
- **Go proxy**: pkg.go.dev indexes from the Git tag directly. No version file needed. Ensure `go.mod` has the correct module path.
- **Cargo fmt**: Rust CI runs `cargo fmt --check` before publish. Always run `cargo fmt` locally before tagging.
- **Proxy dep bump**: The proxy imports gcf-go. After releasing a new gcf-go version, bump the proxy's `go.mod` before the next proxy release.
