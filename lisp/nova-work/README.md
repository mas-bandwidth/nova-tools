# nova-work slice 1 — the internal C/O transition kernel

The engine of [`docs/SPEC-WORK.md`](../../docs/SPEC-WORK.md), at spec head
`7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64` (PR #231, branch `spec/nova-work`).
This is the internal kernel and nothing else: no session, no CLI, no socket, no
provider, no live import.

Run the acceptance suite:

    ./run-tests.sh

SBCL, non-interactive. Exit 0 when every case passes, 1 otherwise.

## Layout

The spec fixes the engine's language and no directory for it —
`docs/SPEC-WORK.md:290`, "**the session's own language is Common Lisp**", and
`:2047`, "the resident Common Lisp session ... the Go CLI is a thin client".
`lisp/nova-work/` with an ASDF system is therefore a **choice for review**,
beside the Go client's existing `cmd/` and `internal/`.

## What is in the boundary

- Restricted-data values and the ordered envelope representation
  (`src/value.lisp`, `src/event.lisp`), with the one deterministic printer of
  `SPEC-WORK.md:337` and the `(:absent)` / `()` / `""` distinction of `:329-337`.
- Deterministic canonical payload serialization and its SHA-256 digest
  (`src/sha256.lisp`, `SPEC-WORK.md:325`), for the supported transition subset.
- Pure atomic application of a validated **close + generated `:settle`** envelope
  and a **reopen + generated `:revive`** envelope (`src/kernel.lisp`,
  `SPEC-WORK.md:1202-1214`); event identity; append-only history and
  append-only closed rows (`:1216-1222`).
- Root and container **O counters maintained on write** (`src/state.lisp`,
  `SPEC-WORK.md:1562`): `|O|` is read, never computed by a walk.
- A small explicit **journal-acceptance interface** that can reject before apply
  (`src/journal.lisp`). Its fake tests **ordering only, not durability**.
- Unsupported inputs **refuse**; nothing silently bypasses a validation that is
  not here yet.

## What is out

`W` and its deadline semantics (W1–W5, `SPEC-WORK.md:1315-1357`), the public
session and CLI, the socket and the JSON wire, providers, live work import,
savepoints, the two-day retention window, batches, clip and checkpoint, undo and
redo, container settle cascades, and every verb schema proposed in PRs 293/294 —
none of which is invented here.

No unbounded request-id map is used as production dedup: `SPEC-WORK.md:2117`
forbids it. The kernel keeps no resident map; the two-part retry test asks the
journal, and the fake journal is bounded and answers `dedup unavailable` past
its bound rather than reporting an evicted id as new.

## The cases

Each names the line of `docs/SPEC-WORK.md` it comes from and carries the
acceptance table's own `expected=` string, or the executable invariant that row
names where the row has none.

| case | spec |
| --- | --- |
| `supported-subset-format-determinism` | `:3345` |
| `settle-outside-the-digest` | `:3122` |
| `open-count-is-read-not-computed` | `:3229` |
| `reconstruction-after-close-and-revive` | `:3348`, rows per `:3104` |
| `two-event-candidate-is-all-or-none` | `:3346` |

**Partial coverage, stated rather than implied.** Nothing here claims green on
durability (`atomic-mutation`, `:3346`, whose failure injections around the
journal append, the durable sync, the savepoint write and the rename are not
exercised), on recovery (`:3357`), or on the materialized working set
(`materialized-working-set`, `:3354`). `open-count-is-read-not-computed` is
exercised after a close and a reopen; its **import replay** leg is not, because
import is out of this slice.
