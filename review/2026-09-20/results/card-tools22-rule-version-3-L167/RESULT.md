RESULT tools22-rule-version-3-L167 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:188
SPEC docs/SPEC-VERSION.md:167 rule 3
PKG cmd/nova-version (via internal/update)
ASK an implementation must refuse (exit 2) when snapshot finds binaries with different stamps in one directory, printing both names and both stamps in the error and not writing the --out file.

Deciding lines:
    internal/update/snapverb.go:187-190: sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
    internal/update/snapverb.go:188:     if rows[i].stamp != rows[0].stamp {
    internal/update/snapverb.go:189:         return refusal(errs, "SNAPSHOT", fmt.Errorf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update apply --sha, or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))

GUARDED-BY internal/update/inventory_spec_test.go:124 TestSnapshotRefusesAMixedSetNamingThePair

Greps run:
  grep -rn "mixed" --include='*.go' cmd/nova-version/           -> no output
  grep -rn "RefusesAMixedSet\|stamp.*mismatch\|two stamp" --include='*.go' cmd/nova-version/ -> no output
  grep -rn "Mixed\|mixedSet\|TwoStamps\|two.*stamp\|different.*stamp\|stamp.*pair" --include='*.go' internal/update/ -> found internal/update/inventory_spec_test.go:123
  grep -n "mixed stamp\|rows\[i\].stamp != rows" internal/update/snapverb.go -> line 188-189
  GOMAXPROCS=8 go test ./internal/update/ -count=1 -run TestSnapshotRefusesAMixedSetNamingThePair -> PASS

git status --short
 ?? .lease
 ?? .nova-sandbox-tmp/
 ?? RESULT.md
 ?? harness-output.log
 ?? opencode.json
 ?? repo/
