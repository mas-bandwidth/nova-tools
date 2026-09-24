package sprintline

import (
	"fmt"
	"sort"
	"strings"
)

// Task is one still-open sprint task, the fields the wall needs. State is
// open, working, or closed; closed is not on the wall. EstMinutes is the
// stored estimate, replaced by the kind's calibrated actual when there is one.
// Route is the producer's route= (friend:<name> or bench:<name>). It names
// the lane only when Owner is empty, which is a hand-over: the owner cleared,
// the typing moved.
type Task struct {
	ID         string
	Kind       string
	Owner      string
	Route      string
	State      string
	EstMinutes int
	DependsOn  []string
	Paths      []string
}

// WallMinutes is the sprint verb's wall: one lane per owner, a task waiting
// on its dependencies, the answer the last finish. Same-owner tasks are
// serial. The sum of the work is not the answer, and neither is a hand formula.
//
// suggest is kind -> minutes from calibration. A kind present in the map,
// including zero, replaces the stored estimate. A kind absent keeps it.
func WallMinutes(tasks []Task, suggest map[string]int) (int, error) {
	open := map[string]Task{}
	var ids []string
	for _, t := range tasks {
		if t.State == "closed" {
			continue
		}
		if t.ID == "" {
			return 0, fmt.Errorf("an open task has no id")
		}
		if _, dup := open[t.ID]; dup {
			return 0, fmt.Errorf("task %s is on the wall twice", t.ID)
		}
		open[t.ID] = t
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)

	laneFree := map[string]int{}
	finishes := map[string]int{}
	scheduled := map[string]bool{}
	wall := 0

	for len(scheduled) < len(open) {
		var ready []Task
		for _, id := range ids {
			if scheduled[id] {
				continue
			}
			t := open[id]
			if depsDone(t, open, scheduled) {
				ready = append(ready, t)
			}
		}
		if len(ready) == 0 {
			var stuck []string
			for _, id := range ids {
				if !scheduled[id] {
					stuck = append(stuck, id)
				}
			}
			return 0, fmt.Errorf("these tasks depend on each other in a cycle and cannot be laid out: %s", strings.Join(stuck, ", "))
		}
		sort.SliceStable(ready, func(i, j int) bool {
			si, sj := startOf(ready[i], open, laneFree, finishes), startOf(ready[j], open, laneFree, finishes)
			if si != sj {
				return si < sj
			}
			return ready[i].ID < ready[j].ID
		})
		t := ready[0]
		lane := laneOf(t)
		start := startOf(t, open, laneFree, finishes)
		est := estimate(t, suggest)
		finish := start + est
		finishes[t.ID] = finish
		laneFree[lane] = finish
		scheduled[t.ID] = true
		if finish > wall {
			wall = finish
		}
	}
	return wall, nil
}

func estimate(t Task, suggest map[string]int) int {
	if suggest != nil {
		if m, ok := suggest[t.Kind]; ok {
			if m < 0 {
				return 0
			}
			return m
		}
	}
	if t.EstMinutes < 0 {
		return 0
	}
	return t.EstMinutes
}

func laneOf(t Task) string {
	if o := strings.ToLower(strings.TrimSpace(t.Owner)); o != "" {
		return o
	}
	r := strings.TrimSpace(t.Route)
	if r == "" || r == "-" {
		return "unowned"
	}
	if i := strings.IndexByte(r, ':'); i >= 0 && i+1 < len(r) {
		return strings.ToLower(r[i+1:])
	}
	return strings.ToLower(r)
}

func depsDone(t Task, open map[string]Task, scheduled map[string]bool) bool {
	for _, d := range t.DependsOn {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := open[d]; !ok {
			continue
		}
		if !scheduled[d] {
			return false
		}
	}
	return true
}

func startOf(t Task, open map[string]Task, laneFree, finishes map[string]int) int {
	start := laneFree[laneOf(t)]
	for _, d := range t.DependsOn {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := open[d]; !ok {
			continue
		}
		if f, ok := finishes[d]; ok && f > start {
			start = f
		}
	}
	return start
}
