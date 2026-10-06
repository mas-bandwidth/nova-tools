package main

// landappend.go is what land does with the records and the tables lock at a merge
// (docs/SPEC-SPRINT.md section 7, the merge classes of a file; docs/STANDARD.md section
// 10). A record is of one of two classes. An append-only record (the deleted-tests log)
// only ever gains rows, and their order means nothing: .gitattributes marks it
// merge=union, so git keeps both sides' rows, and a conflict git still stops on (a tree
// without the attribute) is resolved here the same way, unionRecords over the three
// stages; after every merge a row it holds twice is taken out (dedupeRows, the sort -u of
// the merge without the reordering: the header stays the first line). A keyed table
// (tla/CASES.tsv, tla/RUNS.tsv) holds one row per config, rewritten in place when a case
// is run again or its plan row edited, and internal/tlc refuses a config named twice: it
// is merged by key, never by line (mergeKeyed). Each config's row is taken from the side
// that changed it; where both did, from the side whose run of that config is the newer
// by its started_utc in tla/RUNS.tsv. A config one side removed and the other changed,
// or one both rewrote with no newer run to choose by, is refused as any conflict is.
// git's line merge of a keyed table can finish and still name a config twice (two sides
// add it at different places), so after every merge a keyed table that does is merged by
// key again from the merge's three commits. The tables lock is a generated file whose
// comment is an append-only history: its family (landLedgers) seeds the conflict with
// both sides' comment lines over the tip's body (seedTablesLock), and its update run
// regenerates the body from the merged schema.
//
// Each resolution is a function of the three sides' bytes; the git around it is the
// lander's.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// appendOnlyRecords are the append-only records: rows only appended, order irrelevant.
var appendOnlyRecords = []string{"internal/ci/testdata/deleted-tests.txt"}

// runsTable is the keyed table whose rows carry their run's started_utc.
const runsTable = "tla/RUNS.tsv"

// keyedTables are the keyed tables: one row per config (the first column), the newer
// run's row where both sides rewrote one.
var keyedTables = []string{"tla/CASES.tsv", runsTable}

// appendOnly says p is an append-only record.
func appendOnly(p string) bool {
	return slices.Contains(appendOnlyRecords, p)
}

// keyed says p is a keyed table.
func keyed(p string) bool {
	return slices.Contains(keyedTables, p)
}

// recordPaths splits a merge's unmerged paths into the records (append-only or keyed)
// and the rest.
func recordPaths(paths []string) (records, rest []string) {
	for _, p := range paths {
		if appendOnly(p) || keyed(p) {
			records = append(records, p)
		} else {
			rest = append(rest, p)
		}
	}
	return records, rest
}

// unionRecords is an append-only record at a merge: the tip's (ours) rows in order, then
// each of the card's (theirs) rows the tip lacks, every row once (dedupeRows). nLeft is
// the rows the tip has beyond the base, nRight the rows the card added to the union.
func unionRecords(base, ours, theirs []byte) (out []byte, nLeft, nRight int) {
	b, _ := unionLines(base)
	o, oTrail := unionLines(ours)
	th, tTrail := unionLines(theirs)
	inBase := map[string]bool{}
	for _, l := range b {
		inBase[l] = true
	}
	seen := map[string]bool{}
	var rows []string
	for _, l := range o {
		if !seen[l] {
			seen[l] = true
			rows = append(rows, l)
			if !inBase[l] {
				nLeft++
			}
		}
	}
	for _, l := range th {
		if !seen[l] {
			seen[l] = true
			rows = append(rows, l)
			nRight++
		}
	}
	if len(rows) == 0 {
		return []byte{}, nLeft, nRight
	}
	s := strings.Join(rows, "\n")
	if oTrail || tTrail {
		s += "\n"
	}
	return []byte(s), nLeft, nRight
}

