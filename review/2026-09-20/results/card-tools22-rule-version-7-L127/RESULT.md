"RESULT tools22-rule-version-7-L127 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 7 says?
CONFORMS internal/update/diffverb.go:15
SPEC docs/SPEC-VERSION.md:127 rule 7
PKG cmd/nova-version
ASK The `diff` verb must refuse any input file that is not a valid snapshot (missing file, wrong header, or wrong-arity row), naming the file and `snapshot` as the command that writes one, exiting 2, printing no changed lines, and only reading the two files, never writing to them.
Deciding lines:
- `internal/update/diffverb.go:23-24`: `if !sc.Scan() || sc.Text() != snapshotHeader { return nil, fmt.Errorf("its header is not %q", snapshotHeader) }` — wrong header detection
- `internal/update/diffverb.go:33-34`: `if len(f) != 4 { return nil, fmt.Errorf("a row has %d fields, not 4", len(f)) }` — wrong arity detection
- `internal/update/diffverb.go:70-75`: Each error calls `refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot ...)", ..., ...))` — names the file, names `snapshot`, exit 2 (via `refusal` at `internal/update/cli.go:53-56` returning 2)
- `internal/update/diffverb.go:69-76`: Error return is before any `DIFF CHANGED` output (lines 97-104), so no changed lines are printed on refusal
- `internal/update/diffverb.go:15-17` (`readSnapshotFile` uses `os.Open` only, never writes to files): the two files are read, never written
GUARDED-BY internal/update/inventory_spec_test.go:247 TestDiffRefusesANonSnapshotFile (covers wrong header and wrong arity; missing-file case handled by same code path via `os.Open` failure)
Grep: `grep -rn "snapshotHeader\|readSnapshotFile\|diffVerb\|func refusal" --include='*.go' internal/update/`; `ls cmd/nova-version/`
Left owed: none