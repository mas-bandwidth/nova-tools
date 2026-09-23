package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The WALL is how long the sprint still takes, not how much work is left in it (Glenn,
// 2026-09-22: "consider parallelism and show me the wall clock here, not total hours of
// work ... amdahl's law", and then "What we just did is optimize the set of steps so that
// the same amount of work got done, but wall clock was reduced. I would like you to ALWAYS
// do this").
//
// The model is one machine per consumer: a lane runs its tasks one at a time, and a task
// also waits for every task it TRULY depends on, wherever that one runs. The wall is the
// last finish. The sum of the work is printed beside it and is never the answer -- reporting
// the sum as if it were the time is the mistake this computation exists to stop.
//
// A lane is serial only where it has to be. Two tasks with the same owner and disjoint paths
// are PARALLEL (the unit of parallelism is the FILE, not the person); they are serial here
// only because one consumer can type one thing at a time, and that is precisely the false
// dependency Splittable names so it can be handed to another consumer with the owner kept
// as the required reader.

// Lane is one consumer's serial run of tasks, in the order it will do them.
type Lane struct {
	Consumer string
	Tasks    []Task
	// Minutes is the lane's own serial sum; Finish is when its last task ends, counting
	// the waits for other lanes' tasks. Finish >= Minutes, and the difference is idle.
	Minutes int
	Finish  int
}

// Wall is the answer: the bound, the work behind it, which lane sets it, and what could be
// moved off that lane at no cost in work.
type Wall struct {
	Minutes     int
	WorkMinutes int
	Critical    string
	Lanes       []Lane
	Splittable  []Task
	// Starts and Finishes are each open task's scheduled minute, for a caller that wants
	// to draw the lanes rather than print them.
	Starts   map[string]int
	Finishes map[string]int
}

// Line is the wall as the status line carries it: `~4h`.
func (w Wall) Line() string { return Minutes(w.Minutes) }

// ComputeWall lays the open tasks out on their lanes under their true dependency edges and
// returns the Amdahl bound. A dependency cycle is a refusal, not a guess: a cycle means two
// tasks each wait for the other and no honest number exists.
func ComputeWall(tasks []Task) (Wall, error) {
	open := map[string]Task{}
	var ids []string
	for _, t := range tasks {
		if !t.Open() {
			continue
		}
		open[t.ID] = t
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)

	w := Wall{Starts: map[string]int{}, Finishes: map[string]int{}}
	laneFree := map[string]int{}
	laneTasks := map[string][]Task{}
	scheduled := map[string]bool{}

	for len(scheduled) < len(open) {
		var ready []Task
		for _, id := range ids {
			t := open[id]
			if scheduled[id] {
				continue
			}
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
			return Wall{}, fmt.Errorf("these tasks depend on each other in a cycle and cannot be laid out: %s", strings.Join(stuck, ", "))
		}
		// Of the ready tasks, take the one that can start earliest; ties go to the older
		// task, then to the id, so the layout is the same on every machine.
		sort.SliceStable(ready, func(i, j int) bool {
			si, sj := startOf(ready[i], open, laneFree, w.Finishes), startOf(ready[j], open, laneFree, w.Finishes)
			if si != sj {
				return si < sj
			}
			if !ready[i].CreatedAt.Equal(ready[j].CreatedAt) {
				return ready[i].CreatedAt.Before(ready[j].CreatedAt)
			}
			return ready[i].ID < ready[j].ID
		})
		t := ready[0]
		lane := LaneOf(t)
		start := startOf(t, open, laneFree, w.Finishes)
		est := t.EstMinutes
		if est < 0 {
			est = 0
		}
		finish := start + est
		w.Starts[t.ID] = start
		w.Finishes[t.ID] = finish
		laneFree[lane] = finish
		laneTasks[lane] = append(laneTasks[lane], t)
		scheduled[t.ID] = true
		w.WorkMinutes += est
		if finish > w.Minutes {
			w.Minutes = finish
			w.Critical = lane
		}
	}

	var names []string
	for lane := range laneTasks {
		names = append(names, lane)
	}
	sort.Strings(names)
	for _, lane := range names {
		l := Lane{Consumer: lane, Tasks: laneTasks[lane], Finish: laneFree[lane]}
		for _, t := range l.Tasks {
			l.Minutes += t.EstMinutes
		}
		w.Lanes = append(w.Lanes, l)
	}
	sort.SliceStable(w.Lanes, func(i, j int) bool {
		if w.Lanes[i].Finish != w.Lanes[j].Finish {
			return w.Lanes[i].Finish > w.Lanes[j].Finish
		}
		return w.Lanes[i].Consumer < w.Lanes[j].Consumer
	})
	w.Splittable = splittable(laneTasks[w.Critical], open)
	return w, nil
}

func depsDone(t Task, open map[string]Task, scheduled map[string]bool) bool {
	for _, d := range t.DependsOn {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := open[d]; !ok {
			continue // already closed, or outside this sprint: not a wait
		}
		if !scheduled[d] {
			return false
		}
	}
	return true
}

func startOf(t Task, open map[string]Task, laneFree map[string]int, finishes map[string]int) int {
	start := laneFree[LaneOf(t)]
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

// LaneOf is the consumer a task runs on: its owner when it has one, else the consumer its
// route named, else the unowned lane -- which is a fact worth seeing on the table, because
// an open task with no owner is nobody's.
func LaneOf(t Task) string {
	if o := strings.ToLower(strings.TrimSpace(t.Owner)); o != "" {
		return o
	}
	if r := strings.TrimSpace(t.Route); r != "" {
		if i := strings.IndexByte(r, ':'); i >= 0 && i+1 < len(r) {
			return strings.ToLower(r[i+1:])
		}
		return strings.ToLower(r)
	}
	return "unowned"
}

// splittable names the tasks on a lane that are only serial because one consumer holds
// them: no true dependency inside the lane, and no file in common with anything else on it.
// A lane with one task has nothing to split -- there is no false dependency in a queue of
// one.
func splittable(lane []Task, open map[string]Task) []Task {
	if len(lane) < 2 {
		return nil
	}
	var out []Task
	for i, t := range lane {
		if i == 0 {
			// The head of the lane is what the consumer is doing now; moving it does not
			// shorten the lane, it just moves the same minutes somewhere else.
			continue
		}
		if dependsInside(t, lane) {
			continue
		}
		shared := false
		for _, other := range lane {
			if other.ID == t.ID {
				continue
			}
			if SharesPaths(t, other) {
				shared = true
				break
			}
		}
		if shared {
			continue
		}
		out = append(out, t)
	}
	return out
}

func dependsInside(t Task, lane []Task) bool {
	for _, d := range t.DependsOn {
		for _, other := range lane {
			if other.ID == strings.TrimSpace(d) {
				return true
			}
		}
	}
	return false
}

// AfterMoving is the wall the sprint would have if these task ids moved to a consumer with
// an empty lane each: the number the coordinator puts in front of a friend when asking to
// hand a tail over ("yes -> wall ~2 h, no -> ~4 h"). It changes nothing in the store.
func AfterMoving(tasks []Task, ids []string, to string) (Wall, error) {
	moved := map[string]bool{}
	for _, id := range ids {
		moved[strings.TrimSpace(id)] = true
	}
	out := make([]Task, 0, len(tasks))
	n := 0
	for _, t := range tasks {
		if moved[t.ID] {
			n++
			t.Reader = t.Owner
			if to != "" {
				t.Owner = to
			} else {
				t.Owner = fmt.Sprintf("%s-split-%d", LaneOf(t), n)
			}
		}
		out = append(out, t)
	}
	return ComputeWall(out)
}

// Age is how long a task has been open, for the scoring the dealer does.
func Age(t Task, now time.Time) time.Duration {
	if t.CreatedAt.IsZero() {
		return 0
	}
	return now.Sub(t.CreatedAt)
}