// dedupeRows is a record with each row once, at its first place, and how many rows were
// taken out.
func dedupeRows(b []byte) ([]byte, int) {
	lines, trailing := unionLines(b)
	seen := map[string]bool{}
	var kept []string
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			kept = append(kept, l)
		}
	}
	if len(kept) == len(lines) {
		return b, 0
	}
	s := strings.Join(kept, "\n")
	if trailing {
		s += "\n"
	}
	return []byte(s), len(lines) - len(kept)
}

// keyedRows is a keyed table's header, its configs in order and each config's row; a
// config named twice is an error.
func keyedRows(b []byte) (header string, keys []string, rows map[string]string, err error) {
	lines, _ := unionLines(b)
	rows = map[string]string{}
	for i, l := range lines {
		if i == 0 {
			header = l
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		k, _, _ := strings.Cut(l, "\t")
		if _, twice := rows[k]; twice {
			return "", nil, nil, fmt.Errorf("%s is named twice", k)
		}
		rows[k], keys = l, append(keys, k)
	}
	return header, keys, rows, nil
}

// keyedTwice is how many rows of a keyed table name a config an earlier row names.
func keyedTwice(b []byte) int {
	lines, _ := unionLines(b)
	seen, n := map[string]bool{}, 0
	for i, l := range lines {
		if i == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		k, _, _ := strings.Cut(l, "\t")
		if seen[k] {
			n++
		}
		seen[k] = true
	}
	return n
}

// runStarted is each config's run start in a RUNS.tsv, its started_utc; a row whose
// start does not read is left out.
func runStarted(b []byte) map[string]time.Time {
	lines, _ := unionLines(b)
	out := map[string]time.Time{}
	if len(lines) == 0 {
		return out
	}
	col := slices.Index(strings.Split(lines[0], "\t"), "started_utc")
	if col < 0 {
		return out
	}
	for _, l := range lines[1:] {
		f := strings.Split(l, "\t")
		if col >= len(f) {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, f[col]); err == nil {
			out[f[0]] = t
		}
	}
	return out
}

// mergeKeyed is a keyed table at a merge: the tip's (ours) configs in order, then the
// card's (theirs) the tip lacks, each config one row. A config's row is the side's that
// changed it from the base (either's when both made it the same); where both changed it,
// the side whose run of it is the newer (oursRun, theirsRun: each side's RUNS.tsv
// starts). nLeft is the configs taken as the tip changed them, nRight as the card did. A
// config one side removed and the other changed, one both rewrote with no newer run
// between them, and a header both sides changed are errors.
func mergeKeyed(base, ours, theirs []byte, oursRun, theirsRun map[string]time.Time) (out []byte, nLeft, nRight int, err error) {
	bh, _, b, err := keyedRows(base)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("the base's %v", err)
	}
	oh, oKeys, o, err := keyedRows(ours)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("the left side's %v", err)
	}
	th, tKeys, t, err := keyedRows(theirs)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("the right side's %v", err)
	}
	header := oh
	switch {
	case oh == th, th == bh:
	case oh == bh:
		header = th
	default:
		return nil, 0, 0, fmt.Errorf("both sides changed its header")
	}
	keys := slices.Clone(oKeys)
	for _, k := range tKeys {
		if _, in := o[k]; !in {
			keys = append(keys, k)
		}
	}
	rows := []string{header}
	for _, k := range keys {
		bl, inB := b[k]
		ol, inO := o[k]
		tl, inT := t[k]
		row, keep := ol, inO
		switch {
		case inO == inT && ol == tl:
		case inO == inB && ol == bl:
			row, keep = tl, inT
			nRight++
		case inT == inB && tl == bl:
			nLeft++
		case !inO || !inT:
			return nil, 0, 0, fmt.Errorf("%s was removed on one side and changed on the other", k)
		default:
			ot, oOK := oursRun[k]
			tt, tOK := theirsRun[k]
			switch {
			case oOK && tOK && ot.After(tt):
				nLeft++
			case oOK && tOK && tt.After(ot):
				row = tl
				nRight++
			default:
				return nil, 0, 0, fmt.Errorf("%s was rewritten on both sides with no newer run to choose by", k)
			}
		}
		if keep {
			rows = append(rows, row)
		}
	}
	if header == "" && len(rows) == 1 {
		return []byte{}, nLeft, nRight, nil
	}
	return []byte(strings.Join(rows, "\n") + "\n"), nLeft, nRight, nil
}

