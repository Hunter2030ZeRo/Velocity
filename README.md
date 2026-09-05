# Velocity

Velocity is a small, security-focused package-manager foundation built from a Go I/O plane and a Rust resolution core.

The registry remains metadata-only. Package maintainers commit one TOML manifest per package to
[`Hunter2030ZeRo/velocity-registry`](https://github.com/Hunter2030ZeRo/velocity-registry); those manifests point at
upstream HTTPS artifacts and pin their SHA-256 digests. Registry CI compiles the manifests into a versioned,
checksummed index for clients.

## Architecture

```text
velocity-registry registry branch
  registry.json + velocity.idx.zst
              |
              v
  Go: HTTP, cache, SHA-256, cancellation
              |
              v
  Rust: index decode, target filtering, dependency DAG, conflicts, install plan
              |
              v
  Go: bounded parallel downloads, safe extraction, staged filesystem commit
```

- Go owns network and filesystem orchestration. Related work runs under bounded `errgroup` goroutines and shares
  caller cancellation.
- Rust owns the compute-heavy, side-effect-free registry index validation and dependency-plan calculation.
- The language boundary is a versioned JSON subprocess protocol. This keeps Go builds free from cgo and keeps
  Rust free from an unsafe FFI surface.

## Build

Requirements: Go 1.24+, stable Rust 1.85+, `gofumpt`, `goimports`, `golangci-lint` v2, and `nilaway`.

```sh
task build
```

`task` is optional convenience provided by Go Task 3.x. Without it, run the equivalent raw commands:

```sh
mkdir -p bin
cargo build --release --package velocity-resolver
cp target/release/velocity-resolver bin/velocity-resolver
go build -trimpath -ldflags="-s -w" -o bin/velocity ./cmd/velocity
```

Either path writes sibling executables to `bin/velocity` and `bin/velocity-resolver`.

## Install

Install the CLI and resolver together, without cloning the repository.

Linux (POSIX shell):

```sh
curl -fsSL https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.ps1 | iex
```

These commands install the latest **published** release. A draft release is not
downloadable by an anonymous installer; publication must include `SHA256SUMS`.
No administrator privileges are needed for the default per-user destinations.

To select a version or destination while piping the script:

```sh
curl -fsSL https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.sh | sh -s -- --version v0.1.0 --install-dir "$HOME/.local/bin"
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.ps1))) -Version v0.1.0
```

Alternatively, download either installer and run the file:

```sh
sh install.sh
sh install.sh --version v0.1.0
```

```powershell
.\install.ps1
.\install.ps1 -Version v0.1.0
```

Both installers are self-contained repository artifacts: either file can be
downloaded or copied alone without the rest of the repository.

The POSIX installer supports x86-64/AArch64 Linux GNU and musl targets and
defaults to `$HOME/.local/bin`. The PowerShell installer supports x86-64 and
AArch64 Windows MSVC on Windows PowerShell 5.1 or PowerShell 7 and defaults to
`%LOCALAPPDATA%\velocity\bin`. Pass
`--target`/`-Target` for an explicit target and `--install-dir`/`-InstallDir`
for another destination. Neither script modifies shell profiles or persistent
`PATH`; it prints a reminder when the destination is not already available.

Each release must publish `SHA256SUMS` and one stable archive per target:

```text
velocity-<linux-target>.tar.gz  # velocity, velocity-resolver
velocity-<windows-target>.zip   # velocity.exe, velocity-resolver.exe
```

Both scripts require exactly those two root entries and verify the archive
SHA-256 before changing the installation directory. `VELOCITY_VERSION`,
`VELOCITY_TARGET`, `VELOCITY_INSTALL_DIR`, and `VELOCITY_RELEASE_BASE_URL`
provide environment equivalents for mirrors and automated installs;
`VELOCITY_REPOSITORY` selects another `owner/repository` release source.

Installer downloads are capped at 256 MiB, manifests at 1 MiB, and each
expanded executable at 128 MiB. Redirects remain HTTPS-only and are limited to
10 hops; POSIX downloads also enforce total/low-speed timeouts. Publication is
serialized per installation directory, rolls the sibling pair back together,
and preserves a recovery stage/marker if rollback itself cannot complete.

The selected GitHub release origin or explicit filesystem mirror, every HTTPS
redirect/CDN origin, and the platform WebPKI/proxy path form the installer trust
root. `SHA256SUMS` protects transfer integrity but is fetched through that same
trust chain and therefore does not defend against a compromised publisher.
PowerShell filesystem sources must be rooted directories; direct UNC/device and
leaf reparse-point bases are rejected. A mapped/network-backed drive selected by
the caller is still treated as a trusted mirror.

Install directories and every ancestor must remain stable and must not be
writable or replaceable by a less-privileged user during installation. Avoid
elevated installs into shared/custom paths; the default per-user destinations
are the intended trust boundary.

Use the installed package manager with:

```sh
velocity install ripgrep fd --root "$HOME/.local" --jobs 4
```

Velocity installs only the explicit `artifacts.binaries` mappings into `<root>/bin`. Existing destination files are
never overwritten. Linux host detection defaults to the GNU target; musl users should pass `--target` explicitly.

The default metadata URL is:

```text
https://raw.githubusercontent.com/Hunter2030ZeRo/velocity-registry/registry/registry.json
```

## Supported registry contract

- Registry metadata format `1`
- Index magic `VLTIDX1\0`, format version `1`, bincode 1.3 payload, zstd compression
- Targets: x86-64/AArch64 Linux GNU or musl, and Windows MSVC
- Archives: `zip`, `tar.gz`, `tar.zst`, and `raw`
- Logical archive output is capped at 1 GiB by default, ZIP work is capped at 10,000
  members, sparse TAR entries are rejected, and Zstandard decoding uses a 64 MiB
  window/memory ceiling.
- `tar.xz` is intentionally rejected until the Go decoder can enforce a hard
  LZMA2 dictionary-memory ceiling.
- One install may select at most 256 artifacts and stream at most 1 GiB across
  the transaction; a failed batch removes only cache objects it introduced.
- Resolver index preflight bounds packages at 20,000 and applies per-record and
  aggregate limits before allocating the bincode model.
- Target-conditional dependencies, aliases, semver requirements, cycles, and conflicts

## Current scope

This foundation intentionally implements install only. Upgrades, removal, a persistent ownership database,
cross-process locking, signed registry metadata, and a single-file Go+Rust distribution remain future work.
Artifact hashes protect integrity after registry retrieval; they do not replace registry signing.

## Verification

```sh
golangci-lint run --timeout 5m ./...
nilaway ./...
go test -race -shuffle=on -count=1 ./...
cargo fmt --all -- --check
cargo clippy --all-targets --all-features --workspace -- -D warnings
cargo test --workspace
```
