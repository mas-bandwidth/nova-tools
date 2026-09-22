package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// THE LINE. Glenn, 2026-09-22, three refinements in twenty minutes, ending here: one active
// sprint prints `x/y z% -> ~Nh` and nothing else -- no sprint name, no C/O/W, no item list.
// More than one active sprint prints a table with the sprint name as the leftmost column.
// The swarm table's header carries the same line. Everything else is behind --verbose.
//
// The line is the whole interface most of the time, so it is built here once and printed by
// every caller, rather than formatted at each call site into four slightly different lines
// the way my hand-written status reports were all week.

// Counts is COWS: Closed, Open, Working, and the total they make.
type Counts struct {
	Closed  int
	Open    int
	Working int
	Total   int
}

// Count is the four numbers over a task set.
func Count(tasks []Task) Counts {
	var c Counts
	for _, t := range tasks {
		switch t.State {
		case StateClosed:
			c.Closed++
		case StateWorking:
			c.Working++
		default:
			c.Open++
		}
		c.Total++
	}
	return c
}

// Line is `14/23 61% -> ~4h`: the fraction, the percent, then the WALL (never the sum). A
// set whose remaining tasks carry no estimate prints the fraction alone rather than a made
// up hour -- an estimate nobody made is not an estimate of zero.
func Line(tasks []Task) (string, error) {
	c := Count(tasks)
	head := fmt.Sprintf("%d/%d %d%%", c.Closed, c.Total, Percent(c.Closed, c.Total))
	w, err := ComputeWall(tasks)
	if err != nil {
		return "", err
	}
	if w.Minutes <= 0 {
		return head, nil
	}
	return head + " -> " + w.Line(), nil
}

// LineWithLeft appends `Nh left` when the sprint carries a planned close: the estimate and
// the clock are two different facts and the line shows both.
func LineWithLeft(s Sprint, tasks []Task, now time.Time) (string, error) {
	line, err := Line(tasks)
	if err != nil {
		return "", err
	}
	if s.PlannedCloseAt.IsZero() {
		return line, nil
	}
	left := int(s.PlannedCloseAt.Sub(now).Minutes())
	if left < 0 {
		return line + " (planned close passed)", nil
	}
	return line + " " + strings.TrimPrefix(Minutes(left), "~") + " left", nil
}

// TableRow is one sprint's row when more than one is active.
type TableRow struct {
	Name string
	Line string
}

// Table is the several-sprints form: the name leftmost, then the same line. The names are
// padded so the numbers line up, because a column of numbers that does not line up is read
// wrong by the person it is for.
func Table(rows []TableRow) string {
	width := 0
	for _, r := range rows {
		if len(r.Name) > width {
			width = len(r.Name)
		}
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s  %s\n", width, r.Name, r.Line)
	}
	return b.String()
}

// Verbose is everything behind the line: the COWS counts, the open and working items by
// owner and route, the critical lane with its owner, and the splittable tasks on it. It is
// what the coordinator reads before deciding; the line is what everyone else reads.
func Verbose(tasks []Task) (string, error) {
	c := Count(tasks)
	w, err := ComputeWall(tasks)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "C=%d O=%d W=%d\n", c.Closed, c.Open, c.Working)
	fmt.Fprintf(&b, "wall %s work %s critical %s\n", Minutes(w.Minutes), Minutes(w.WorkMinutes), lane(w.Critical))
	for _, l := range w.Lanes {
		fmt.Fprintf(&b, "lane %s %s serial %s finish %s\n", l.Consumer, plural(len(l.Tasks), "task"), Minutes(l.Minutes), Minutes(l.Finish))
	}
	open := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Open() {
			open = append(open, t)
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		if LaneOf(open[i]) != LaneOf(open[j]) {
			return LaneOf(open[i]) < LaneOf(open[j])
		}
		return open[i].ID < open[j].ID
	})
	for _, t := range open {
		fmt.Fprintf(&b, "%s %s %s owner=%s route=%s est=%s\n",
			strings.ToUpper(t.State[:1])+t.State[1:], t.ID, dashed(t.Ref), dashed(t.Owner), dashed(t.Route), Minutes(t.EstMinutes))
	}
	if len(w.Splittable) == 0 {
		fmt.Fprintf(&b, "splittable none on %s\n", lane(w.Critical))
	}
	for _, t := range w.Splittable {
		fmt.Fprintf(&b, "splittable %s %s paths=%s (no file in common with the rest of %s's lane)\n",
			t.ID, dashed(t.Ref), strings.Join(t.Paths, ","), lane(w.Critical))
	}
	return b.String(), nil
}

func lane(name string) string {
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	return name
}

func dashed(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
