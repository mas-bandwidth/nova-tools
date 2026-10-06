package main

// landappend.go is what land does with the append-only records and the tables lock at a
// merge (docs/SPEC-SPRINT.md section 7, the merge classes of a file). An append-only
// record (tla/CASES.tsv, tla/RUNS.tsv, the deleted-tests log) only ever gains rows, and
// their order means nothing: .gitattributes marks it merge=union, so git keeps both
// sides' rows, and a conflict git still stops on (a tree without the attribute) is
// resolved here the same way, unionRecords over the three stages. After every merge a
// record the merge changed is read once more and a row it holds twice is taken out
// (dedupeRows, the sort -u of the merge without the reordering: the header stays the
// first line), the merge commit amended. The tables lock is a generated file whose
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

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// appendOnlyRecords are the append-only records: rows only appended, order irrelevant.
var appendOnlyRecords = []string{"tla/CASES.tsv", "tla/RUNS.tsv", "internal/ci/testdata/deleted-tests.txt"}

// appendOnly says p is an append-only record.
func appendOnly(p string) bool {
	return slices.Contains(appendOnlyRecords, p)
}

// recordPaths splits a merge's unmerged paths into the append-only records and the rest.
func recordPaths(paths []string) (records, rest []string) {
	for _, p := range paths {
		if appendOnly(p) {
			records = append(records, p)
		} else {
			rest = append(rest, p)
		}
	}
	return records, rest
}

// unionRecords is an append-only record at a merge: the tip's (ours) rows in order, then
// each of the card's (theirs) rows the tip lacks, every row once (dedupeRows). nLeft is
// the rows the tip has beyond the base, nRight the rows the card added to the union. A
// row either side rewrote is kept in both forms, as git's union merge keeps it.
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

// recordLine is the land log's line for one record resolved at a conflict.
func recordLine(p string, nLeft, nRight int) string {
	return fmt.Sprintf("record %s: resolved as the union of both sides' rows (+%d left, +%d right)", p, nLeft, nRight)
}

// recordNote is the card's note for a merge resolved in its append-only records.
func recordNote(paths []string) string {
	return "the append-only records " + strings.Join(paths, ", ") + " conflicted and were resolved at the merge as the union of both sides' rows"
}

// recordSentence is the merge commit's sentence for the resolved records.
func recordSentence(lines []string) string {
	return "The append-only records conflicted and were resolved as the union of both sides' rows: " + strings.Join(lines, "; ") + "."
}

// dedupeLine is the land log's line for a record whose repeated rows were taken out.
func dedupeLine(p string, n int) string {
	return fmt.Sprintf("record %s: %d repeated row(s) taken out after the merge", p, n)
}

// dedupeNote is the card's note for that.
func dedupeNote(paths []string) string {
	return "the append-only records " + strings.Join(paths, ", ") + " held a repeated row after the merge, taken out"
}

// mergeRecords resolves the append-only records of a merge in progress in dir (paths,
// each unmerged): each read at its three stages (a missing stage is an empty side),
// resolved (unionRecords), written and staged. lines is the land log's line per record;
// card is why one could not be resolved, env a failure that is not the card's.
func (l *lander) mergeRecords(ctx context.Context, dir string, paths []string) (lines []string, card, env string) {
	for _, p := range paths {
		if q := onDiskLink(dir, []string{p}); q != "" {
			return nil, "its append-only record " + p + " conflicts and " + q + " is a symlink, which the resolution would write through", ""
		}
		var sides [3][]byte
		for i := range sides {
			res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "show", ":"+strconv.Itoa(i+1)+":"+p)
			if err == nil {
				sides[i] = res.Stdout
			}
		}
		out, nLeft, nRight := unionRecords(sides[0], sides[1], sides[2])
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

// dedupeRecords takes the repeated rows out of each append-only record the merge from
// before to HEAD changed and amends the merge commit when one was; lines is the land
// log's, changed the records rewritten, env a git failure.
func (l *lander) dedupeRecords(ctx context.Context, dir, before string) (lines, changed []string, env string) {
	names, err := l.git(ctx, dir, "diff", "--name-only", "--no-renames", before, "HEAD")
	if err != nil {
		return nil, nil, "the files of the merge could not be listed: " + firstLine("", err)
	}
	for _, p := range strings.Split(names, "\n") {
		if !appendOnly(p) || onDiskLink(dir, []string{p}) != "" {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(p))
		b, err := os.ReadFile(path)
		if err != nil {
			continue // the merge deleted it
		}
		out, n := dedupeRows(b)
		if n == 0 {
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return nil, nil, "the record " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, nil, "the record " + p + " could not be staged: " + firstLine("", err)
		}
		lines, changed = append(lines, dedupeLine(p, n)), append(changed, p)
	}
	if len(changed) > 0 {
		if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "--no-edit"); err != nil {
			return nil, nil, "the merge could not be amended with its records' repeated rows taken out: " + firstLine("", err)
		}
	}
	return lines, changed, ""
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
