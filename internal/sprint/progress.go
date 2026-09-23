package sprint

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Progress is the four fields on sprint:<name> that make the x/y/eta line a
// store read (#2684). done, units and percent are the live acceptance
// evaluation — SET OK units=, SET DONE done= and percent= — not the count of
// closed tasks. percent is the figure the tool printed, not one recomputed
// here. eta_minutes is the wall. A reader that recomputes them has already
// lost the race the hash exists to end.
type Progress struct {
	Done       int
	Units      int
	Percent    int
	ETAMinutes int
	// Evaluated is true once SET OK / SET DONE have been recorded. Until then
	// the three counts stay zero: a closed task is not an accepted unit.
	Evaluated bool
}

// Acceptance is one pass of nova-work set check over the work set. SET OK
// supplies units. SET DONE supplies done and percent.
type Acceptance struct {
	Set     bool
	Done    int
	Units   int
	Percent int
}

// ProgressView is what a progress write measured: the tasks, for the wall, and
// the acceptance counts last recorded on the sprint.
type ProgressView struct {
	Tasks      []Task
	Acceptance Acceptance
}

// Measure is those four fields. eta_minutes is #2593's wall, with each open
// task's estimate replaced by its kind's lease-to-done mean when calibration
// has one. A kind with no actual yet keeps the estimate it was given: a
// missing measurement is not a zero. done, units and percent come only from
// the acceptance evaluation. With none recorded they are zero, even when
// every task in the set is closed, and the percent is not derived from done
// and units.
func Measure(tasks []Task, acc Acceptance) (Progress, error) {
	eta, err := etaMinutes(tasks)
	if err != nil {
		return Progress{}, err
	}
	if !acc.Set {
		return Progress{ETAMinutes: eta}, nil
	}
	return Progress{
		Done:       acc.Done,
		Units:      acc.Units,
		Percent:    acc.Percent,
		ETAMinutes: eta,
		Evaluated:  true,
	}, nil
}

func etaMinutes(tasks []Task) (int, error) {
	cal := Calibrate(tasks)
	adjusted := make([]Task, len(tasks))
	copy(adjusted, tasks)
	for i := range adjusted {
		t := &adjusted[i]
		if !t.Open() {
			continue
		}
		if m, ok := cal.Suggest[t.Kind]; ok && m > 0 {
			t.EstMinutes = m
		}
	}
	w, err := ComputeWall(adjusted)
	if err != nil {
		return 0, err
	}
	return w.Minutes, nil
}

// ParseAcceptance reads nova-work set check stdout. Anything else in it,
// including a :status receipt or a copy of the work set, is not a count.
func ParseAcceptance(stdout string) (Acceptance, error) {
	var acc Acceptance
	var sawUnits, sawDone, sawPercent bool
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "SET OK" || strings.HasPrefix(line, "SET OK "):
			n, ok, err := fieldInt(line, "units")
			if err != nil {
				return Acceptance{}, err
			}
			if !ok {
				return Acceptance{}, fmt.Errorf("SET OK wants units=; refusing to guess y")
			}
			if err := oneField(sawUnits, acc.Units, n, "units"); err != nil {
				return Acceptance{}, err
			}
			acc.Units, sawUnits = n, true
		case line == "SET DONE" || strings.HasPrefix(line, "SET DONE "):
			done, ok, err := fieldInt(line, "done")
			if err != nil {
				return Acceptance{}, err
			}
			if !ok {
				return Acceptance{}, fmt.Errorf("SET DONE wants done=; refusing to count :status")
			}
			pct, pok, err := fieldInt(line, "percent")
			if err != nil {
				return Acceptance{}, err
			}
			if !pok {
				return Acceptance{}, fmt.Errorf("SET DONE wants percent=; the percent is the tool's, refusing to recompute")
			}
			if err := oneField(sawDone, acc.Done, done, "done"); err != nil {
				return Acceptance{}, err
			}
			if err := oneField(sawPercent, acc.Percent, pct, "percent"); err != nil {
				return Acceptance{}, err
			}
			acc.Done, sawDone = done, true
			acc.Percent, sawPercent = pct, true
		}
	}
	if !sawUnits {
		return Acceptance{}, fmt.Errorf("set check did not print SET OK units=; refusing to guess y")
	}
	if !sawDone || !sawPercent {
		return Acceptance{}, fmt.Errorf("set check did not print SET DONE; refusing to count :status")
	}
	acc.Set = true
	return acc, nil
}

func oneField(saw bool, prev, next int, name string) error {
	if saw && prev != next {
		return fmt.Errorf("%s is given twice and they disagree (%d and %d)", name, prev, next)
	}
	return nil
}

func fieldInt(line, key string) (int, bool, error) {
	for _, tok := range strings.Fields(line) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k != key {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, true, fmt.Errorf("%s=%s is not a non-negative integer", key, v)
		}
		return n, true, nil
	}
	return 0, false, nil
}

// RecordAcceptance stores one set-check evaluation on the sprint and rewrites
// the four fields from it. A later task-state write keeps these counts and
// only moves eta.
func RecordAcceptance(ctx context.Context, st Store, name, stdout string) error {
	acc, err := ParseAcceptance(stdout)
	if err != nil {
		return err
	}
	return publishProgress(ctx, st, name, func(v ProgressView) (Progress, error) {
		v.Acceptance = acc
		return Measure(v.Tasks, v.Acceptance)
	})
}

// WriteProgress rewrites done, units, percent and eta_minutes on sprint:<name>.
// The three counts stay the last acceptance evaluation; eta is the wall over
// the tasks as they are now. The write lands only if that view is still
// current, so a slower writer cannot move the hash backwards.
func WriteProgress(ctx context.Context, st Store, name string) error {
	return publishProgress(ctx, st, name, func(v ProgressView) (Progress, error) {
		return Measure(v.Tasks, v.Acceptance)
	})
}

// progressAttempts is how many times a lost race is remeasured before the
// write is refused. Refusing is the safe end: the older snapshot is never
// what gets published in order to finish the call.
const progressAttempts = 8

func publishProgress(ctx context.Context, st Store, name string, measure func(ProgressView) (Progress, error)) error {
	for i := 0; i < progressAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := st.PublishProgress(ctx, name, measure)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("sprint %s changed while its progress was being written; refusing to publish an older snapshot", name)
}

// WriteProgressContaining rewrites the four fields on every sprint whose set
// holds taskID. A state change names a task; the sprint it belongs to is the
// set that holds it, which may not be the flag the verb was given.
func WriteProgressContaining(ctx context.Context, st Store, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	all, err := st.Sprints(ctx)
	if err != nil {
		return err
	}
	for _, s := range all {
		tasks, err := st.Tasks(ctx, s.Name)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.ID != taskID {
				continue
			}
			if err := WriteProgress(ctx, st, s.Name); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func progressFields(p Progress) map[string]interface{} {
	ev := "0"
	if p.Evaluated {
		ev = "1"
	}
	return map[string]interface{}{
		"done":        strconv.Itoa(p.Done),
		"units":       strconv.Itoa(p.Units),
		"percent":     strconv.Itoa(p.Percent),
		"eta_minutes": strconv.Itoa(p.ETAMinutes),
		"evaluated":   ev,
	}
}

func acceptanceFrom(m map[string]string) Acceptance {
	if m["evaluated"] != "1" {
		return Acceptance{}
	}
	return Acceptance{Set: true, Done: atoi(m["done"]), Units: atoi(m["units"]), Percent: atoi(m["percent"])}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
