RESULT tools22-rule-version-9 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 9 say?
CONFORMS internal/update/diffverb.go:23
SPEC docs/SPEC-VERSION.md:173 rule 9
PKG cmd/nova-version
ASK The diff verb must call readSnapshotFile() on each input file and produce exit 2 with a stderr message naming the offending file and the "nova-version snapshot" remedy when either the first-line header does not match or a data row has fewer/more than 4 tab-separated fields.

deciding lines:
  internal/update/diffverb.go:23	if !sc.Scan() || sc.Text() != snapshotHeader {
  internal/update/diffverb.go:24:		return nil, fmt.Errorf("its header is not %q", snapshotHeader)
  internal/update/diffverb.go:38:		f := strings.Split(line, "\t")
  internal/update/diffverb.go:39:		if len(f) != 4 {
  internal/update/diffverb.go:40:			return nil, fmt.Errorf("a row has %d fields, not 4", len(f))
  internal/update/diffverb.go:71:		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", from, err))
  internal/update/diffverb.go:75:		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", to, err))
  internal/update/cli.go:53:// refusal prints DIFF REFUSED: <msg> to stderr and returns 2.
  internal/update/cli.go:55:	fmt.Fprintf(w, "%s REFUSED: %s\n", token, oneline.Err(err))
  internal/update/cli.go:56:	return 2
  internal/update/snapverb.go:51:const snapshotHeader = "name\tstamp\trevision\tplatform"

GUARDED-BY internal/update/inventory_spec_test.go:247 TestDiffRefusesANonSnapshotFile

grep -rn "snapshot\|header\|arity" --include='*.go' cmd/nova-version/friendsequence_test.go
cmd/nova-version/friendsequence_test.go:15:// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL: snapshot the binaries in a bin
cmd/nova-version/friendsequence_test.go:43:	snap := filepath.Join(dir, "snapshot.tsv")
cmd/nova-version/friendsequence_test.go:45:	if code := update.Main("nova-version", []string{"snapshot", "--bin", bin, "--out", snap}, "", &out, &errs); code != 0 {
cmd/nova-version/friendsequence_test.go:46:		t.Fatalf("snapshot: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
cmd/nova-version/friendsequence_test.go:49:		t.Fatalf("snapshot did not record both binaries:\n%s", out.String())

grep -rn "diff\|Diff\|DIFF" --include='*.go' . | grep 'diffverb\|cli.go:.*diff'
internal/update/cli.go:80:nova-version diff --from <a.tsv> --to <b.tsv>
internal/update/cli.go:171:	if verb == "diff" {
internal/update/cli.go:175:		return diffVerb(name, args, out, errs)
internal/update/diffverb.go:44:// diffVerb compares two snapshots and prints one line per changed binary. The
internal/update/diffverb.go:45:// closing DIFF OK counts the state -- every name on either side -- not the
internal/update/diffverb.go:47:func diffVerb(name string, args []string, out, errs io.Writer) int {
internal/update/diffverb.go:48:	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
internal/update/diffverb.go:54:		return refusal(errs, "DIFF", fmt.Errorf("%s (run %s help)", err, name))
internal/update/diffverb.go:64:		return refusal(errs, "DIFF", fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
internal/update/diffverb.go:67:		return refusal(errs, "DIFF", fmt.Errorf("diff takes no positional arguments (run %s help)", name))
internal/update/diffverb.go:71:		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", from, err))
internal/update/diffverb.go:75:		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", to, err))
internal/update/diffverb.go:97:			fmt.Fprintf(out, "DIFF CHANGED name=%s from=- to=%s\n", field(n), field(b.stamp))
internal/update/diffverb.go:99:			fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=-\n", field(n), field(a.stamp))
internal/update/diffverb.go:101:			fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=%s\n", field(n), field(a.stamp), field(b.stamp))
internal/update/diffverb.go:105:	fmt.Fprintf(out, "DIFF OK from=%s to=%s tools=%d changed=%d\n", field(from), field(to), len(ordered), changed)

Left owed
git status --short
