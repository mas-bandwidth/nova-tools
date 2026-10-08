# nova-tools — cold audit, opencode, 2026-10-06

Base: `sprint/mechanical-2026-10-02` at `bba7b0ca8d641150c05c80a12d30f5e94d46e197`.
Scope: nova-tools without the sprint — every command and internal package, the fleet
plays, the docs tree and their TLA+ models. The code was read as a stranger with the
specs beside it; nothing was run against a live store or server.

---

1. **`tla/Bus2.tla:2` —
   the model cites `docs/SPEC-BUS2.md` and `internal/bus2/bus2.go` that do not exist in the tree.**
   Line 2 reads `nova-bus2's delivery machine (docs/SPEC-BUS2.md; internal/bus2/bus2.go:
   the, Recv, AckEntry and Ack; ...)`, and the model EXTENDS `Bus2` (itself). But
   `docs/SPEC-BUS2.md` and the directory `internal/bus2/` are absent from the tree; the
   bus was renamed to `internal/bus` and specced by `docs/SPEC-BUS.md` on 2026-10-04.
   *Evidence:* `ls docs/SPEC-BUS2.md` → no such file; `ls internal/bus2/` → no such directory;
   `git log --all --oneline -- docs/SPEC-BUS2.md` → no commits adding it.
   *Grade:* **NEXT** — a TLA+ model of a package that was renamed is a dead reference.
   *Fix:* retire the `Bus2` family (delete `Bus2.tla`, `Bus2Receipts.tla` and their MC wrappers)
   or retarget the header to `docs/SPEC-BUS.md` and `internal/bus`.

2. **`tla/BusCursor.tla:3-4` —
   the model cites `internal/bus` files that were deleted days before the model landed.**
   Lines 3–4 say `Models internal/bus (cursor.go:1-25, :401-417 WriteCursor, :611-612 ReadOpen;
   lock.go:1-30; conflict.go:41-55; protocol.go; git.go)`. None of those files exist in
   `internal/bus`; `internal/bus` now contains only `bus.go`, `redis.go`, `stages.go`, etc.
   *Evidence:* `ls internal/bus/cursor.go internal/bus/lock.go internal/bus/conflict.go
   internal/bus/protocol.go internal/bus/git.go 2>&1` → all return "No such file or directory";
   `ls internal/bus/` → no such files.
   *Grade:* **NEXT** — a TLA+ model citing deleted source files is dead weight.
   *Fix:* retire `BusCursor.tla` and its MC wrappers (`MCBusCursor*.cfg`, `MCBusCursor*.tla`)
   or rewrite the model against the actual `internal/bus` API.

3. **`tla/CardContract.tla:156` —
   the comment cites `internal/sprint/providerEnded` that does not exist in the tree.**
   Line 156 reads `Staging (cmd/nova-swarm frameOf, internal/swarm StageCard and restageAtTip)`,
   and lines 248–286 cite `internal/sprint providerEnded` and `internal/sprint stagingRefused`.
   `stagingRefused` exists in `internal/sprint/steps_work.go`, but `providerEnded` is absent.
   *Evidence:* `grep -n "func providerEnded" internal/sprint/*.go` → no output;
   `grep -n "stagingRefused" internal/sprint/steps_work.go` → present at line 239.
   *Grade:* **NEXT** — a cited function that does not exist breaks the reader's ability to
   follow the model's correspondence with code.
   *Fix:* add `providerEnded` to `internal/sprint/...` with a clear contract in its header,
   or remove the citation from the model.

---

urgent=0 next=3
