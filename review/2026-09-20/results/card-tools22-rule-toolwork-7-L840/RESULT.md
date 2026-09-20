RESULT tools22-rule-toolwork-7-L840 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 7 says?
GAP cmd/nova-merge/batch.go:652
SPEC docs/SPEC-TOOLWORK.md:840 rule 7
PKG internal/docs
ASK `nova-merge batch` must admit a harvest-pushed member only when the PR body's first line is an `ACCEPT OK` whose `head=` is the member's current head and whose `control=` is on file (else `BATCH DROP #<n> reason="no ACCEPT OK for head <sha12>"`), run `internal/hygiene.Check` over every member whatever its origin, and when the merged tree does not compile, bisect the members once and name the failing member with `BATCH DROP #<n> reason="build red with this member merged: <first line>"` instead of failing the batch whole.

The code the rule binds to is present and does less: the admission gate at cmd/nova-merge/batch.go:521 (`admissible`) admits on holds/ci-ok and never reads a PR body or a control file, so the rule's `no ACCEPT OK for head <sha12>` drop cannot happen; cmd/nova-merge/batch.go:14-18 imports no `internal/hygiene`, so the hygiene pass over every member is not wired in (the package and its `Check` exist at internal/hygiene/hygiene.go:85 and are called by `accept` and `nova-check hygiene`, never by batch); and a red step fails the whole batch at cmd/nova-merge/batch.go:447-450 (`BATCH FAIL`) with no member-by-name bisection.

Deciding lines:
- cmd/nova-merge/batch.go:652: `fmt.Fprintf(stderr, "BATCH DROP #%d reason=%q t=%.1fs\n", n, fmt.Sprintf("head %s has no green %s (state=%s)", oneline.Field(pr.HeadOID), batchRequiredCheck, oneline.Field(state)), since(start))` — the only admission-drop in `batch`, and it is a `ci-ok` drop, not the rule's `no ACCEPT OK for head` drop; `pr.Body` (internal/merge/host.go:179) is never read.
- cmd/nova-merge/batch.go:447-450: `fmt.Fprintf(stdout, "BATCH FAIL %s step=%s ... reason=%q\n", ...)` — on a red build/test step the whole batch fails, naming the step and the first failure line but never bisecting the members to name the one that broke the merge, so the rule's `build red with this member merged: <first line>` line is impossible.
- cmd/nova-merge/batch.go:14-18: the imports list has no `github.com/mas-bandwidth/nova-tools/internal/hygiene`, and no `nova-merge` file loads a lane `identities.tsv` to feed `hygiene.Options.Identities` — the `batch` half of §3 rule 7's "one entry point, three callers" is absent.

Greps ran:
- `grep -rn "no ACCEPT OK for head\|build red with this member merged" --include='*.go' .` -> no match
- `grep -rn "ACCEPT OK" --include='*.go' .` -> no match in any .go file
- `grep -rn "internal/hygiene\|hygiene\." --include='*.go' cmd/nova-merge/` -> no match (imports only in cmd/nova-check and internal/swarm)
- `grep -rn "bisect\|build red\|member that breaks" --include='*.go' cmd/nova-merge/ internal/merge/` -> no match
- `grep -rn "harvest" --include='*.go' cmd/nova-merge/` -> only a comment at react.go:8
- `grep -rn "identities\|identities.tsv" --include='*.go' cmd/nova-merge/ internal/merge/` -> no match
- `grep -rn "func Test" cmd/nova-merge/batch_test.go` -> `TestBatchDropsTheConflictAndGoesRedOnTheFailingMember` (batch_test.go:90) pins the current whole-batch `BATCH FAIL` shape, not the rule's bisection.

Left owed