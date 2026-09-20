RESULT tools22-rule-version-7-L171 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3
CONFORMS internal/update/diffverb.go:90
SPEC docs/SPEC-VERSION.md:171 rule 7
PKG cmd/nova-version
ASK The diff verb must compare two snapshot TSV files and print exactly one `DIFF CHANGED name=<name> from=<stamp1> to=<stamp2>` line for each binary whose row differs between snapshots (using `-` where absent), and print nothing at all for binaries present identically in both.

deciding lines (internal/update/diffverb.go:90–104):
    for _, n := range ordered {
        a, inA := before[n]
        b, inB := after[n]
        switch {
        case inA && inB && a == b:
            continue
        case !inA:
            fmt.Fprintf(out, "DIFF CHANGED name=%s from=- to=%s\n", field(n), field(b.stamp))
        case !inB:
            fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=-\n", field(n), field(a.stamp))
        default:
            fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=%s\n", field(n), field(a.stamp), field(b.stamp))
        }
        changed++
    }

lines 90–104 iterate all unique names across both snapshots in sorted order; line 94 skips identical rows entirely (no output), while lines 96–101 emit exactly one `DIFF CHANGED` line per non-matching binary with the correct format including `-` for absent sides.

GUARDED-BY internal/update/inventory_spec_test.go:221 TestDiffNamesOneLinePerChangedBinary

grep -rn "DIFF CHANGED" --include='*.go' cmd/nova-version/   => (no output — dispatch through cli.go)
grep -rn "diffVerb\|Diff\|diff" --include='*.go' internal/update/cli.go  => cli.go:171-175 routes verb=="diff" to diffVerb()
grep -rn "func Test.*Diff" --include='*_test.go' internal/update/  => inventory_spec_test.go:221, :236, :247

Left owed
git status --short
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json
?? repo/
