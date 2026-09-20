RESULT tools22-rule-version-9-L173 sha=5298f6be12ea
CONFORMS internal/update/diffverb.go:23
SPEC docs/SPEC-VERSION.md:173 rule 9
PKG cmd/nova-version
ASK The diff verb must refuse, at exit 2 and naming the file and the `snapshot` remedy, both a snapshot file whose header is not `name	stamp	revision	platform` and one with any row of the wrong (4-field) arity.
CONFORMS internal/update/diffverb.go:23-25:
    if !sc.Scan() || sc.Text() != snapshotHeader {
        return nil, fmt.Errorf("its header is not %q", snapshotHeader)
    }
CONFORMS internal/update/diffverb.go:33-35:
    if len(f) != 4 {
        return nil, fmt.Errorf("a row has %d fields, not 4", len(f))
    }
CONFORMS internal/update/diffverb.go:71 (and 75 for --to): both errors are wrapped as
    refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", from, err))
which names the file and the `snapshot` remedy; refusal returns 2 (internal/update/cli.go:53-56).
GUARDED-BY internal/update/inventory_spec_test.go:247 TestDiffRefusesANonSnapshotFile (subtests "wrong header", "wrong arity", each asserts exit 2, the file name in stderr and "nova-version snapshot"; ran, passed)
GREPS: grep -rn "snapshot" --include='*.go' cmd/nova-version/; grep -rn "DIFF\|diff" --include='*.go' cmd/nova-version/; grep -rn "DIFF\|diff" --include='*.go' internal/update/; grep -rn "TestDiffRefusesANonSnapshotFile" --include='*_test.go' .; grep -rn "snapshotHeader\|func refusal" internal/update/*.go
READ: cmd/nova-version/main.go, internal/update/diffverb.go, internal/update/inventory_spec_test.go:240-279, internal/update/cli.go:40-69
Left owed: none