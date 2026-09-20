RESULT tools22-rule-wake-13-L4135 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 13 says?
CONFORMS internal/wake/pr.go:85
SPEC docs/SPEC-WAKE.md:4135 rule 13
PKG internal/wake
ASK The code must implement a `--pr`/`--owned-prs` source in `internal/wake/pr.go` that polls each watched PR via a single `gh api graphql` standing query, stores a multi-field state value with counts, `updatedAt`, head OID, and newest item metadata, performs a second `last:min(d,10)` node fetch under `--not-mine` for moved-count kinds, suppresses ticks proven entirely the actor's own words (storing but not queueing), uses `--not-mine` by node id as the sole exclusion mechanism (no second call when absent), reads `gh api user` once per run only for `--owned-prs`, refreshes the `--owned-prs` set with a cap of 20, applies cold-join recording for mid-run PRs, treats a failed `gh pr list` as a failed poll, batches at 8, uses `unreadable:` values, returns `fail:prs` when all PRs are unreadable in one tick, and defers the cold-first-poll to the watcher (records and reports nothing).

Deciding lines:

Standing `gh api graphql` call per PR: `internal/wake/pr.go:85-94` (standingQuery) and `pr.go:285-286` (`gh(ctx, ..., "api", "graphql", ...)`)).

State value (6 fields: comments, reviews, threads, updatedAt, headRefOid, newestField): `pr.go:324-325`. The spec says "five-field" but the code stores 6 — the `newest=` field was added in draft 3; the `fields(Decompose(old), 6)` at `pr.go:328` confirms 6 fields. This is a minor spec word count lag, not a code defect.

`rescan=true` when only the stamp moved: `pr.go:379` (`rescan := updatedMoved && !countMoved && !headMoved`).

Second call `last:min(d,10)` under `--not-mine`: `pr.go:348-351` calls `p.nodes(...)`; `pr.go:433-476` implements the fetch with `window()` capped at `PRNodeWindow(10)` at `pr.go:434-438`.

Suppressed tick storing whole observed value and queueing nothing: `pr.go:419-422` (`item.Record = true; return item, false`) — stored via `RecordOnly` in `main.go:1488-1489`.

`--not-mine` as the only id-based exclusion, no second call when absent: `pr.go:144-150` (nil guard), `pr.go:347` (`if mine != nil` guard), `pr.go:356-358` (`mine[n.ID]` check), `pr.go:431-432` comment: "without the flag nothing has to be excluded, so no second gh call is ever made."

`gh api user` once per run only for `--owned-prs`: `pr.go:211-228` behind `!p.loginRead` guard, only reached when `len(p.Owned) > 0` (`pr.go:205`).

`--owned-prs` cap of 20: `pr.go:46` (`OwnedCap = 20`), `pr.go:253-263` (capping at OwnedCap).

Cold-join rule: `pr.go:412-414` (`if !had && joined { item.Record = true }`).

Failed `gh pr list` as failed poll: `pr.go:193-194` and `pr.go:240-243` (sets `listFailed`), returning error at `pr.go:194`.

Batching at 8: `pr.go:163` (`sem := make(chan struct{}, ForgeBatch)`) where `ForgeBatch = EntryBatch = 8` (`forge.go:26`, `entry.go:24`).

`unreadable:` as a value: `pr.go:283`, `pr.go:288`, `pr.go:293`.

`fail:prs` when all PRs unreadable: `pr.go:196-198`.

Cold first poll recording/reporting nothing: deferred to main.go's cold-start handler (`pr.go:152-153` comment: "first poll is the cold-start rule's, which main.go's watcher owns"); implemented at `cmd/nova-wake/main.go:1459-1462`.

UNGUARDED — no test in `internal/wake/*_test.go` directly exercises `pr.go`'s behavior. The spec names "Tests: test 14" but no `Test14` or `TestPR` function exists in the package.

Grep commands run:
- `grep -rn "ForgeBatch" --include='*.go' internal/`
- `grep -rn "EntryBatch" --include='*.go' internal/`
- `grep -rn "func Test" --include='*_test.go' internal/wake/`
- `grep -rn 'test 14\|Test14' --include='*.go' -r .`
- `grep -rn 'test 14' docs/SPEC-WAKE.md`
- `grep -rn 'pr\.go\|KindPR' --include='*_test.go' internal/wake/ cmd/nova-wake/`
- `ls internal/wake/`

Left owed: None.

```
$ git status --short
(no output)
```