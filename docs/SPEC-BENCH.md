# Bench Run Directory Specification

This document specifies how bench runs manage their directories and cleanup.

## Run Directory Structure

Every bench run lives in its own directory under the bench root:

```
<root>/runs/<kind>-<id>/
  ├── tree/      # The repository tree
  ├── tmp/       # Temporary files (TMPDIR)
  └── gocache/   # Go build cache (GOCACHE)
```

The `<root>` defaults to `~/nova-bench/runs`.

The `<kind>` is one of:
- `gate` - Tree gate runs (land.go, benchGate)
- `read` - Friend reader runs
- `land` - Land runs
- `friend` - Friend operations

The `<id>` is a unique identifier (e.g., timestamp, card ID, or hash).

## Environment Variables

Each run exports these environment variables before executing commands:

```bash
TMPDIR=<run>/tmp
GOTMPDIR=<run>/tmp
GOCACHE=<run>/gocache
GOFLAGS=-mod=readonly
NOVA_TEST_NO_HOST=1
```

## Lifecycle

1. **Create**: `mkdir -p <run>/tree <run>/tmp <run>/gocache`
2. **Copy**: Copy the repository tree to `<run>/tree/`
3. **Execute**: Run commands with environment variables set
4. **Remove**: `rm -rf <run>/` (defered, always executed)

## Cleanup Guarantee

The run directory is removed in a `defer` statement:

```go
defer func() {
    // Always executed, even on error or context cancel
    // Uses ssh with path checked under root to prevent variable path attacks
    remove(context.WithoutCancel(ctx), t, host, dir)
}()
```

This ensures cleanup happens on:
- Successful completion
- Command failure
- Timeout
- SSH drop
- Context cancellation

## Disk Guard

The `nova-swarm disk-guard` loop sweeps `<root>/runs/*` and removes:
- Run directories older than 2 hours
- Without live processes

It also checks disk space against a floor threshold before allowing new runs.

## Implementation

See `internal/bench/bench.go` for the `Run`, `MakeLine`, and `ExecLine` functions.
