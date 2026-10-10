package main

// ledgerunion.go is what land does with a merge that stops on a shrink-only ledger
// (docs/SPEC-SPRINT.md section 7, the shrink-only ledgers; shrinkonly.ShrinkOnly says
// which files those are). Two cards that each remove a row of the same ledger conflict
// when the rows are adjacent, and the one right answer is the base with both sides'
// removals taken out: unionRemovals is that three-way resolution as a pure function
// of the three sides' bytes, and unionLedgers is the lander's use of it in the clone,
// reading the stages git holds for the conflicted path and staging the result. A side
// that adds a line or raises a count is refused (the ledger only shrinks: a change that
// is not a removal is
// not resolved here), and the merge stops as before.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/ci/shrinkonly"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// countedLedgers are the shrink-only ledgers whose rows carry counts lowered in place.
var countedLedgers = []string{shrinkonly.DeadCode}

// isCountedLedger reports whether p is a declared counted ledger whose rows carry counts.
func isCountedLedger(p string) bool {
	for _, q := range countedLedgers {
		if p == q || filepath.Base(p) == filepath.Base(q) {
			return true
		}
	}
	return false
}

// ceilingPrefix opens a list's `# ceiling: N` line (internal/ci/allowlist): the one line
// a removal moves, down, so two sides that each lower it are both honoured by the lower.
const ceilingPrefix = "# ceiling:"

// unionLines splits a file into lines without their newlines; a file of nothing is no
// lines, and trailing says whether the last line ended with one.
func unionLines(b []byte) (lines []string, trailing bool) {
	s := string(b)
	if s == "" {
		return nil, true
	}
	trailing = strings.HasSuffix(s, "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), trailing
}

// ceilingOf is the index and value of the first `# ceiling: N` line of lines, -1 when
// none; an unreadable value is an error.
func ceilingOf(lines []string) (at, n int, err error) {
	for i, l := range lines {
		if strings.HasPrefix(l, ceilingPrefix) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, ceilingPrefix)))
			if err != nil {
				return -1, 0, fmt.Errorf("%q is not `%s <rows>`", l, ceilingPrefix)
			}
			return i, n, nil
		}
	}
	return -1, 0, nil
}

// countAt is the span and value of a line's count, its second field when that is a
// positive integer (a counted row, `key N ...`); ok false for a plain row.
func countAt(line string) (start, end, n int, ok bool) {
	rest := strings.TrimLeft(line, " \t")
	key := strings.IndexAny(rest, " \t")
	if key < 0 {
		return 0, 0, 0, false
	}
	start = len(line) - len(rest) + key
	start += len(line[start:]) - len(strings.TrimLeft(line[start:], " \t"))
	end = start + len(line[start:])
	if i := strings.IndexAny(line[start:], " \t"); i >= 0 {
		end = start + i
	}
	n, err := strconv.Atoi(line[start:end])
	return start, end, n, err == nil && n > 0 && start < end
}

// rowCount is what a base line holds: its count for a counted row (on a counted ledger),
// 1 for a plain row (held whole or gone).
func rowCount(line string, counted bool) int {
	if counted {
		if _, _, n, ok := countAt(line); ok {
			return n
		}
	}
	return 1
}

// withCount is a counted base line at the count n.
func withCount(line string, n int) string {
	start, end, _, _ := countAt(line)
	return line[:start] + strconv.Itoa(n) + line[end:]
}

// sameRow says s is the base line b, as it is (n its count) or with its count changed
// (n the new one, on a counted ledger): the same key and the same other bytes.
func sameRow(b, s string, counted bool) (n int, ok bool) {
	if s == b {
		return rowCount(b, counted), true
	}
	if !counted {
		return 0, false
	}
	bs, be, _, bok := countAt(b)
	ss, se, sn, sok := countAt(s)
	if bok && sok && b[:bs] == s[:ss] && b[be:] == s[se:] {
		return sn, true
	}
	return 0, false
}

