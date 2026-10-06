package main

// landappend.go is what land does with the file classes a plain merge gets wrong
// (docs/STANDARD.md, the file classes; docs/SPEC-SPRINT.md section 7, append-only records,
// keyed run records and the tables lock). An append-only record (appendOnly: the TLA+ case table)
// only ever gains rows, in no order that matters: a merge keeps the rows of both sides
// (.gitattributes says merge=union, so git itself does not stop on it) and the lander
// removes a row that came twice after the merge. A side that removes a row is no append,
// and the merge stops as before. The TLA+ run table (tla/RUNS.tsv) is keyed by case config:
// a conflict in it is resolved by config. The tables lock is generated from the schema:
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
var appendOnlyRecords = []string{"tla/CASES.tsv"}

// tablesLockFile is the generated tables lock (internal/sprint/schema.go renders it).
const tablesLockFile = "internal/sprint/TABLES.lock"

// runsFile is the keyed TLA+ run records (keyed by case config).
const runsFile = "tla/RUNS.tsv"

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

// configKey returns the config column of a TSV row (the text before the first tab, or the whole line).
func configKey(line string) string {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		return line[:i]
	}
	return line
}

// resolveRunsByConfig resolves a conflict in tla/RUNS.tsv by config: each side's row for
// a case it changed, refused if both sides changed the same case.
func resolveRunsByConfig(base, ours, theirs []byte) (out []byte, nOurs, nTheirs int, err error) {
	bl, bTrail := unionLines(base)
	ol, oTrail := unionLines(ours)
	tl, tTrail := unionLines(theirs)

	var header string
	if len(bl) > 0 && strings.HasPrefix(bl[0], "config\t") {
		header = bl[0]
		bl = bl[1:]
	}
	if len(ol) > 0 && strings.HasPrefix(ol[0], "config\t") {
		header = ol[0]
		ol = ol[1:]
	}
	if len(tl) > 0 && strings.HasPrefix(tl[0], "config\t") {
		header = tl[0]
		tl = tl[1:]
	}

	baseMap := make(map[string]string, len(bl))
	var baseOrder []string
	for _, l := range bl {
		k := configKey(l)
		if k == "" {
			continue
		}
		if _, dup := baseMap[k]; !dup {
			baseOrder = append(baseOrder, k)
		}
		baseMap[k] = l
	}

	oursMap := make(map[string]string, len(ol))
	var oursAdded []string
	for _, l := range ol {
		k := configKey(l)
		if k == "" {
			continue
		}
		if _, dup := oursMap[k]; !dup {
			if _, inBase := baseMap[k]; !inBase {
				oursAdded = append(oursAdded, k)
			}
		}
		oursMap[k] = l
	}

	theirsMap := make(map[string]string, len(tl))
	var theirsAdded []string
	for _, l := range tl {
		k := configKey(l)
		if k == "" {
			continue
		}
		if _, dup := theirsMap[k]; !dup {
			if _, inBase := baseMap[k]; !inBase {
				theirsAdded = append(theirsAdded, k)
			}
		}
		theirsMap[k] = l
	}

	var resultRows []string
	for _, k := range baseOrder {
		baseRow := baseMap[k]
		oursRow, inOurs := oursMap[k]
		theirsRow, inTheirs := theirsMap[k]

		oursChanged := inOurs && oursRow != baseRow
		theirsChanged := inTheirs && theirsRow != baseRow

		switch {
		case oursChanged && theirsChanged:
			if oursRow == theirsRow {
				resultRows = append(resultRows, oursRow)
				nOurs++
			} else {
				return nil, 0, 0, fmt.Errorf("both sides changed the record for %s", k)
			}
		case !inOurs && theirsChanged:
			return nil, 0, 0, fmt.Errorf("the left side removed and the right side changed the record for %s", k)
		case !inTheirs && oursChanged:
			return nil, 0, 0, fmt.Errorf("the right side removed and the left side changed the record for %s", k)
		case !inOurs && !inTheirs:
			continue
		case !inOurs:
			if inTheirs {
				resultRows = append(resultRows, theirsRow)
			}
		case !inTheirs:
			if inOurs {
				resultRows = append(resultRows, oursRow)
			}
		case oursChanged:
			resultRows = append(resultRows, oursRow)
			nOurs++
		case theirsChanged:
			resultRows = append(resultRows, theirsRow)
			nTheirs++
		default:
			resultRows = append(resultRows, baseRow)
		}
	}

	seenAdded := make(map[string]bool)
	for _, k := range oursAdded {
		oursRow := oursMap[k]
		if theirsRow, inTheirs := theirsMap[k]; inTheirs {
			if oursRow != theirsRow {
				return nil, 0, 0, fmt.Errorf("both sides added different records for %s", k)
			}
		}
		resultRows = append(resultRows, oursRow)
		seenAdded[k] = true
		nOurs++
	}

	for _, k := range theirsAdded {
		if seenAdded[k] {
			continue
		}
		theirsRow := theirsMap[k]
		resultRows = append(resultRows, theirsRow)
		nTheirs++
	}

	var lines []string
	if header != "" {
		lines = append(lines, header)
	}
	lines = append(lines, resultRows...)
	s := strings.Join(lines, "\n")
	if oTrail || tTrail || bTrail {
		s += "\n"
	}
	return []byte(s), nOurs, nTheirs, nil
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

// resolveRecords resolves the append-only records, keyed run records and the tables lock among a merge's
// unmerged paths (each written and staged); rest is the paths left, lines the land log's
// lines, note the card's. card is why one is not resolved (the conflict stands), env a
// git failure that is not the card's.
func (l *lander) resolveRecords(ctx context.Context, dir string, paths []string) (rest, lines []string, note, card, env string) {
	var done []string
	for _, p := range paths {
		if !appendOnly(p) && p != tablesLockFile && p != runsFile {
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
		switch p {
		case tablesLockFile:
			out = regenTablesLock(sides[0], sides[1], sides[2], renderTablesLock())
			lines = append(lines, "generated "+p+": regenerated from the schema at the merge")
		case runsFile:
			var nOurs, nTheirs int
			var err error
			if out, nOurs, nTheirs, err = resolveRunsByConfig(sides[0], sides[1], sides[2]); err != nil {
				return nil, nil, "", "its keyed record " + p + " conflicts and cannot be resolved: " + err.Error(), ""
			}
			lines = append(lines, fmt.Sprintf("record %s: resolved by config (+%d tip, +%d card)", p, nOurs, nTheirs))
		default:
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
// before is the tip the merge was made on; c is the card being merged.
// If an append-only record is missing a base row or a side removed a row, or if tla/RUNS.tsv
// has a duplicate config, the merge is refused (card conflict returned).
func (l *lander) dedupeMerge(ctx context.Context, dir, before string, c landCard) (lines []string, card, env string) {
	names, err := l.git(ctx, dir, "diff", "--name-only", "-z", before, "HEAD")
	if err != nil {
		return nil, "", "the merge's changed files could not be listed: " + firstLine("", err)
	}
	mb, mberr := l.git(ctx, dir, "merge-base", before, c.head)
	mb = strings.TrimSpace(mb)
	if mberr != nil || mb == "" {
		mb, _ = l.git(ctx, dir, "merge-base", "HEAD^1", "HEAD^2")
		mb = strings.TrimSpace(mb)
	}
	var fixed []string
	for _, p := range strings.Split(names, "\x00") {
		if p == "" {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(p))
		if p == runsFile {
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			rlines, _ := unionLines(b)
			seen := map[string]bool{}
			for _, lstr := range rlines {
				if strings.HasPrefix(lstr, "config\t") || strings.TrimSpace(lstr) == "" {
					continue
				}
				k := configKey(lstr)
				if seen[k] {
					l.conflictKind = "file"
					l.conflictPaths = []string{p}
					return nil, "the head " + c.head + " of " + c.id + " does not merge: record " + p + " appears twice for " + k, ""
				}
				seen[k] = true
			}
		}
		if !appendOnly(p) {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if mb != "" && mb != before {
			baseContent, berr := l.git(ctx, dir, "show", mb+":"+p)
			if berr == nil {
				baseLines, _ := unionLines([]byte(baseContent))
				leftContent, lerr := l.git(ctx, dir, "show", before+":"+p)
				if lerr == nil {
					leftLines, _ := unionLines([]byte(leftContent))
					for _, row := range baseLines {
						if !slices.Contains(leftLines, row) {
							l.conflictKind = "file"
							l.conflictPaths = []string{p}
							return nil, "the head " + c.head + " of " + c.id + " does not merge: its append-only record " + p + " is not an append: the left side removes a row, which is no append: " + strconv.Quote(row), ""
						}
					}
				}
				rightContent, rerr := l.git(ctx, dir, "show", c.head+":"+p)
				if rerr == nil {
					rightLines, _ := unionLines([]byte(rightContent))
					for _, row := range baseLines {
						if !slices.Contains(rightLines, row) {
							l.conflictKind = "file"
							l.conflictPaths = []string{p}
							return nil, "the head " + c.head + " of " + c.id + " does not merge: its append-only record " + p + " is not an append: the right side removes a row, which is no append: " + strconv.Quote(row), ""
						}
					}
				}
				mergedLines, _ := unionLines(b)
				for _, row := range baseLines {
					if !slices.Contains(mergedLines, row) {
						l.conflictKind = "file"
						l.conflictPaths = []string{p}
						return nil, "the head " + c.head + " of " + c.id + " does not merge: its append-only record " + p + " is not an append: a base row is missing: " + strconv.Quote(row), ""
					}
				}
			}
		}
		out, n := dedupeRows(b)
		if n == 0 {
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return nil, "", "the deduplicated " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, "", "the deduplicated " + p + " could not be staged: " + firstLine("", err)
		}
		fixed = append(fixed, p)
		lines = append(lines, fmt.Sprintf("record %s: %d repeated rows dropped after the merge", p, n))
	}
	if len(fixed) > 0 {
		if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "--no-edit"); err != nil {
			return nil, "", "the merge could not be amended after its records were deduplicated: " + firstLine("", err)
		}
	}
	return lines, "", ""
}
