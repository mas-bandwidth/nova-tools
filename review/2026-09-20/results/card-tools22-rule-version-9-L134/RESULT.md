RESULT tools22-rule-version-9-L134 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 9 says?
CONFORMS internal/update/snapverb.go:188
SPEC docs/SPEC-VERSION.md:134 rule 9
PKG cmd/nova-version
ASK An implementation must make a bin's mixed stamps and a binary whose `version` answers in a syntax of its own both visible in one command — refusing the mixed set naming the pair and refusing the non-conforming version line naming the tool — instead of recording them as one conforming set.
Deciding lines:
  internal/update/snapverb.go:188-189 (four stamps cannot be recorded as one set):
    for i := 1; i < len(rows); i++ {
        if rows[i].stamp != rows[0].stamp {
            return refusal(errs, "SNAPSHOT", fmt.Errorf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update apply --sha, or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))
  internal/update/snapverb.go:177-179 (the `nova-wake version` in its own syntax is named, not recorded):
    stamp, revision, platform, ok := parseVersionLine(p.Stdout)
    if !ok {
        return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (it printed no version line: want `<tool> <stamp> <goos>/<goarch> <go version>` ...) ...", e.Name(), e.Name()))
  internal/update/snapverb.go:200 (the one command that shows the count and the one identity):
    fmt.Fprintf(out, "SNAPSHOT OK bin=%s out=%s tools=%d stamp=%s\n", field(bin), field(outPath), len(rows), field(rows[0].stamp))
GUARDED-BY internal/update/inventory_spec_test.go:124 TestSnapshotRefusesAMixedSetNamingThePair
GUARDED-BY internal/update/inventory_spec_test.go:163 TestSnapshotRefusesABinaryWithNoVersion (subtest "no parseable line")
Both confirmed passing: GOMAXPROCS=8 go test ./internal/update/ -count=1 -run 'TestSnapshotRefusesAMixedSetNamingThePair|TestSnapshotRefusesABinaryWithNoVersion|TestDiffNamesOneLinePerChangedBinary|TestSnapshotWritesOneRowPerBinary' — PASS.
Greps run:
  grep -rn "nova-wake\|four stamps\|sixteen\|mixed\|SNAPSHOT OK\|DIFF CHANGED" --include='*.go' cmd/nova-version/
  grep -rn "SNAPSHOT OK\|DIFF CHANGED\|DIFF OK\|refusing\|mixed\|parseable" --include='*.go' internal/update/
  grep -rn "func Test" --include='*_test.go' cmd/nova-version/ internal/update/ | grep -i "snap\|diff\|mixed\|parse\|friend\|stamp"
Files read: cmd/nova-version/main.go (delegates to internal/update), internal/update/snapverb.go, internal/update/diffverb.go, internal/update/inventory_spec_test.go, cmd/nova-version/friendsequence_test.go.
Left owed