// sideHolds is what one side holds of each base line: the base's count (unchanged), a
// lower count (a counted row lowered), or 0 (the row gone). The side's lines are matched
// to base's in order, each to the next base line it is or lowers (base's rows are unique
// keys, so this is exact for them, and an identical line taken for another gives the
// same text); a side that is a subsequence of base, counts lowered or not, matches
// whole. A line the base does not hold (a new row, a changed row, a reordering) and a
// raised count are errors, the side's name as the caller says it.
func sideHolds(name string, base, side []string, counted bool) (held []int, err error) {
	held = make([]int, len(base))
	at := 0
	for _, s := range side {
		j, n := at, 0
		for ok := false; j < len(base) && !ok; {
			if n, ok = sameRow(base[j], s, counted); !ok {
				j++
			}
		}
		if j == len(base) {
			return nil, fmt.Errorf("the %s side adds a line, which is no removal: %q", name, s)
		}
		if n > rowCount(base[j], counted) {
			return nil, fmt.Errorf("the %s side raises the count of %q, which is no removal: %q", name, base[j], s)
		}
		held[j], at = n, j+1
	}
	return held, nil
}

// unionRemovals resolves a conflict in a shrink-only ledger: the base (the merge base's
// side) less what either side took. A plain row is held or gone; a counted row (`key N
// ...`), on a ledger that declares counts (counted), holds its count less what each side
// lowered it by (each side's removals are its own, so a row both lowered is lowered by
// both: left + right - base), and goes at zero.
// The `# ceiling: N` line, when the base has one and a side lowered it, is at the lower
// of the two sides' values (each side's is at least its own row count, so the lower is
// at least the union's). left and right are the two sides (the tip's and the card's);
// nLeft and nRight are the base rows each removed or lowered, the ceiling line not
// counted. It refuses, with why, a side that adds a line the base does not hold (a
// ledger that only shrinks has no such change), a side that raises a count or the
// ceiling, and a result that is not the base's lines less some; the caller then stops as
// any conflict does.
func unionRemovals(base, left, right []byte, counted ...bool) (out []byte, nLeft, nRight int, err error) {
	isCounted := len(counted) > 0 && counted[0]
	b, trailing := unionLines(base)
	bAt, bCeil, err := ceilingOf(b)
	if err != nil {
		return nil, 0, 0, errors.New("the base's ceiling line: " + err.Error())
	}
	ceil := bCeil
	value := make([]int, len(b)) // what the union holds of each base line
	for i, l := range b {
		value[i] = rowCount(l, isCounted)
	}
	counts := [2]int{}
	for k, s := range [][]byte{left, right} {
		name := [2]string{"left", "right"}[k]
		lines, _ := unionLines(s)
		if bAt >= 0 {
			// a lowered ceiling is a removal's shadow, not an addition: read it, then
			// match the line as the base's
			at, n, err := ceilingOf(lines)
			switch {
			case err != nil:
				return nil, 0, 0, errors.New("the " + name + " side's ceiling line: " + err.Error())
			case at >= 0 && n > bCeil:
				return nil, 0, 0, fmt.Errorf("the %s side raises the ceiling from %d to %d", name, bCeil, n)
			case at >= 0:
				lines[at] = b[bAt]
				ceil = min(ceil, n)
			}
		}
		held, err := sideHolds(name, b, lines, isCounted)
		if err != nil {
			return nil, 0, 0, err
		}
		for i, l := range b {
			if took := rowCount(l, isCounted) - held[i]; took > 0 {
				value[i] -= took
				if i != bAt {
					counts[k]++
				}
			}
		}
	}
	var kept []string
	for i, l := range b {
		switch {
		case value[i] <= 0:
		case i == bAt && ceil != bCeil:
			kept = append(kept, ceilingPrefix+" "+strconv.Itoa(ceil))
		case isCounted && value[i] < rowCount(l, isCounted):
			kept = append(kept, withCount(l, value[i]))
		default:
			kept = append(kept, l)
		}
	}
	if !subsetOfBase(b, kept, bAt, isCounted) {
		return nil, 0, 0, errors.New("the resolution is not the base's lines less some")
	}
	if len(kept) == 0 {
		return []byte{}, counts[0], counts[1], nil
	}
	text := strings.Join(kept, "\n")
	if trailing {
		text += "\n"
	}
	return []byte(text), counts[0], counts[1], nil
}

