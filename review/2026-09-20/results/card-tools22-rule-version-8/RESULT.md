RESULT tools22-rule-version-8 sha=5298f6be12ea
CONFORMS internal/update/diffverb.go:97
SPEC docs/SPEC-VERSION.md:172 rule 8
PKG cmd/nova-version
ASK The diff verb must print exactly one `DIFF CHANGED` line per binary present on only one side of `--from`/`--to`, with `-` in the absent side's slot, and count each such binary in the `changed=` total.

Deciding lines (internal/update/diffverb.go):
    97: 			fmt.Fprintf(out, "DIFF CHANGED name=%s from=- to=%s\n", field(n), field(b.stamp))
    99: 			fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=-\n", field(n), field(a.stamp))
   103: 			changed++
   105: 	fmt.Fprintf(out, "DIFF OK from=%s to=%s tools=%d changed=%d\n", field(from), field(to), len(ordered), changed)

Case `!inA` (diffverb.go:96) prints one line with `from=-` for a binary only in `--to`; case `!inB` (diffverb.go:98) prints one line with `to=-` for a binary only in `--from`; every printed change increments `changed` (line 103) so an added and a removed binary together make `changed=2`, printed on the DIFF OK line (line 105).

GUARDED-BY internal/update/inventory_spec_test.go:236 TestDiffNamesAddedAndRemoved
The test asserts `DIFF CHANGED name=gone from=v1.0.0 to=-`, `DIFF CHANGED name=new from=- to=v2.0.0` and `changed=2`, and passes (`go test ./internal/update/ -count=1 -run TestDiffNamesAddedAndRemoved`: PASS).

Greps run:
- `grep -rn "diff|DIFF|changed=" --include='*.go' cmd/nova-version/`
- `grep -rn "DIFF CHANGED|changed=|"DIFF "" --include='*.go' .`
- `grep -rn "TestDiffNamesAddedAndRemoved|TestDiffNamesOneLinePerChangedBinary|TestDiffRefuses" --include='*.go' .`
- `grep -rn "diffVerb|\"diff\"" --include='*.go' .`

Left owed: none.