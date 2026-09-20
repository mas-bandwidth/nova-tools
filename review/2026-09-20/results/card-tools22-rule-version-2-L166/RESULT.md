RESULT tools22-rule-version-2-L166 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:177-181
SPEC docs/SPEC-VERSION.md:166 rule 2
PKG cmd/nova-version
ASK Snapshot must record the stamp that the binary's `version` command actually prints, not infer it from the binary's filename.
QUOTE snapverb.go:177-181: `stamp, revision, platform, ok := parseVersionLine(p.Stdout)` then `rows = append(rows, snapRow{e.Name(), stamp, revision, platform})` — the stamp comes from parsing the binary's own stdout, not from its file name.
GUARDED-BY internal/update/inventory_spec_test.go:106 TestSnapshotReadsVersionNotTheFileName
GREPS: `grep -rn "TestSnapshotReadsVersionNotTheFileName" --include='*.go' .` → internal/update/inventory_spec_test.go:105-121; `grep -rn "snapshot\|Snapshot" --include='*.go' cmd/nova-version/` found nothing (snapshot lives in internal/update/); `ls cmd/nova-version/` listed test files but no snapshot test there; `grep -rn "func Test" --include='*_test.go' cmd/nova-version/` returned 4 test files with 9 tests, none named SnapshotReadsVersionNotTheFileName.
Left owed: none.
`git status --short` prints nothing.