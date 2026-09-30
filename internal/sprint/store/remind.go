package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The reminder duty of the tick (docs/SPEC-SPRINT.md, "Reminders"): the
// sprint's people and their goals are one record in the store,
// kept by the goal verbs and carried across a clear; the tick reads it, and
// while the machine is RUNNING pushes each person's goal down its route when
// it is due, once, and writes one judgment for a route that fails.

// keyGoals is the people's record (JSON): the goals, the routes, the pushes.
const keyGoals = "goals"

// maxFailText bounds the error text a failing route keeps.
const maxFailText = 300

// Goals reads the people's record; none is empty.
func (st *Store) Goals(ctx context.Context) (sprint.Goals, error) {
	var g sprint.Goals
	err := st.getJSON(ctx, keyGoals, &g)
	return g, err
}

// updateGoals reads the record fresh, applies fn and writes it back in name
// order, so a change made while a tick was delivering is not lost.
func (st *Store) updateGoals(ctx context.Context, fn func(*sprint.Goals) error) error {
	g, err := st.Goals(ctx)
	if err != nil {
		return err
	}
	if err := fn(&g); err != nil {
		return err
	}
	g.Sort()
	return st.putJSON(ctx, keyGoals, g)
}

// SetGoal sets a person's goal: text nil keeps the text, route "" keeps the
// route (a new person needs both). A person set is pushed to by the next tick
// that finds the machine RUNNING; a set clears the failure of their route so
// the next attempt is judged afresh. It returns the person as
// stored and whether they are new.
func (st *Store) SetGoal(ctx context.Context, name string, text *string, route string) (sprint.Goal, bool, error) {
	if err := sprint.ValidGoalName(name); err != nil {
		return sprint.Goal{}, false, err
	}
	if text != nil {
		if err := sprint.ValidGoalText(*text, MaxCardTextBytes); err != nil {
			return sprint.Goal{}, false, err
		}
	}
	if route != "" {
		if _, err := NewDeliverer(route); err != nil {
			return sprint.Goal{}, false, err
		}
	}
	var out sprint.Goal
	var created bool
	err := st.updateGoals(ctx, func(g *sprint.Goals) error {
		i := g.Find(name)
		if i < 0 {
			if text == nil || route == "" {
				return fmt.Errorf("%s is new: give the text (--file <path>) and the route (--to file:<path>)", name)
			}
			g.People = append(g.People, sprint.Goal{Name: name})
			i, created = len(g.People)-1, true
		}
		p := &g.People[i]
		if text != nil {
			p.Text = *text
		}
		if route != "" {
			p.Route = route
		}
		p.Tried, p.Fail, p.Pending = time.Time{}, "", true
		out = *p
		return nil
	})
	return out, created, err
}

// DropGoal removes a person; false when there was none. Their open judgment
// is closed by the next tick of a RUNNING machine.
func (st *Store) DropGoal(ctx context.Context, name string) (bool, error) {
	found := false
	err := st.updateGoals(ctx, func(g *sprint.Goals) error {
		i := g.Find(name)
		if i < 0 {
			return nil
		}
		found = true
		g.People = append(g.People[:i], g.People[i+1:]...)
		return nil
	})
	return found, err
}

// ResetGoalPushes keeps the people and their goals and forgets the pushes:
// what a clear does, so the first tick after it pushes to everyone.
func (st *Store) ResetGoalPushes(ctx context.Context) error {
	return st.updateGoals(ctx, func(g *sprint.Goals) error {
		for i := range g.People {
			p := &g.People[i]
			p.Last, p.Tried, p.Count, p.Fail, p.Pending = time.Time{}, time.Time{}, 0, "", false
		}
		g.Noted = nil
		return nil
	})
}

// Reminder is one push: which one it is, to whom, when, the sprint it is
// for, and the goal text.
type Reminder struct {
	N     int
	To    string
	At    time.Time
	Epoch uint64
	Text  string
}

// Header is the one line that precedes the text.
func (r Reminder) Header() string {
	return fmt.Sprintf("REMINDER %d to %s at %s, epoch %d", r.N, r.To, r.At.Format(time.RFC3339), r.Epoch)
}

// Deliverer delivers one reminder down one route.
type Deliverer interface {
	Deliver(Reminder) error
}

