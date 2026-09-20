RESULT tools22-rule-version-2 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:177
SPEC docs/SPEC-VERSION.md:166 rule 2
PKG cmd/nova-version
ASK The `nova-version snapshot` verb must run each `nova-*` binary's own `version` and record the stamp (and revision and platform) that printed line reports, so a file renamed to `nova-*` cannot forge its row from its file name.
Deciding lines (internal/update/snapverb.go:177-181):
	stamp, revision, platform, ok := parseVersionLine(p.Stdout)
	if !ok {
		return refusal(...)
	}
	rows = append(rows, snapRow{e.Name(), stamp, revision, platform})
And snapverb.go:53-55: "Name is the executable's file name; stamp, revision and platform are read off the four-token line, so a renamed stub cannot forge a row."
GUARDED-BY internal/update/inventory_spec_test.go:106 TestSnapshotReadsVersionNotTheFileName
Greps: grep -rn "TestSnapshotReadsVersionNotTheFileName" --include='*.go' . ; grep -n "func .*[Ss]napshot|version line|goos|platform|parseVersion|readVersion" internal/update/*.go ; grep -n "func specStub|func specRun" internal/update/*.go
Test run: GOMAXPROCS=8 go test ./internal/update/ -count=1 -run TestSnapshotReadsVersionNotTheFileName -v -> PASS
Left owed