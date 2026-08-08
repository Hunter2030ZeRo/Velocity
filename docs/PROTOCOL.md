# Go–Rust resolver protocol v1

Velocity invokes `velocity-resolver` directly and writes one JSON request to standard input.

```json
{
  "protocol": 1,
  "index_path": "/cache/registry/<sha256>",
  "target": "x86_64-unknown-linux-gnu",
  "roots": ["ripgrep", "fd"]
}
```

Success returns packages in deterministic dependency-first order:

```json
{
  "protocol": 1,
  "status": "ok",
  "packages": [
    {
      "id": 1,
      "name": "ripgrep",
      "version": "14.1.1",
      "artifact": {
        "target": "x86_64-unknown-linux-gnu",
        "url": "https://example.invalid/ripgrep.tar.gz",
        "sha256": "0000000000000000000000000000000000000000000000000000000000000000",
        "archive": "tar.gz",
        "strip_components": 1,
        "binaries": [{"source": "rg", "name": "rg"}]
      }
    }
  ]
}
```

Domain failures remain machine-readable:

```json
{
  "protocol": 1,
  "status": "error",
  "error": {"code": "dependency_cycle", "message": "dependency cycle detected"}
}
```

Malformed input or an unrecoverable standard-I/O failure may terminate the resolver non-zero. The Go boundary
captures bounded standard output/error and converts either outcome into a typed error.
