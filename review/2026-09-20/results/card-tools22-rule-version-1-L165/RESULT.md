RESULT tools22-rule-version-1-L165 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:139

SPEC docs/SPEC-VERSION.md:165 rule 1
PKG cmd/nova-version

ASK The snapshot command must scan a directory for files named nova-*, execute each to parse its version output into stamp/revision/platform fields, write a TSV header followed by one data row per binary sorted by name, and skip non-nova-* entries.

--- Deciding lines ---

internal/update/snapverb.go:139-182 (filtering and collecting rows):
	if len(rows) == 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("--bin %s holds no nova-* regular file (supply a readable --bin: a directory of nova-* executables)", bin))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

internal/update/snapverb.go:193-196 (writing header + rows):
	b.WriteString(snapshotHeader + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.name, r.stamp, r.revision, r.platform)
	}

internal/update/snapverb.go:51 (header constant):
	const snapshotHeader = "name\tstamp\trevision\tplatform"

internal/update/snapverb.go:140 (skipping non-nova-* files):
		if e.IsDir() || !strings.HasPrefix(e.Name(), "nova-") {
			continue
		}

GUARDED-BY internal/update/inventory_spec_test.go:71 TestSnapshotWritesOneRowPerBinary

--- Greps run ---

grep -rn "snapshot\|Snapshot" --include='*.go' . internal/update/ | grep snapverb
grep -rn "func TestSnapshotWritesOneRowPerBinary" --include='*.go' .
grep -rn "func Test" --include='*_test.go' cmd/nova-version/
ls cmd/nova-version/
sed -n '145,191p' docs/SPEC-VERSION.md

Left owed
git status --short
