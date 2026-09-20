RESULT tools22-rule-toolwork-9 sha=5298f6be12ea
CONFORMS cmd/nova-review/mutate.go:150
SPEC docs/SPEC-TOOLWORK.md:404 rule 9
PKG internal/docs
ASK nova-review mutate must provide a --seed form that applies a unified diff via git apply, counts edits from the applied patch (not the header), asserts exactly one edit, refuses otherwise with MUTATE REFUSED: seed makes <n> edits, want exactly 1, and prints MUTATE <head8> seed=<sha8> edits=1 red=<n> green=<n> <PASS|FAIL>; it must also include reverted=<n> on the range form's verdict line, and mutate must write nothing into the pointed-at repo.

Deciding lines:

Range form with reverted=<n> (cmd/nova-review/mutate.go:150):
```
line := fmt.Sprintf("MUTATE %s reverted=%d red=%d green=%d %s%s\n", review.Short(res.Head), res.Reverted, res.Red, res.Green, selected, res.Verdict())
```
reverted is populated by countHunks (internal/review/mutate.go:227-230), counting @@ hunks in the base..head diff restricted to non-test files (internal/review/mutate.go:632-659).

Seed form verdict line (cmd/nova-review/mutate.go:189):
```
line := fmt.Sprintf("MUTATE %s seed=%s edits=%d red=%d green=%d %s\n", review.Short(res.Head), res.Seed, res.Edits, res.Red, res.Green, res.Verdict())
```

Edit count assertion (internal/review/seed.go:46-49, 190-192):
```
func (e *SeedCountError) Error() string {
    return fmt.Sprintf("seed makes %d edits, want exactly 1", e.Edits)
}
...
res.Edits = countEdits(applied, placed)
if res.Edits != 1 {
    return res, &SeedCountError{Edits: res.Edits}
}
```

Count never from patch file header (internal/review/seed.go:178-190): reads `git diff --cached --numstat` and `git diff --cached -U0` from the applied tree — not the patch header. countEdits at internal/review/seed.go:253-276 implements the per-place arithmetic.

Applied with git apply (internal/review/seed.go:168):
```
if _, err := gitOut(ctx, wt, "apply", "--whitespace=nowarn", seedPath); err != nil {
```

Writes nothing (range: internal/review/mutate.go:213-220 — throwaway worktree removed via defer; seed: internal/review/seed.go:128 — private clone removed via defer; and internal/review/seed.go:114-134 — uses --shared clone, not a linked worktree, so the pointed-at repo's .git is never touched.

GUARDED-BY cmd/nova-review/mutate_test.go:225 TestMutatePrintsRevertedCount
GUARDED-BY cmd/nova-review/mutate_test.go:314 TestMutateSeedRefusesTwoEdits
GUARDED-BY cmd/nova-review/mutate_test.go:278 TestMutateSeedPassesWhenTheSeedTurnsThePackageRed
GUARDED-BY cmd/nova-review/mutate_test.go:349 TestMutateSeedRefusesZeroEdits
GUARDED-BY cmd/nova-review/mutate_test.go:402 TestMutateSeedWritesNothingIntoTheRepo
GUARDED-BY internal/docs/mutate_lines_test.go:16 TestMutateLinesAreDocumented

Grep searches run:
  grep -rn "MUTATE" --include='*.go' .
  grep -rn "mutate" --include='*.go' .
  grep -rn "nova-review" --include='*.go' .

Files read:
  cmd/nova-review/mutate.go (the verb dispatch and output formatting)
  internal/review/mutate.go (the range form logic, countHunks, revert)
  internal/review/seed.go (the seed form logic, countEdits, git apply, worktree cleanup)
  internal/docs/mutate_lines_test.go (doc conformance test)
  docs/SPEC-TOOLWORK.md:384-438 (rule 9 and context)

Left owed: none

git status --short