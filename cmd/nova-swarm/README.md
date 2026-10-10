# nova-swarm (shim)

`nova-swarm` is now `nova-worker` (see [docs/SPEC-WORKER.md](../../docs/SPEC-WORKER.md)).
This directory holds only a shim: it prints one line to standard error,

```
nova-swarm is now nova-worker; this name is removed in the next release
```

and then runs `nova-worker` with the same arguments, the same standard input,
standard output and standard error, and the same exit code.

The `nova-swarm` name ships for one release — v1.3 — and is removed in the
release after it. The v1.4 cleanup renames the `internal/swarm` package; that
package keeps its import path in v1.3.
