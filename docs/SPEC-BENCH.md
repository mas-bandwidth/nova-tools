# Bench Run Directory Specification (SPEC-BENCH)

This document specifies how bench runs manage their directories, isolation, and automatic cleanup.

## 1. Goal and Invariant

Every run on a bench (the lander's gate, a reader lane's gate, a friend lane's bench copy) lives in one directory of its own under the bench root, puts its temporary files, Go temp and Go build cache inside that directory, and removes the whole directory when it ends, however it ends.

Cleanup is automatic and guaranteed; finished runs never leak or fill bench disks.

## 2. Run Directory Structure

Under the bench root (`<root>`, typically `~/nova-bench`), every run is isolated in its own directory:

```
<root>/runs/<kind>-<id>/
  ├── tree/      # The repository checkout / tree
  ├── tmp/       # Temporary files (TMPDIR and GOTMPDIR)
  └── gocache/   # Go build cache (GOCACHE)
```

The run directory name encodes:
- `<kind>`: the purpose of the run (e.g., `gate` for lander tree gates, `read` for reader lanes, `run` for general runs).
- `<id>`: a unique run identifier (card ID, timestamp, or unique hash).

## 3. Environment Variables

Before executing any commands on the bench, the run exports these environment variables:

```sh
TMPDIR='<dir>/tmp'
GOTMPDIR='<dir>/tmp'
GOCACHE='<dir>/gocache'
GOFLAGS=-mod=readonly
NOVA_TEST_NO_HOST=1
```

The process executes under `nice -n 19` in `<dir>/tree`.

## 4. Run Lifecycle and Cleanup Guarantee

1. **Make**: Creates `<dir>/tree`, `<dir>/tmp`, and `<dir>/gocache` in a single remote step (`MakeRunLine`), echoing `<dir>`.
2. **Stage/Copy**: Stages or copies the repository tree into `<dir>/tree`.
3. **Execute**: Changes directory to `<dir>/tree` and executes the command under the exported environment variables.
4. **Deferred Remove**: The run directory is removed when it ends under a context that caller cancellation does not terminate (`context.WithoutCancel(ctx)`):

```go
defer func() {
    res.RemoveErr = remove(context.WithoutCancel(ctx), t, host, o.Root, dir)
    res.Removed = res.RemoveErr == nil
}()
```

The path is strictly validated under the root by the bench's own `rm` guard (`CheckRunDir`), preventing escape or variable path attacks. Cleanup is guaranteed across:
- Successful exit
- Failing tests / non-zero command exits
- Step and run timeouts
- SSH connection drops
- Context cancellation / interrupts

## 5. Disk Guard Sweep Rule

A run directory older than two hours with no live process on the bench is a leak:
- The disk-guard loop (`nova-swarm disk-guard`) sweeps only the run directories,
  `<root>/runs/*`. Every other directory under the bench root (the shared build
  cache, a friend's copy, anything else) is never a candidate, so the sweep can
  never delete an arbitrary two-level directory below a bench root.
- For each run directory where modification age exceeds 2 hours and no live
  process names it or holds a file in it, the directory is removed under the
  bench root by the guard's own `safepath` remove, never a bare path.
- Each removal prints a single `REMOVED run <path> freed=<bytes>` line.

## 6. Disk Floor Check

Before starting any run on a bench, the available disk space is verified against the disk floor (`DefaultFloorGB = 10` GiB, or `--disk-floor`):
- A bench with less free space than the floor takes no run.
- It reports its status on its fleet row: `disk <free>`, red under the floor.
- It takes no new runs until free disk space rises above the floor.
