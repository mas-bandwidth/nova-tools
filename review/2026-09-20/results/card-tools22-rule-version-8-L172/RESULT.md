RESULT tools22-rule-version-8-L172 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 8 says?
CONFORMS internal/update/diffverb.go:97
SPEC docs/SPEC-VERSION.md:172 rule 8
PKG cmd/nova-version
ASK When comparing two snapshots via `nova-version diff --from A --to B`, a binary only in `--from` must print one `DIFF CHANGED` line with `to=-` on the absent right side, a binary only in `--to` must print one with `from=-` on the absent left side, and `changed=` must count both.

CONFORMS internal/update/diffverb.go:97 — binary only in `--to` (added): `fmt.Fprintf(out, "DIFF CHANGED name=%s from=- to=%s\n", ...)` shows `-` on the absent `from=` side.
CONFORMS internal/update/diffverb.go:99 — binary only in `--from` (removed): `fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=-\n", ...)` shows `-` on the absent `to=` side.
CONFORMS internal/update/diffverb.go:103,105 — `changed++` runs for every diff (added, removed, or changed) and the closing `DIFF OK` line prints `changed=%d`, counting both.

GUARDED-BY internal/update/inventory_spec_test.go:236 TestDiffNamesAddedAndRemoved

Grep commands run:
- `grep -rn "TestDiffNamesAddedAndRemoved" --include='*.go' .` → found in docs/SPEC-VERSION.md:172 and internal/update/inventory_spec_test.go:236
- `grep -rn "DiffNamesAddedAndRemoved" --include='*.go' .` → same results
- `grep -rn "changed=" --include='*.go' .` → 35 matches across diffverb.go, inventory_spec_test.go, report.go, cli.go
- `grep -rn "DIFF CHANGED" --include='*.go' .` → 7 matches across docs/SPEC-VERSION.md, diffverb.go, inventory_spec_test.go
- `ls cmd/nova-version/` → main.go, version_test.go, examplelines_test.go, firstrun_test.go, friendsequence_test.go, testdata/
- Read internal/update/diffverb.go (full file, 107 lines)
- Read internal/update/inventory_spec_test.go lines 235-244 (the test)
- Read internal/update/cli.go lines 171-176 (verb routing)

Left owed: none

`git status --short` prints nothing — tree is clean.