RESULT tools22-rule-version-3-L105 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:192
SPEC docs/SPEC-VERSION.md:105 rule 3
PKG cmd/nova-version
ASK the snapshot verb must write a TSV file with header `name\tstamp\trevision\tplatform` and one row per nova-* binary sorted by name, with stamp=build identity, revision=12-hex commit or "-", platform=goos/goarch, and print `SNAPSHOT OK bin=<dir> out=<path> tools=<n> stamp=<stamp>` to stdout.
The header is written at internal/update/snapverb.go:51 (`snapshotHeader = "name\tstamp\trevision\tplatform"`), rows are built with tabs at line 195 (`fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.name, r.stamp, r.revision, r.platform)`), sorted by name at line 186, and the stdout line is printed at line 200 (`fmt.Fprintf(out, "SNAPSHOT OK bin=%s out=%s tools=%d stamp=%s\n", ...)`). Revision extraction is at line 62 (`revisionOf`), stamp is from `buildinfo.Parse` at line 89, platform is the third token `goos/goarch` from the same parse.
GUARDED-BY internal/update/inventory_spec_test.go:71 TestSnapshotWritesOneRowPerBinary
Grep commands ran: `grep -rn "SNAPSHOT OK" --include='*.go' .`, `grep -rn "snapshot" --include='*.go' cmd/nova-version/`, `grep -rn "func Test" --include='*_test.go' cmd/nova-version/`
Left owed: none
git status --short: (no output)