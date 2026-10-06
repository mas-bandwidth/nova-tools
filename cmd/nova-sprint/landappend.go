package main

// landappend.go is what land does with the two file classes a plain merge gets wrong
// (docs/STANDARD.md, the file classes; docs/SPEC-SPRINT.md section 7, append-only records
// and the tables lock). An append-only record (appendOnly: the TLA+ case and run tables)
// only ever gains rows, in no order that matters: a merge keeps the rows of both sides
// (.gitattributes says merge=union, so git itself does not stop on it) and the lander
// removes a row that came twice after the merge. A side that removes or changes a row is
// no append, and the merge stops as before. The tables lock is generated from the schema:
// a conflict in it takes the tip's comments, the card's added comment lines, and a body
// rendered from the schema as it is built, never a body picked by hand. The decisions are
// functions of their inputs, apart from the git that feeds them.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// appendOnlyRecords are the records that only gain rows; rows are a set.
var appendOnlyRecords = []string{"tla/CASES.tsv", "tla/RUNS.tsv"}

// tablesLockFile is the generated tables lock (internal/sprint/schema.go renders it).
const tablesLockFile = "internal/sprint/TABLES.lock"

func appendOnly(p string) bool { return slices.Contains(appendOnlyRecords, p) }

// dedupeRows is b with each repeated line dropped after its first, and how many were.
func dedupeRows(b []byte) ([]byte, int) {
	lines, trailing := unionLines(b)
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	s := strings.Join(out, "\n")
	if trailing && len(out) > 0 {
		s += "\n"
	}
	return []byte(s), len(lines) - len(out)
}

// unionAppended is an append-only record at a merge: the tip's rows, then the rows the
// card added that the tip lacks. Each side must hold every row of the base (a removed
// row is no append); the count of rows each side added is returned.
func unionAppended(base, ours, theirs []byte) (out []byte, nOurs, nTheirs int, err error) {
	bl, _ := unionLines(base)
	ol, oTrail := unionLines(ours)
	tl, tTrail := unionLines(theirs)
	for name, side := range map[string][]string{"left": ol, "right": tl} {
		for _, row := range bl {
			if !slices.Contains(side, row) {
				return nil, 0, 0, fmt.Errorf("the %s side removes a row, which is no append: %q", name, row)
			}
		}
	}
	has := map[string]bool{}
	for _, r := range bl {
		has[r] = true
	}
	for _, r := range ol {
		if !has[r] {
			nOurs++
		}
	}
	lines := slices.Clone(ol)
	seen := map[string]bool{}
	for _, r := range ol {
		seen[r] = true
	}
	for _, r := range tl {
		if !seen[r] {
			seen[r] = true
			lines = append(lines, r)
			if !has[r] {
				nTheirs++
			}
		}
	}
	s := strings.Join(lines, "\n")
	if oTrail || tTrail {
		s += "\n"
	}
	deduped, _ := dedupeRows([]byte(s))
	return deduped, nOurs, nTheirs, nil
}

// renderTablesLock is the lock's body as schema.go has it now (the same text
// internal/ci's TestSprintTablesAreLocked renders): one line per column, then the view
// order, default and --all.
func renderTablesLock() string {
	var b strings.Builder
	for _, t := range append((sprint.Names{}).Definitions(), sprint.FriendsDef()) {
		hidden := map[string]bool{}
		for _, h := range t.Hidden {
			hidden[h] = true
		}
		for _, c := range t.Columns {
			h := "shown"
			if hidden[c.Name] {
				h = "hidden"
			}
			fmt.Fprintf(&b, "%s.%s %s %s %s", t.Name, c.Name, c.Projection, c.Fold, h)
			if c.Label != "" {
				fmt.Fprintf(&b, " label=%s", c.Label)
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "view %s\n", strings.Join(sprint.ShownOrder, " "))
	fmt.Fprintf(&b, "view-all %s\n", strings.Join(sprint.AllOrder, " "))
	return b.String()
}

// regenTablesLock is the lock at a merge: the tip's comment lines, then the comment lines
// the card added (not in the base or the tip), then the body rendered from the schema.
func regenTablesLock(base, ours, theirs []byte, body string) []byte {
	comments := func(b []byte) []string {
		var c []string
		lines, _ := unionLines(b)
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "#") || strings.TrimSpace(l) == "" {
				c = append(c, l)
			}
		}
		return c
	}
	out := comments(ours)
	blanks := 0 // the blank lines that close the tip's comments, kept closing them
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out, blanks = out[:len(out)-1], blanks+1
	}
	known := map[string]bool{}
	for _, l := range append(comments(base), out...) {
		known[l] = true
	}
	var added []string
	for _, l := range comments(theirs) {
		if strings.TrimSpace(l) != "" && !known[l] {
			added = append(added, l)
		}
	}
	// the card's comment lines are its own change paragraphs: kept after the tip's, as one block
	if len(added) > 0 {
		out = append(out, "#")
		out = append(out, added...)
	}
	out = append(out, make([]string, blanks)...)
	return []byte(strings.Join(out, "\n") + "\n" + body)
}

