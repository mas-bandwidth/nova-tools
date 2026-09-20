"RESULT tools22-rule-version-7 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 7 says?
CONFORMS internal/update/diffverb.go:94-95,101

SPEC docs/SPEC-VERSION.md:171 rule 7
PKG cmd/nova-version

ASK comparing two snapshot files, for each binary present in both with differing stamps, print exactly one `DIFF CHANGED name=<bin> from=<old> to=<new>` line, and print nothing for unchanged binaries.

Deciding lines:

- `internal/update/diffverb.go:94-95`: `case inA && inB && a == b: continue` — unchanged binaries print no output.
- `internal/update/diffverb.go:101`: `fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=%s\n", field(n), field(a.stamp), field(b.stamp))` — changed binary prints one line naming it and both stamps.

GUARDED-BY internal/update/inventory_spec_test.go:221 TestDiffNamesOneLinePerChangedBinary

Grep searches run:
- `grep -rn "DIFF CHANGED" --include='*.go' internal/update/`
- `grep -rn "func Test" --include='*_test.go' cmd/nova-version/`
- `grep -rn "TestDiffNamesOneLinePerChangedBinary" --include='*_test.go' .`

Left owed: None.

git status --short: (nothing printed)