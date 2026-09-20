RESULT tools22-rule-toolwork-7-L385 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 7 says?
CONFORMS internal/review/seed.go:191
SPEC docs/SPEC-TOOLWORK.md:385 rule 7
PKG internal/docs
ASK The seed form of `nova-review mutate` must count, from the worktree after `git apply` and not from the patch header, exactly what the seed changed — one changed/added/removed/moved line, or for the whole-object seeds exactly one file or one hunk — print `edits=<n>` on its verdict line, and refuse with exit 2 when the count is 0 or >1.
The count is asserted by the verb itself, exactly as rule 7 says: MutateSeed applies the seed in a throwaway worktree, stages it, and reads git's own `--numstat` and a `-U0` diff, then

```
internal/review/seed.go:190:	res.Edits = countEdits(applied, placed)
internal/review/seed.go:191:	if res.Edits != 1 {
internal/review/seed.go:192:		return res, &SeedCountError{Edits: res.Edits}
```

`countEdits` (internal/review/seed.go:253-276) implements all four one-edit shapes the rule names — one `-` and one `+` (changed), one added line, one removed line, and one line moved (two hunks, exactly one added and one removed line of the SAME text, seed.go:269) — and the whole-object counting (more than one file, or more than one hunk in one file, is more than one place). The refusal carries the count and exits 2:

```
internal/review/seed.go:47:	return fmt.Sprintf("seed makes %d edits, want exactly 1", e.Edits)
cmd/nova-review/mutate.go:178:	case errors.As(err, &count):
cmd/nova-review/mutate.go:179:		return refuseMutate(errOut, count.Error())
cmd/nova-review/mutate.go:209:func refuseMutate(w io.Writer, reason string) int {
cmd/nova-review/mutate.go:210:	fmt.Fprintf(w, "MUTATE REFUSED: %s\n", oneline.Escape(reason))
cmd/nova-review/mutate.go:211:	return 2
```

and the count is printed on the verdict line, `edits=%d` (cmd/nova-review/mutate.go:189). Note: the code prints `MUTATE ... edits=1` / `MUTATE REFUSED: seed makes <n> edits, want exactly 1`, matching rule 9's own grammar block at docs/SPEC-TOOLWORK.md:409-410 and docs/SPEC-REVIEW.md:700-701 verbatim; rule 7's parenthetical `ACCEPT SEED`/`ACCEPT REFUSED:` wording is the spec's loose shorthand for the accept selftest's seed lines (rule 6), and no `ACCEPT*` line is printed by any code in this tree — the verb that asserts the count is `nova-review mutate`.
GUARDED-BY cmd/nova-review/mutate_test.go:314 TestMutateSeedRefusesTwoEdits
GUARDED-BY cmd/nova-review/mutate_test.go:349 TestMutateSeedRefusesZeroEdits
GUARDED-BY cmd/nova-review/mutate_test.go:374 TestMutateSeedCountsAMovedLineAsOneEdit
GUARDED-BY cmd/nova-review/mutate_test.go:878 TestMutateSeedRefusesTwoFiles
GUARDED-BY cmd/nova-review/mutate_test.go:905 TestMutateSeedRefusesTwoHunksInOneFile
GUARDED-BY cmd/nova-review/mutate_test.go:932 TestMutateSeedRefusesALineMovedBetweenFiles
(also internal/docs/mutate_lines_test.go:16 TestMutateLinesAreDocumented guards the `MUTATE ... seed=<hex8> edits=<n> ...` line and the `--seed` CLI flags reaching docs/SPEC-REVIEW.md and docs/CLI.md; and cmd/nova-review/mutate_test.go:278 TestMutateSeedPassesWhenTheSeedTurnsThePackageRed asserts the `edits=1` suffix on the PASS line. All confirmed passing: `GOMAXPROCS=8 go test ./internal/docs/ -count=1 -run TestMutateLinesAreDocumented` and `GOMAXPROCS=8 go test ./cmd/nova-review/ -count=1 -run 'TestMutateSeedRefusesTwoEdits|TestMutateSeedRefusesZeroEdits|TestMutateSeedCountsAMovedLineAsOneEdit|TestMutateSeedRefusesTwoFiles|TestMutateSeedRefusesTwoHunksInOneFile|TestMutateSeedRefusesALineMovedBetweenFiles|TestMutateSeedPassesWhenTheSeedTurnsThePackageRed'` both ok.)
Greps run: `grep -rn "want exactly 1" --include='*.go' .`; `grep -rn "ACCEPT SEED\|ACCEPT REFUSED" --include='*.go' .` (no Go hits; the strings appear only in docs/SPEC-TOOLWORK.md); `grep -rn "MUTATE REFUSED\|MutateSeed\|edits=" --include='*.go' cmd/nova-review/ internal/review/`; `grep -rn "selftest" --include='*.go' .`; `grep -rn "testdata/accept\|ACCEPT OK" --include='*.go' .`; `grep -rn "rule 7\|edits\|seed\|mutate" --include='*.go' internal/docs/`; `grep -rn "func Test\|countEdits\|SeedCountError" --include='*_test.go' internal/review/`. Files read: internal/review/seed.go (full), cmd/nova-review/mutate.go (full), cmd/nova-review/mutate_test.go (lines 278-407, 870-969), internal/docs/mutate_lines_test.go (full), docs/SPEC-TOOLWORK.md:365-425, docs/SPEC-REVIEW.md:693-712, 878-884.
Left owed
```