// resolveRecords resolves the append-only records and the tables lock among a merge's
// unmerged paths (each written and staged); rest is the paths left, lines the land log's
// lines, note the card's. card is why one is not resolved (the conflict stands), env a
// git failure that is not the card's.
func (l *lander) resolveRecords(ctx context.Context, dir string, paths []string) (rest, lines []string, note, card, env string) {
	var done []string
	for _, p := range paths {
		if !appendOnly(p) && p != tablesLockFile {
			rest = append(rest, p)
			continue
		}
		if q := onDiskLink(dir, []string{p}); q != "" {
			return nil, nil, "", p + " conflicts and " + q + " is a symlink, which the resolution would write through", ""
		}
		var sides [3][]byte
		for i := range sides {
			res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "show", ":"+strconv.Itoa(i+1)+":"+p)
			if err != nil {
				return nil, nil, "", p + " conflicts and has no stage " + strconv.Itoa(i+1) + " to resolve from: " + firstLine("", err), ""
			}
			sides[i] = res.Stdout
		}
		var out []byte
		if p == tablesLockFile {
			out = regenTablesLock(sides[0], sides[1], sides[2], renderTablesLock())
			lines = append(lines, "generated "+p+": regenerated from the schema at the merge")
		} else {
			var nOurs, nTheirs int
			var err error
			if out, nOurs, nTheirs, err = unionAppended(sides[0], sides[1], sides[2]); err != nil {
				return nil, nil, "", "its append-only record " + p + " conflicts and is not an append: " + err.Error(), ""
			}
			lines = append(lines, fmt.Sprintf("record %s: resolved as the union of appended rows (+%d tip, +%d card)", p, nOurs, nTheirs))
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), out, 0o644); err != nil {
			return nil, nil, "", "", "the resolved " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, nil, "", "", "the resolved " + p + " could not be staged: " + firstLine("", err)
		}
		done = append(done, p)
	}
	if len(done) > 0 {
		note = "the records " + strings.Join(done, ", ") + " conflicted and were resolved at the merge (appended rows united, the tables lock regenerated)"
	}
	return rest, lines, note, "", ""
}

// dedupeMerge removes a row that came twice from each append-only record the merge at
// HEAD changed (git's merge=union keeps both sides' rows), amending the merge commit.
// before is the tip the merge was made on.
func (l *lander) dedupeMerge(ctx context.Context, dir, before string) (lines []string, env string) {
	names, err := l.git(ctx, dir, "diff", "--name-only", "-z", before, "HEAD")
	if err != nil {
		return nil, "the merge's changed files could not be listed: " + firstLine("", err)
	}
	var fixed []string
	for _, p := range strings.Split(names, "\x00") {
		if !appendOnly(p) {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(p))
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out, n := dedupeRows(b)
		if n == 0 {
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return nil, "the deduplicated " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, "the deduplicated " + p + " could not be staged: " + firstLine("", err)
		}
		fixed = append(fixed, p)
		lines = append(lines, fmt.Sprintf("record %s: %d repeated rows dropped after the merge", p, n))
	}
	if len(fixed) > 0 {
		if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "--no-edit"); err != nil {
			return nil, "the merge could not be amended after its records were deduplicated: " + firstLine("", err)
		}
	}
	return lines, ""
}
