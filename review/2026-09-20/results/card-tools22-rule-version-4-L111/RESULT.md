RESULT tools22-rule-version-4-L111 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:189
SPEC docs/SPEC-VERSION.md:111 rule 4
PKG cmd/nova-version
ASK `snapshot` must refuse, at exit 2, a --bin whose binaries report more than one stamp, naming one differing pair of binaries and their stamps, and must write no --out.
internal/update/snapverb.go:188-190:
	`for i := 1; i < len(rows); i++ {`
	`if rows[i].stamp != rows[0].stamp {`
	`return refusal(errs, "SNAPSHOT", fmt.Errorf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update apply --sha, or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))`
refusal() returns 2 (internal/update/cli.go:55) and this return precedes os.WriteFile(outPath) at snapverb.go:197, so no --out is written for a mixed set; the refusal names both binaries (rows[0].name, rows[i].name) and both stamps, and carries the spec's remedy.
GUARDED-BY internal/update/inventory_spec_test.go:124 TestSnapshotRefusesAMixedSetNamingThePair (passes: go test ./internal/update/ -run TestSnapshotRefusesAMixedSetNamingThePair)
greps: grep -rn "SNAPSHOT OK" . ; grep -rn "snapshot" --include='*.go' internal/update/ ; grep -rn "func refusal" --include='*.go' internal/update/ ; grep -rn "mixed stamps" --include='*_test.go' .
Left owed
git status --short