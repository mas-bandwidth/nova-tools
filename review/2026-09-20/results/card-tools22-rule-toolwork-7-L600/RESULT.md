RESULT tools22-rule-toolwork-7-L600 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 7 says?
GAP internal/hygiene/hygiene.go:85 — two of three expected callers are absent; the third exists as `nova-check hygiene`.

SPEC docs/SPEC-TOOLWORK.md:600 rule 7
PKG internal/hygiene
ASK An implementation must provide a single internal/hygiene.Check function that performs four checks (identity, out-of-path, stray-file, secret) and is called from exactly three callers: the accept gate at harvest, nova-merge batch on each member, and the nova-check hygiene verb.

## Deciding lines

`internal/hygiene/hygiene.go:85` — the Check function:
```go
func Check(ctx context.Context, o Options) ([]Finding, error) {
```

The function body runs the four checks in sequence (lines 120–146):
- `checkIdentity` (line 121) — every commit carries the pool's identity and none is a merge
- `checkPaths` (line 132) — out-of-path check when Paths is non-empty, skipped with `paths=-` otherwise
- `checkStray` (line 134) — stray-file detection: symlinks, submodules, modes != 100644/100755, files over 1 MiB, stray list matches
- `checkAddedLines` (line 142) — conflict markers and key shapes in added lines; matched text never printed

The comment at lines 11–16 claims "One implementation and three callers -- `nova-pulse accept` at harvest, `nova-merge batch` on every member, and `nova-check hygiene` for a person before they ask for a read".

### Callers found (one of three)

`cmd/nova-check/hygiene.go:120` — direct call to `hygiene.Check`:
```go
findings, err := hygiene.Check(ctx, hygiene.Options{
    Repo: *repo, Base: *base, Head: *head, Paths: paths, Identities: ids, Kind: *kind,
})
```

### Callers missing (two of three)

1. **accept** (`nova-pulse accept` at harvest): `internal/pulse/harvestguard.go:28-29` explicitly states "#1650's `accept` gate between verify and push" is NOT yet implemented ("WHAT THIS STILL DOES NOT DO"). The accept lane belongs to toolwork T05 (#1650), not present at this base.

2. **nova-merge batch**: The `batchGate` in `cmd/nova-merge/batch.go:70-84` defines five steps (build, vet, vet-windows, test, lisp). None of them invoke `hygiene.Check` or `nova-check hygiene`. No import of `internal/hygiene` exists anywhere in `cmd/nova-merge/`.

## Tests

Red tests from the spec and their location:

| Spec test name | Test func | Location |
|---|---|---|
| hygiene-rejects-a-foreign-committer | TestHygieneRejectsAForeignCommitter | hygiene_test.go:116 |
| hygiene-rejects-a-merge-commit | TestHygieneRejectsAMergeCommit | hygiene_test.go:161 |
| hygiene-counts-a-rename-on-both-sides | TestHygieneCountsARenameOnBothSides | hygiene_test.go:185 |
| hygiene-rejects-result-md-in-the-diff | TestHygieneRejectsResultMDInTheDiff | hygiene_test.go:239 |
| hygiene-never-prints-the-secret | TestHygieneRejectsAKeyShapeAndNeverPrintsIt | hygiene_test.go:363 |
| paths-line-refuses-dotdot-and-bare-doublestar | TestValidatePathsRefusesDotDotAndBareDoubleStar | hygiene_test.go:419 |
| hygiene-rejects-a-conflict-marker | TestHygieneRejectsAConflictMarker | hygiene_test.go:322 |
| hygiene-rejects-a-file-over-one-mebibyte | TestHygieneRejectsAFileOverOneMebibyte | hygiene_test.go:271 |
| hygiene-rejects-a-symlink | TestHygieneRejectsASymlink | hygiene_test.go:287 |
| hygiene-rejects-a-submodule | TestHygieneRejectsASubmodule | hygiene_test.go:304 |
| hygiene-rejects-mode-100600 | TestModeFindingRejectsAModeGitWillNotWrite | hygiene_test.go:471 |
| hygiene-rejects-a-symlink-and-a-submodule | (covered by separate tests above) | see above |

Tests not in hygiene_test.go but named in spec (live elsewhere):
- launch-refuses-a-pool-with-no-identity
- staged-clone-ignores-the-bench-gitconfig  
- stage-refuses-a-symlink-out-of-the-job
- secret-quarantines-and-never-deletes

## Unguarded conformance notes

The package signature evolved from the spec's `Check(repo, base, head, paths, identity) []Finding` to `Check(ctx context.Context, o Options) ([]Finding, error)` — wrapping positional args into a struct. The structural contract (one implementation) holds within the package itself, guarded by the many unit tests above. But the external contract ("three callers") does not hold.

## Greps run

```
grep -rn "hygiene" --include='*.go' . | head -40     # all hygiene mentions
grep -rn "hygiene\.Check\b" --include='*.go' .        # callers of Check
find . -type d -name hygiene                            # package location
grep -rn "^func Test.*Hygiene\|^func Test.*hygiene"    # test names
grep -rn "result.md\|RESULT.md" --include='*.go'       # RESULT.md references
grep -rn "quarantine\|Quarantine" --include='*.go'     # quarantine logic
grep -rn "accept" --include='*.go' internal/pulse/     # accept gate search
grep -rn "internal/hygiene" --include='*.go' .         # imports of hygiene pkg
```

Left owed: Implement the `accept` gate (#1650) and/or have `nova-merge batch` invoke `hygiene.Check` on each member so the "one implementation, three callers" invariant is complete. Right now only `nova-check hygiene` has this door open.

git status --short