// subsetOfBase is the check on a resolution, apart from how it was made: out is base
// with lines taken out, counts lowered and nothing else, the ceiling line (base's bAt,
// when any) allowed a new value.
func subsetOfBase(base, out []string, bAt int, counted bool) bool {
	if len(out) > len(base) {
		return false
	}
	at := 0
	for _, o := range out {
		i := at
		for i < len(base) {
			if n, ok := sameRow(base[i], o, counted); (ok && n <= rowCount(base[i], counted)) || (i == bAt && strings.HasPrefix(o, ceilingPrefix)) {
				break
			}
			i++
		}
		if i == len(base) {
			return false
		}
		at = i + 1
	}
	return true
}

// unionPaths splits a merge's unmerged paths into the shrink-only ledgers resolved as a
// union of removals and the rest (the generated ledgers a family regenerates, and
// anything else): a path a family owns is the family's.
func unionPaths(paths []string, ledgers []landLedger) (union, rest []string) {
	for _, p := range paths {
		if shrinkonly.ShrinkOnly(p) && !owned(p, ledgers) {
			union = append(union, p)
		} else {
			rest = append(rest, p)
		}
	}
	return union, rest
}

// unionLine is the land log's line for one resolved ledger.
func unionLine(p string, nLeft, nRight int) string {
	return fmt.Sprintf("ledger %s: resolved as the union of removals (-%d left, -%d right)", p, nLeft, nRight)
}

// unionNote is the card's note for a merge resolved in its shrink-only ledgers.
func unionNote(paths []string) string {
	return "the shrink-only ledgers " + strings.Join(paths, ", ") + " conflicted and were resolved at the merge as the union of both sides' removals"
}

// unionMessage is the merge commit of a merge resolved only in its shrink-only ledgers.
func unionMessage(id, stream string, lines []string) []string {
	return []string{"land " + id + " (sprint stream " + stream + ")",
		"The shrink-only ledgers conflicted and were resolved as the union of both sides' removals: " + strings.Join(lines, "; ") + "."}
}

// unionLedgers resolves the shrink-only ledgers of a merge in progress in dir (paths,
// each unmerged): each is read at its three stages, resolved (unionRemovals), written
// and staged. lines is the land log's line per ledger; card is why one could not be
// resolved (the card's conflict stands as before), env a git failure that is not the
// card's. The merge is left in progress either way: the caller commits it or aborts it.
func (l *lander) unionLedgers(ctx context.Context, dir string, paths []string) (lines []string, card, env string) {
	for _, p := range paths {
		if q := onDiskLink(dir, []string{p}); q != "" {
			return nil, "its shrink-only ledger " + p + " conflicts and " + q + " is a symlink, which the resolution would write through", ""
		}
		// the stages as git holds them, bytes untrimmed: 1 the merge base, 2 the tip
		// (ours), 3 the card (theirs)
		var sides [3][]byte
		for i := range sides {
			res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "show", ":"+strconv.Itoa(i+1)+":"+p)
			if err != nil {
				return nil, "its shrink-only ledger " + p + " conflicts and has no stage " + strconv.Itoa(i+1) + " to resolve from: " + firstLine("", err), ""
			}
			sides[i] = res.Stdout
		}
		out, nLeft, nRight, err := unionRemovals(sides[0], sides[1], sides[2], isCountedLedger(p))
		if err != nil {
			return nil, "its shrink-only ledger " + p + " conflicts and is not a union of removals: " + err.Error(), ""
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), out, 0o644); err != nil {
			return nil, "", "the resolved ledger " + p + " could not be written: " + err.Error()
		}
		if _, err := l.git(ctx, dir, "add", "--", p); err != nil {
			return nil, "", "the resolved ledger " + p + " could not be staged: " + firstLine("", err)
		}
		lines = append(lines, unionLine(p, nLeft, nRight))
	}
	return lines, "", ""
}