// recordLine is the land log's line for one record resolved at a conflict.
func recordLine(p string, nLeft, nRight int) string {
	if keyed(p) {
		return fmt.Sprintf("record %s: resolved by config, one row each, the newer run's where both sides rewrote one (+%d left, +%d right)", p, nLeft, nRight)
	}
	return fmt.Sprintf("record %s: resolved as the union of both sides' rows (+%d left, +%d right)", p, nLeft, nRight)
}

// recordNote is the card's note for a merge resolved in its records.
func recordNote(paths []string) string {
	return "the records " + strings.Join(paths, ", ") + " conflicted and were resolved at the merge by their class (an append-only record as the union of both sides' rows, a keyed table one row per config)"
}

// recordSentence is the merge commit's sentence for the resolved records.
func recordSentence(lines []string) string {
	return "The records conflicted and were resolved by their class: " + strings.Join(lines, "; ") + "."
}

// dedupeLine is the land log's line for a record whose repeated rows were taken out.
func dedupeLine(p string, n int) string {
	if keyed(p) {
		return fmt.Sprintf("record %s: %d config(s) named twice after the merge, merged by config to one row each", p, n)
	}
	return fmt.Sprintf("record %s: %d repeated row(s) taken out after the merge", p, n)
}

// dedupeNote is the card's note for that.
func dedupeNote(paths []string) string {
	return "the records " + strings.Join(paths, ", ") + " held a repeated row or config after the merge, taken out"
}

// blob is the file at spec (rev:path or :stage:path) in dir; nil where there is none.
func (l *lander) blob(ctx context.Context, dir, spec string) []byte {
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "show", spec)
	if err != nil {
		return nil
	}
	return res.Stdout
}

// resolveKeyed is the keyed table p merged by config (mergeKeyed) from its base (base, a
// spec) and the commits ours and theirs, each config's run start read from their
// RUNS.tsv.
func (l *lander) resolveKeyed(ctx context.Context, dir, p, base, ours, theirs string) ([]byte, int, int, error) {
	return mergeKeyed(l.blob(ctx, dir, base), l.blob(ctx, dir, ours+":"+p), l.blob(ctx, dir, theirs+":"+p),
		runStarted(l.blob(ctx, dir, ours+":"+runsTable)), runStarted(l.blob(ctx, dir, theirs+":"+runsTable)))
}

// mergeRecords resolves the records of a merge in progress in dir (paths, each
// unmerged): an append-only one read at its three stages (a missing stage is an empty
// side) and resolved as the union (unionRecords), a keyed one by config (resolveKeyed),
// each written and staged. lines is the land log's line per record; card is why one
// could not be resolved, env a failure that is not the card's.
func (l *lander) mergeRecords(ctx context.Context, dir string, paths []string) (lines []string, card, env string) {
	for _, p := range paths {
		if q := onDiskLink(dir, []string{p}); q != "" {
			return nil, "its record " + p + " conflicts and " + q + " is a symlink, which the resolution would write through", ""
		}
		var out []byte
		var nLeft, nRight int
		if keyed(p) {
			var err error
			if out, nLeft, nRight, err = l.resolveKeyed(ctx, dir, p, ":1:"+p, "HEAD", "MERGE_HEAD"); err != nil {
				return nil, "its keyed table " + p + " conflicts and " + err.Error(), ""
			}
		} else {
			var sides [3][]byte
			for i := range sides {
				sides[i] = l.blob(ctx, dir, ":"+strconv.Itoa(i+1)+":"+p)
			}
			out, nLeft, nRight = unionRecords(sides[0], sides[1], sides[2])
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), out, 0o644); err != nil {
			return nil, "", "the resolved record " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, "", "the resolved record " + p + " could not be staged: " + firstLine("", err)
		}
		lines = append(lines, recordLine(p, nLeft, nRight))
	}
	return lines, "", ""
}