// NewDeliverer is the deliverer of a route. The file route is built; the bus
// route is refused: sending on a bus in process needs a sender, a remote and
// a branch, and network git work in the tick, which this route does not name.
func NewDeliverer(route string) (Deliverer, error) {
	kind, target, err := sprint.ParseRoute(route)
	if err != nil {
		return nil, err
	}
	switch kind {
	case sprint.RouteFile:
		if !filepath.IsAbs(target) {
			return nil, fmt.Errorf("the file route %q is not an absolute path", route)
		}
		return FileRoute{Path: filepath.Clean(target)}, nil
	}
	return nil, fmt.Errorf("the bus route is not built yet (%q): use file:<path> and let a watcher of the file wake the person", route)
}

// FileRoute delivers by replacing one file.
type FileRoute struct{ Path string }

// Deliver writes the header line and the text to a temporary file beside the
// path and renames it over the path: whatever watches the file sees one
// current reminder, whole, and the file never grows.
func (f FileRoute) Deliver(r Reminder) (err error) {
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(f.Path)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	text := r.Text
	if text == "" || text[len(text)-1] != '\n' {
		text += "\n"
	}
	if _, err = tmp.WriteString(r.Header() + "\n" + text); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}

// workEpoch is the epoch of the work table: the sprint's, changed by a clear.
func (st *Store) workEpoch(ctx context.Context) (uint64, error) {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil || len(shapes) == 0 {
		return 0, err
	}
	return shapes[0].Epoch, nil
}

// remind is the tick's reminder duty, run for a RUNNING machine after the
// parts: each person due gets their goal delivered once and the push
// recorded; a route that fails is recorded and judged once, and the
// judgment closes when a later delivery arrives. A second tick right after
// finds nobody due and changes nothing. A delivery that arrived and whose
// record could not be written is delivered again by the next tick.
func (st *Store) remind(ctx context.Context, m Machine, res *TickResult) error {
	g, err := st.Goals(ctx)
	if err != nil {
		return err
	}
	if len(g.People) == 0 && len(g.Noted) == 0 {
		return nil
	}
	now := st.now()
	type outcome struct {
		goal sprint.Goal
		n    int
		err  error
	}
	var did []outcome
	var epoch uint64
	for _, p := range g.DueAt(now, m.Since, m.StoppedBetween) {
		if len(did) == 0 {
			if epoch, err = st.workEpoch(ctx); err != nil {
				return err
			}
		}
		r := Reminder{N: p.Count + 1, To: p.Name, At: now, Epoch: epoch, Text: p.Text}
		d, derr := NewDeliverer(p.Route)
		if derr == nil {
			derr = d.Deliver(r)
		}
		did = append(did, outcome{p, r.N, derr})
	}
	part := PartResult{Name: "remind", Result: Result{Verb: "tick remind"}}
	if len(did) > 0 {
		err = st.updateGoals(ctx, func(g *sprint.Goals) error {
			for _, o := range did {
				i := g.Find(o.goal.Name)
				if i < 0 || g.People[i].Route != o.goal.Route || g.People[i].Text != o.goal.Text {
					continue // dropped or set again while the tick delivered: the next tick pushes it
				}
				p := &g.People[i]
				if o.err == nil {
					p.Last, p.Count, p.Tried, p.Fail, p.Pending = now, o.n, time.Time{}, "", false
					part.Moved = append(part.Moved, fmt.Sprintf("REMINDER %d to %s over %s", o.n, p.Name, p.Route))
					continue
				}
				p.Tried, p.Pending = now, false
				if p.Fail == "" {
					p.Fail = oneline.Escape(o.err.Error())
					if len(p.Fail) > maxFailText {
						p.Fail = p.Fail[:maxFailText]
					}
				}
				part.Refused = append(part.Refused, sprint.Refusal{Key: o.goal.Name, Why: "the reminder over " + p.Route + " failed: " + o.err.Error()})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if g, err = st.Goals(ctx); err != nil {
		return err
	}
	if want, stale := g.NotesStale(); stale {
		r, err := st.Run(ctx, Step{Verb: "tick remind", Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.RemindNotes(s, g, sprint.MachineActor)
		}})
		if err != nil {
			return err
		}
		part.Notes = r.Notes
		err = st.updateGoals(ctx, func(g *sprint.Goals) error {
			g.Noted = want
			if len(want) == 0 {
				g.Noted = nil
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(part.Moved) > 0 || len(part.Refused) > 0 || part.Notes > 0 {
		res.Parts = append(res.Parts, part)
	}
	return nil
}
