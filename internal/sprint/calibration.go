package sprint

import (
	"fmt"
	"sort"
	"strings"
)

// CALIBRATION is the other half of "estimate each task ahead of doing it" (Glenn,
// 2026-09-22: "and then look at how much time it took afterwards and over time, your
// estimation skills will become better"). An estimate with no actual recorded beside it is
// the TELL the ruling names, so this prints the error distribution by kind and by owner and
// the defaults are tuned from it -- measured, never guessed.
//
// The actual is LeasedAt to DoneAt: the time the task was somebody's, not the time it was in
// the sprint. A closed task with no lease has no actual and is counted as such rather than
// as a zero, because a zero would quietly pull every average down.

// Row is one calibration line: a group, how many closed tasks it has with an actual, the
// estimated and actual totals, and the error between them.
type Row struct {
	Group   string
	N       int
	Est     int
	Actual  int
	Missing int // closed tasks in this group with no actual recorded
}

// ErrorPct is how far the estimate was from the measurement, positive when the work took
// longer than estimated. It is 0 when there is nothing to compare.
func (r Row) ErrorPct() int {
	if r.N == 0 || r.Est == 0 {
		return 0
	}
	return int((float64(r.Actual-r.Est)/float64(r.Est))*100 + sign(float64(r.Actual-r.Est))*0.5)
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

// Line is one row as the verb prints it.
func (r Row) Line(label string) string {
	if r.N == 0 {
		return fmt.Sprintf("%s %s n=0 no actual recorded (%d closed without a lease)", label, r.Group, r.Missing)
	}
	return fmt.Sprintf("%s %s n=%d est=%s actual=%s error=%+d%%", label, r.Group, r.N, Minutes(r.Est), Minutes(r.Actual), r.ErrorPct())
}

// Calibration is the error distribution by kind and by owner over the closed tasks.
type Calibration struct {
	ByKind  []Row
	ByOwner []Row
	// Suggest is the estimate each kind's measurement argues for, beside today's default.
	Suggest map[string]int
}

// Calibrate reads the closed tasks and returns the distribution. It changes nothing: the
// defaults move when a person moves them, with the numbers on the line.
func Calibrate(tasks []Task) Calibration {
	kinds := map[string]*Row{}
	owners := map[string]*Row{}
	for _, t := range tasks {
		if t.State != StateClosed {
			continue
		}
		k := group(kinds, t.Kind)
		o := group(owners, LaneOf(t))
		if t.Actual <= 0 {
			k.Missing++
			o.Missing++
			continue
		}
		for _, r := range []*Row{k, o} {
			r.N++
			r.Est += t.EstMinutes
			r.Actual += t.Actual
		}
	}
	c := Calibration{Suggest: map[string]int{}}
	c.ByKind = rows(kinds)
	c.ByOwner = rows(owners)
	for _, r := range c.ByKind {
		if r.N > 0 {
			c.Suggest[r.Group] = r.Actual / r.N
		}
	}
	return c
}

// String is the whole report, kinds then owners, one row per line.
func (c Calibration) String() string {
	var b strings.Builder
	for _, r := range c.ByKind {
		fmt.Fprintln(&b, r.Line("KIND"))
	}
	for _, r := range c.ByOwner {
		fmt.Fprintln(&b, r.Line("OWNER"))
	}
	var kinds []string
	for k := range c.Suggest {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "SUGGEST %s %dm (default %dm)\n", k, c.Suggest[k], DefaultEstimate[k])
	}
	return b.String()
}

func group(m map[string]*Row, name string) *Row {
	if name == "" {
		name = "-"
	}
	if r, ok := m[name]; ok {
		return r
	}
	r := &Row{Group: name}
	m[name] = r
	return r
}

func rows(m map[string]*Row) []Row {
	var out []Row
	for _, r := range m {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}