// dedupeRecords repairs each record the merge from before to HEAD changed: an
// append-only one holding a row twice has the repeat taken out, a keyed one naming a
// config twice is merged by config again from the merge's three commits; the merge
// commit is amended when one was. lines is the land log's, changed the records
// rewritten, card why a keyed table could not be merged by config, env a git failure.
func (l *lander) dedupeRecords(ctx context.Context, dir, before string) (lines, changed []string, card, env string) {
	names, err := l.git(ctx, dir, "diff", "--name-only", "--no-renames", before, "HEAD")
	if err != nil {
		return nil, nil, "", "the files of the merge could not be listed: " + firstLine("", err)
	}
	for _, p := range strings.Split(names, "\n") {
		if !(appendOnly(p) || keyed(p)) || onDiskLink(dir, []string{p}) != "" {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(p))
		b, err := os.ReadFile(path)
		if err != nil {
			continue // the merge deleted it
		}
		var out []byte
		var n int
		if keyed(p) {
			if n = keyedTwice(b); n == 0 {
				continue
			}
			theirs, terr := l.git(ctx, dir, "rev-parse", "--verify", "-q", "HEAD^2")
			if terr != nil {
				return nil, nil, "", "the merge naming a config twice in " + p + " has no second parent to merge it from: " + firstLine("", terr)
			}
			base := ""
			if mb, err := l.git(ctx, dir, "merge-base", before, theirs); err == nil {
				base = mb + ":" + p
			}
			if out, _, _, err = l.resolveKeyed(ctx, dir, p, base, before, theirs); err != nil {
				l.conflictPaths, l.conflictKind = []string{p}, "ledger"
				return nil, nil, "its keyed table " + p + " names a config twice after the merge and " + err.Error(), ""
			}
		} else if out, n = dedupeRows(b); n == 0 {
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return nil, nil, "", "the record " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, nil, "", "the record " + p + " could not be staged: " + firstLine("", err)
		}
		lines, changed = append(lines, dedupeLine(p, n)), append(changed, p)
	}
	if len(changed) > 0 {
		if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "--no-edit"); err != nil {
			return nil, nil, "", "the merge could not be amended with its records' repeated rows taken out: " + firstLine("", err)
		}
	}
	return lines, changed, "", ""
}

// lockSplit is a lock file's comment (its leading comment and blank lines) and its body
// (the rest), as lines.
func lockSplit(b []byte) (comment, body []string) {
	lines, _ := unionLines(b)
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "#") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	return lines[:i], lines[i:]
}

// unionInserts is base with every line either side inserted, the tip's (ours) before
// the card's (theirs) where both insert at one place; a side that is not the base with
// lines inserted is an error.
func unionInserts(base, ours, theirs []string) ([]string, error) {
	every := func(string) bool { return true }
	oAt, ok := sideInserts(base, ours, every)
	if !ok {
		return nil, fmt.Errorf("the left side is not the base with lines added")
	}
	tAt, ok := sideInserts(base, theirs, every)
	if !ok {
		return nil, fmt.Errorf("the right side is not the base with lines added")
	}
	var out []string
	for i := 0; i <= len(base); i++ {
		out = append(out, oAt[i]...)
		if !slices.Equal(oAt[i], tAt[i]) {
			out = append(out, tAt[i]...)
		}
		if i < len(base) {
			out = append(out, base[i])
		}
	}
	return out, nil
}

// seedTablesLock is the tables lock at a conflict, before its update run: the union of
// both sides' comment lines (each side's change paragraphs kept) over the tip's body,
// which the run regenerates from the merged schema.
func seedTablesLock(base, ours, theirs []byte) ([]byte, error) {
	bc, _ := lockSplit(base)
	oc, ob := lockSplit(ours)
	tc, _ := lockSplit(theirs)
	comment, err := unionInserts(bc, oc, tc)
	if err != nil {
		return nil, err
	}
	return []byte(strings.Join(append(comment, ob...), "\n") + "\n"), nil
}
