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
  Evaluation syntax — every dispatch macro, quote, backquote, unquote, comment
  and `|` escape — and a malformed or trailing form refuse at the boundary with
  a byte offset (`:678-681`), never as a raw reader error. A keyword whose name
  is not already upper case refuses, because the printer downcases and two names
  differing only in case would otherwise print identical bytes.
- Deterministic canonical payload serialization and its SHA-256 digest
  (`src/sha256.lisp`, `SPEC-WORK.md:325`), for the supported transition subset.
- Pure atomic application of a validated **close + generated `:settle`** envelope
  and a **reopen + generated `:revive`** envelope (`src/kernel.lisp`,
  `SPEC-WORK.md:1202-1214`); event identity; append-only history and
  append-only closed rows, each carrying `revived=<rev|->` and `settles=<n>`
  (`:1216-1222`). Static `:work-set` and `:feature` containers maintain direct
  required-member counters: closing the last required member settles the
  containment path in the same envelope, and reopening one revives that path
  (`:1272-1283`). Optional children do not block completion, and an empty
  required set never settles.
- Root and container **O counters maintained on write** (`src/state.lisp`,
  `SPEC-WORK.md:1568`): `|O|` is read, never computed by a walk. `query --ask
  size` prints the counters that ask itself measured.
- A small explicit **journal-acceptance interface** that can reject before apply
  (`src/journal.lisp`), and the journal is **appended before the apply**
  (`:307`). Its fake tests **ordering only, not durability**.
- Referential integrity **before publication** (`:3347`): rule 1 duplicate ids,
  rule 2 dangling parents and rule 3 cycles all refuse at seed time, bounded by
  the node count.
- Unsupported inputs **refuse**; nothing silently bypasses a validation that is
  not here yet. An unrecognised request key refuses, and a field the transition
  does not own — a caller-given `:blocked-by` on a `:to :done` — refuses rather
  than being quietly overwritten with `(:absent)` (`:3227`).

## What is out

`W` and its deadline semantics (W1–W5, `SPEC-WORK.md:1316-1357`), the public
session and CLI, the socket and the JSON wire, providers, live work import,
savepoints, the two-day retention window, batches, clip and checkpoint, undo and
redo, dynamic required-set mutation, roadmap/epic cascade policy, and every verb
schema proposed in PRs 293/294 — none of which is invented here.

No unbounded request-id map is used as production dedup: `SPEC-WORK.md:2117`
forbids it. The kernel keeps no resident map; the two-part retry test asks the
journal, and the fake journal is bounded and answers `dedup unavailable` past
its bound rather than reporting an evicted id as new.

## Decisions, for review

1. **A container is in its own count.** A container's counter is the open
   canonical item ids in its subtree *including itself*. `:1568` says only "the
   per-repository and per-container counts beneath it", where *beneath it* is
   beneath the root; it does not settle self-inclusion. The root's `|O|` counts
   containers as items, and `:1573` says the counters "count canonical item ids
   once", so a per-container count that excluded its own id would not be the
   same counting rule one level down.
2. **`:transition`'s ordered field list.** `:823` names its fields but sets out
   no explicit order the way `:structure` and the scope kinds are set out. The
   sentence's own order is used — `:to :reason :blocked-by :evidence` — and the
   evidence field is spelled `:evidence`, the name `:cancel` uses.
3. **`unit=items` on `query --ask size`.** `:1575` requires the count's unit on
   the line; `:1471`'s vocabulary is defined for roadmap and rollup grains and
   `|O|`'s own unit is not enumerated.
4. **The bounded fake refuses forever past its bound.** Once a record has been
   evicted, every id the store no longer holds answers `dedup unavailable`,
   including one never seen. `:517` refuses rather than assuming a request
   outside the bound is new, and a fake that cannot tell the two apart must take
   the refusal.

## The cases

Each names the line of `docs/SPEC-WORK.md` it comes from and carries the
acceptance table's own `expected=` string, or the executable invariant that row
names where the row has none.

| case | spec |
| --- | --- |
| `supported-subset-format-determinism` | `:3345` `format-determinism` |
| `settle-outside-the-digest` | `:3122` |
| `open-count-is-read-not-computed` | `:3229` |
| `reconstruction-after-close-and-revive` | `:3353` `indexes-and-counters` |
| `two-event-candidate-is-all-or-none` | `:3348` `atomic-mutation` |
| `referential-integrity-refuses-a-cycle` | `:3347` `referential-integrity` |
| `closed-rows-carry-revived-and-settles` | `:3103` `revive-appends-and-counts-latest` |
| `journal-records-before-it-applies` | `:3348` `atomic-mutation` |
| `reconstructed-kernel-does-not-reissue-ids` | `:3353` `indexes-and-counters` |
| `request-fields-refuse-rather-than-drop` | `:3227` `every-field-has-an-owning-verb` |
| `dedup-refuses-past-its-bound` | `:3349` `retry-protocol` |

## Partial coverage, stated rather than implied

Nothing here claims green on:

- **Durability.** `atomic-mutation` (`:3348`) is exercised at two boundaries
  only — the acceptance refusal and a stop injected between the journal append
  and the apply. The failure injections around the durable sync, the savepoint
  write, the rename and the reply are not here, and the journal fake has no
  durability to test. **A journal record without an applied envelope is written
  and never replayed**: recovery replay is out of this slice, so the fake's
  post-stop state is proof of ordering and of nothing else.
- **Recovery** (`:3357`). Untouched.
- **The materialized working set** (`:3354`). Untouched; there is no W here.
- **Evidence resolution.** Rule 5 (`:2657-2659`) refuses a `:to :done` "naming
  no evidence events, or naming ones whose criteria do not cover the node's
  `:acceptance`, or of an older generation than the node's, or whose pointer is
  a `note:` scheme". Only the first clause and the `note:` clause are enforced.
  Each id is checked for being a non-empty string and is **not resolved to an
  existing `:evidence` event**, because the `:evidence` event kind, `:acceptance`
  and `:generation` are all outside this slice. `("ev-1")` in the suite is a
  placeholder that resolves to nothing.
- **Two independent serializers.** `settle-outside-the-digest` (`:3122`) asks for
  one request digested by *two independent serializers*. There is one serializer
  here, run in two kernels with different revision bases and different stamps.
  That proves the digest is independent of what the session assigns; it does not
  prove two builds agree.
- **A second settle of one id through the kernel's gate.** `:3104-3106` wants a
  third row with `settles=2`. A `:reopen` lands at `:todo`, and `:todo` has no
  edge to `:done` (`:994-1005`), so reaching it would need `state --to doing`,
  which is outside this slice's transition subset. `settles=2` is exercised on
  `apply-event` — the primitive the live path and the replay path share — and
  the test says so at the assertion.
- **The complete container model.** `containers-settle-with-their-members`
  covers the static containment subset for `:work-set` and `:feature`. Direct
  container close still refuses, and roadmap/epic membership, dynamic
  `node require`, cancellation, removal and supersession remain outside this
  slice. A cascade decision is O(1) at each ancestor, but this kernel still
  copies the full candidate state and each emitted event updates containment
  counters along its ancestor path; it makes no O(depth) claim for the whole
  mutation. `container-cascade-journal-rejection-and-retry-stability` tests
  whole-cascade refusal at journal acceptance and an accepted request's retry;
  it does not inject a failure in an ancestor event.
- **`open-count-is-read-not-computed`'s import-replay leg** (`:1572`). Not
  exercised, because import is out of the boundary. Its close and reopen legs
  are.
