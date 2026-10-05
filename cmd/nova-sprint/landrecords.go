package main

// landrecords.go is what land does after a card's merge to the append-only records
// (docs/SPEC-SPRINT.md section 7, the append-only records). A record is a `.tsv` file the
// tree's .gitattributes marks merge=union: a header line, then one row per key (the first
// field), rows appended and never reordered by hand (tla/CASES.tsv, tla/RUNS.tsv). Git's
// union driver keeps both sides' lines where two cards append at one place, so the merge
// never stops on them; what it can leave is a row twice (both sides appended it) or a key
// twice (a side changed a row the other side's hunk also held). After the merge land drops
// each repeated row, the first kept, and amends the merge; a key held by two different rows
// is no append, and the card is refused as a conflict, the batch branch reset.
//
// The decisions (which paths are records, the resolved bytes, the words) are functions of
// their inputs, apart from the git that feeds them.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// recordPaths is the paths git check-attr -z merge says are merged by union and are
// records (a `.tsv` file), from its output.
func recordPaths(out string) []string {
	var paths []string
	f := strings.Split(out, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		if f[1+i] == "merge" && f[2+i] == "union" && strings.HasSuffix(f[i], ".tsv") && !slices.Contains(paths, f[i]) {
			paths = append(paths, f[i])
		}
	}
	return paths
}

// uniqueRows is a record with each repeated row after the first dropped, the header and
// the order kept, and how many were dropped. key is a key (the first field) two different
// rows hold, "" for none: the record is no union of appends.
func uniqueRows(b []byte) (out []byte, dropped int, key string) {
	lines, trailing := unionLines(b)
	seen := map[string]bool{}
	rowOf := map[string]string{}
	var kept []string
	for i, l := range lines {
		if i > 0 && l != "" && seen[l] {
			dropped++
			continue
		}
		seen[l] = true
		kept = append(kept, l)
		if i == 0 || l == "" {
			continue
		}
		k, _, _ := strings.Cut(l, "\t")
		if r, ok := rowOf[k]; ok && r != l && key == "" {
			key = k
		}
		rowOf[k] = l
	}
	s := strings.Join(kept, "\n")
	if trailing {
		s += "\n"
	}
	return []byte(s), dropped, key
}

// recordLine is the land log's line for a record whose repeated rows were dropped.
func recordLine(p string, dropped int) string {
	return fmt.Sprintf("record %s: merged by union, %d repeated rows dropped", p, dropped)
}

// recordNote is the card's note for a merge whose records were made unique.
func recordNote(paths []string) string {
	return "the append-only records " + strings.Join(paths, ", ") + " were merged by union and their repeated rows dropped"
}

// uniqueRecords makes the records the merge of c changed (before its first parent) unique
// and amends the merge; note is the card's note when it did, card why a record holds a key
// twice (the batch branch reset to before), env a git failure that is not the card's.
func (l *lander) uniqueRecords(ctx context.Context, dir string, c landCard, before string) (note, card, env string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return "", "", ""
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only", "--no-renames", "-z", before, after)
	if err != nil {
		return "", "", "the files the merge of " + c.id + " changed could not be listed: " + firstLine("", err)
	}
	files := slices.DeleteFunc(strings.Split(changed, "\x00"), func(p string) bool { return !strings.HasSuffix(p, ".tsv") })
	if len(files) == 0 {
		return "", "", ""
	}
	attrs, err := l.git(ctx, dir, append([]string{"check-attr", "-z", "merge", "--"}, files...)...)
	if err != nil {
		return "", "", "the merge attributes of " + c.id + "'s records could not be read: " + firstLine("", err)
	}
	var paths, lines []string
	for _, p := range recordPaths(attrs) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		b, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			continue // the merge deleted it
		}
		if err != nil {
			return "", "", "the record " + p + " could not be read: " + err.Error()
		}
		out, dropped, key := uniqueRows(b)
		if key != "" {
			l.conflictKind, l.conflictPaths = "file", []string{p}
			if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
				return "", "", "the batch branch could not be reset after " + c.id + "'s record " + p + " held a key twice: " + firstLine("", err)
			}
			return "", "the head " + c.head + " of " + c.id + " does not merge: its append-only record " + p + " merged by union holds the key " + key + " in two different rows, which is no append", ""
		}
		if dropped == 0 {
			continue
		}
		if q := onDiskLink(dir, []string{p}); q != "" {
			return "", "", "the record " + p + " could not be made unique: " + q + " is a symlink"
		}
		if err := os.WriteFile(full, out, 0o644); err != nil {
			return "", "", "the record " + p + " could not be written: " + err.Error()
		}
		paths, lines = append(paths, p), append(lines, recordLine(p, dropped))
	}
	if len(paths) == 0 {
		return "", "", ""
	}
	msg, err := l.git(ctx, dir, "log", "-1", "--format=%B")
	if err != nil {
		return "", "", "the merge message of " + c.id + " could not be read: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", "", "the unique records could not be staged: " + firstLine("", err)
	}
	body := "The append-only records were merged by union and their repeated rows dropped: " + strings.Join(lines, "; ") + "."
	if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "-m", msg, "-m", body); err != nil {
		return "", "", "the merge of " + c.id + " could not be amended with its unique records: " + firstLine("", err)
	}
	l.ledgerLog = append(l.ledgerLog, lines...)
	return recordNote(paths), "", ""
}
