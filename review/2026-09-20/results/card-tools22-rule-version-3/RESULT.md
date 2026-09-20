RESULT tools22-rule-version-3 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 3 says?
CONFORMS internal/update/snapverb.go:189
SPEC docs/SPEC-VERSION.md:167 rule 3
PKG cmd/nova-version
ASK When a `snapshot --bin` holds two `nova-*` binaries answering two different version stamps, the command must exit 2 naming both binary names and both stamps, and write no `--out` file.
internal/update/snapverb.go:187-191:
    for i := 1; i < len(rows); i++ {
        if rows[i].stamp != rows[0].stamp {
            return refusal(errs, "SNAPSHOT", fmt.Errorf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update apply --sha, or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))
The mismatch returns before internal/update/snapverb.go:197 `os.WriteFile(outPath, ...)`, so no `--out` is written; `refusal` at internal/update/cli.go:53-56 prints `SNAPSHOT REFUSED: ...` and returns exit code 2, naming both names and both stamps.
GUARDED-BY internal/update/inventory_spec_test.go:124 TestSnapshotRefusesAMixedSetNamingThePair
Grep: `grep -rn "refusing" --include='*.go' cmd/nova-version/` (no hits; logic lives in internal/update); `grep -rn "mixed\|stamp\|refusing to guess" --include='*.go' internal/update/`; `grep -rn "func Test" --include='*_test.go' cmd/nova-version/ internal/update/`. Read: cmd/nova-version/main.go, internal/update/snapverb.go (full), internal/update/cli.go:45-56, internal/update/inventory_spec_test.go:124-137, docs/SPEC-VERSION.md:147-193.
Left owed.

`git status --short` (from repo/, must print nothing):