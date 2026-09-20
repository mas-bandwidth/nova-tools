RESULT tools22-rule-version-3-L82 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 3 says?
ABSENT
SPEC docs/SPEC-VERSION.md:82 rule 3
PKG cmd/nova-version
ASK An implementation of `nova-version moved` must refuse when `--from` resolves to something that is not a commit by exiting 2, naming the revision and the git fetch remedy, and writing no note to `--out`.
Deciding lines: none — the `moved` verb is not dispatched in `internal/update/cli.go` at all. Go tool any verb other than `snapshot`, `diff`, `report`, `send` (which becomes `report`), `version`, or `help` for `nova-version` hits the catchall at `internal/update/cli.go:200-202` and returns "unknown verb". No `moved` handler, no `--from`/`--to` flag parsing, no "not a commit" or "fetch remedy" logic exists anywhere in the tree.
Greps run:
- `grep -rn "TestMoved" --include='*_test.go' .` — no matches
- `grep -rn "TestMovedRefusesAnUnresolvedRevision" .` — only in `docs/SPEC-VERSION.md:82` (the spec itself)
- `grep -rn "moved" --include='*.go' internal/update/` — no verb handler
- `grep -rn '"moved"' --include='*.go' .` — no matches
- `grep -rn "not a commit\|not_a_commit\|unresolve" --include='*.go' internal/update/` — no matches
GUARDED-BY none — no test exists for this behavior (no `TestMoved*` test in the tree)
Left owed: implement the `moved` verb including the refusal this rule describes.
git status --short: nothing