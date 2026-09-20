RESULT tools22-rule-toolwork-9-L404 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 9 says?
CONFORMS cmd/nova-review/mutate.go:189
SPEC docs/SPEC-TOOLWORK.md:404 rule 9
PKG internal/docs
ASK nova-review mutate must take a --seed <patch file> --tests <pkg>[,<pkg>...] [--timeout] form that applies the seed as a unified diff with `git apply` in a throwaway worktree, counts changed lines from the patch it applied (refusing with `MUTATE REFUSED: seed makes <n> edits, want exactly 1` unless that count is exactly one), prints `MUTATE <head8> seed=<sha8> edits=1 red=<n> green=<n> <PASS|FAIL>`, adds `reverted=<n>` to the range form's verdict line, and records/writes nothing into the repo it is pointed at.

Deciding lines:
- cmd/nova-review/mutate.go:189 — the seed verdict line: `line := fmt.Sprintf("MUTATE %s seed=%s edits=%d red=%d green=%d %s\n", review.Short(res.Head), res.Seed, res.Edits, res.Red, res.Green, res.Verdict())` (seed= is first 8 hex of the patch's SHA-256, internal/review/seed.go:104-105).
- cmd/nova-review/mutate.go:150 — the range verdict gains `reverted=%d`: `line := fmt.Sprintf("MUTATE %s reverted=%d red=%d green=%d %s%s\n", ...)`; the count is hunks put back, internal/review/mutate.go:227 `reverted, err := countHunks(ctx, repo, base, head, others)`.
- cmd/nova-review/mutate.go:65-79 — the seed form is exclusive to `--seed`/`--tests`, `--timeout` is wired at line 44, and `mutateSeed` runs it.
- internal/review/seed.go:48 — the refusal: `return fmt.Sprintf("seed makes %d edits, want exactly 1", e.Edits)`, printed as `MUTATE REFUSED: <reason>` at cmd/nova-review/mutate.go:210.
- internal/review/seed.go:129-134,168 — the seed is applied in a PRIVATE throwaway clone (`git clone --shared --no-checkout` into a temp dir, then `git apply --whitespace=nowarn seedPath`), never a linked worktree of the pointed-at repo.
- internal/review/seed.go:178-193 — the count is from the applied patch (`--numstat` + `-U0` of `git diff --cached` after `git apply`), never the patch header (comment at seed.go:17-19); `res.Edits = countEdits(applied, placed)` then `if res.Edits != 1 { return res, &SeedCountError{Edits: res.Edits} }`.
- internal/review/seed.go:83,128 — "It writes nothing into the repo it is pointed at, on every path"; the temp clone is removed via `safepath.RemoveUnder`.

GUARDED-BY cmd/nova-review/mutate_test.go:278 TestMutateSeedPassesWhenTheSeedTurnsThePackageRed (exact `MUTATE <head8> seed=... edits=1 red=1 green=0 PASS` line); cmd/nova-review/mutate_test.go:225 TestMutatePrintsRevertedCount (exact `reverted=1 red=1 green=1 PASS` line); cmd/nova-review/mutate_test.go:314 and :349 TestMutateSeedRefusesTwoEdits / TestMutateSeedRefusesZeroEdits (exact `MUTATE REFUSED: seed makes 2/0 edits, want exactly 1`); cmd/nova-review/mutate_test.go:402 TestMutateSeedWritesNothingIntoTheRepo (head unmoved, worktree clean); internal/docs/mutate_lines_test.go:16 TestMutateLinesAreDocumented (the lines live in docs/SPEC-REVIEW.md and docs/CLI.md).

Greps run:
- `grep -rn "MUTATE" --include='*.go' internal/docs/`
- `grep -rn "reverted=" --include='*.go' internal/docs/`
- `grep -rn "reverted=" --include='*.go' .` (whole tree)
- `grep -rn "MUTATE REFUSED"` (whole tree)
- `grep -rn "func MutateSeed|SeedCountError|SeedBuildError|reverted|Edits|git apply" --include='*.go' internal/review/`
- `grep -rn "func Test.*[Mm]utate|func Test.*[Ss]eed" --include='*.go' cmd/nova-review/`

Left owed
`git status --short` (in repo/) prints nothing.