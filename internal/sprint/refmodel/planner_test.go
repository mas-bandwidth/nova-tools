package refmodel_test

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// minSamplesWithMoves is the fewest of the snapshots a duty must make a move on
// for a test over the snapshots to count as having tried it: a duty the walks
// never make anything of is a duty the test never compared.
const minSamplesWithMoves = 10

// todaysDecision is what today's tick decides on the sample, from today's code
// and not from the reference: each part of the tick planned as the store's tick
// plans it (store.TickPartStep is the step the tick runs), held to what the
// store applies, and converted to moves; the unknown machines told of and the
// reminders due, from the functions the store's tick calls for them. The
// snapshot's own clock is set to the time it is decided at, as the store's read
// is. It is the moves of each duty, by duty; a STOPPED machine has none.
func todaysDecision(s sample) map[string][]refmodel.Move {
	c := s.snap.Clone()
	t := c.Tables
	t.Now = s.now
	out := map[string][]refmodel.Move{}
	if !c.Running {
		return out
	}
	stopped := func(from, to time.Time) time.Duration { return sprint.StoppedBetween(c.Stopped, from, to) }
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: stopped, Beats: c.Beats}
	held := func(duty string, p sprint.Plan, due int) []refmodel.Move {
		ms := refmodel.PlanMoves(duty, t, sprint.Applied(t, p))
		if due > 0 {
			ms = append(ms, refmodel.Move{Duty: duty, Kind: refmodel.KindDue, Attrs: []string{"due=" + strconv.Itoa(due)}})
		}
		return ms
	}
	if len(c.Untold) > 0 {
		out[refmodel.DutyStrangers] = held(refmodel.DutyStrangers, sprint.StrangerNotes(t, c.Untold), 0)
	}
	for _, part := range sprint.TickParts {
		due := 0
		out[part.Name] = held(part.Name, store.TickPartStep(part.Name, part.Fn, req, nil, nil, &due).Plan(t), due)
	}
	var remind []refmodel.Move
	for _, p := range c.Goals.People {
		if p.Due(s.now, c.Since, stopped) {
			remind = append(remind, refmodel.Move{Duty: refmodel.DutyRemind, Kind: refmodel.KindPush, Card: p.Name, To: p.Route,
				Words: fmt.Sprintf("REMINDER %d to %s over %s", p.Count+1, p.Name, p.Route)})
		}
	}
	if _, stale := c.Goals.NotesStale(); stale {
		remind = append(remind, held(refmodel.DutyRemind, sprint.RemindNotes(t, c.Goals, sprint.MachineActor), 0)...)
	}
	out[refmodel.DutyRemind] = remind
	return out
}

func TestDecideIsWhatTodaysTickPlansOnAThousandSnapshots(t *testing.T) {
	t.Parallel()
	withMoves := map[string]int{}
	kinds := map[string]int{}
	for i, s := range snapshots() {
		want := todaysDecision(s)
		var all []refmodel.Move
		for _, d := range refmodel.Duties {
			got := d.Moves(s.snap, s.now)
			if ok, diff := refmodel.Equal(want[d.Name], got); !ok {
				t.Fatalf("snapshot %d, duty %s: %s\ntoday's tick:%s\nthe reference:%s", i, d.Name, diff, show(want[d.Name]), show(got))
			}
			if len(got) > 0 {
				withMoves[d.Name]++
			}
			for _, m := range got {
				kinds[m.Kind]++
			}
			all = append(all, want[d.Name]...)
		}
		if i < deepSamples {
			if ok, diff := refmodel.Equal(all, refmodel.Decide(s.snap, s.now)); !ok {
				t.Fatalf("snapshot %d: Decide is not the moves of today's tick: %s", i, diff)
			}
		}
	}
	for _, d := range refmodel.Duties {
		if withMoves[d.Name] < minSamplesWithMoves {
			t.Errorf("the duty %s made moves on %d of %d snapshots, fewer than %d: the walks do not try it", d.Name, withMoves[d.Name], len(snapshots()), minSamplesWithMoves)
		}
	}
	for _, kind := range []string{refmodel.KindCreate, refmodel.KindMove, refmodel.KindSet, refmodel.KindOpen, refmodel.KindNotice, refmodel.KindClose,
		refmodel.KindHold, refmodel.KindUpdate, refmodel.KindPush} {
		if kinds[kind] == 0 {
			t.Errorf("no snapshot made a move of kind %s: the walks do not try it", kind)
		}
	}
	t.Logf("snapshots with moves, by duty: %v; moves by kind: %v", withMoves, kinds)
}

// applyMoves carries out the moves that change cards, as the plan they came
// from would, on the tables.
func applyMoves(s *sprint.Snapshot, ms []refmodel.Move) error {
	bump := regexp.MustCompile(`^(.+?)([+-][0-9]+)$`)
	for _, m := range ms {
		tb := s.T(m.Table)
		switch m.Kind {
		case refmodel.KindRow:
			if !tb.HasRow(m.Card) {
				tb.Rows = append(tb.Rows, m.Card)
			}
		case refmodel.KindCreate:
			row, col, _ := strings.Cut(m.To, ":")
			score, _ := strconv.ParseFloat(m.Score, 64)
			c := &sprint.Card{ID: m.Card, Row: row, Col: col, Score: score, Fields: map[string]string{}}
			if err := setFields(c, m); err != nil {
				return err
			}
			tb.Put(c)
		case refmodel.KindMove, refmodel.KindSet, refmodel.KindRemove, refmodel.KindBump:
			c := tb.Card(m.Card)
			if c == nil {
				return fmt.Errorf("%s: %s: no such card", m.Table, m.Card)
			}
			if m.Kind == refmodel.KindMove {
				c.Row, c.Col, _ = strings.Cut(m.To, ":")
			}
			if m.Kind == refmodel.KindRemove {
				c.Row, c.Col = "", ""
			}
			if m.Score != "" {
				c.Score, _ = strconv.ParseFloat(m.Score, 64)
			}
			if m.Kind == refmodel.KindBump {
				f := bump.FindStringSubmatch(m.Set[0])
				delta, _ := strconv.Atoi(f[2])
				c.Fields[f[1]] = strconv.Itoa(c.Int(f[1]) + delta)
			} else if err := setFields(c, m); err != nil {
				return err
			}
			tb.Put(c)
		}
	}
	return nil
}

func setFields(c *sprint.Card, m refmodel.Move) error {
	for _, kv := range m.Set {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("%s: a field %q with no value", m.Card, kv)
		}
		c.Fields[k] = v
	}
	for _, k := range m.Unset {
		delete(c.Fields, k)
	}
	return nil
}

// cards is every card of the tables as text: its table, id, place, score and
// fields, and each table's rows, and nothing else, so two states with the same
// cards are the same here whatever their revisions.
func cards(s *sprint.Snapshot) string {
	var lines []string
	for _, tb := range []*sprint.Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		lines = append(lines, fmt.Sprintf("%s rows %q", tb.Name, tb.Rows))
		for id, c := range tb.Cards {
			lines = append(lines, fmt.Sprintf("%s %s %s:%s %v %v", tb.Name, id, c.Row, c.Col, c.Score, c.Fields))
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// The moves are the plan and not a summary of it: carrying out a part's moves
// on the tables leaves the cards as carrying out its plan does.
func TestTheMovesOfAPlanReproduceItsChangesToTheCards(t *testing.T) {
	t.Parallel()
	tried, changed := 0, 0
	for i, s := range snapshots()[:deepSamples] {
		c := s.snap.Clone()
		t0 := c.Tables
		t0.Now = s.now
		req := sprint.TickReq{Who: sprint.MachineActor, Beats: c.Beats,
			Stopped: func(from, to time.Time) time.Duration { return sprint.StoppedBetween(c.Stopped, from, to) }}
		for _, part := range sprint.TickParts {
			plan, _ := part.Fn(t0, req)
			plan = sprint.Applied(t0, plan)
			byPlan := &world{s: refmodel.Snapshot{Tables: t0}.Clone().Tables}
			if err := byPlan.apply(plan); err != nil {
				continue // a plan the store would refuse whole (a card changed twice): nothing to reproduce
			}
			byMoves := refmodel.Snapshot{Tables: t0}.Clone().Tables
			if err := applyMoves(byMoves, refmodel.PlanMoves(part.Name, t0, plan)); err != nil {
				t.Fatalf("snapshot %d, part %s: %v", i, part.Name, err)
			}
			tried++
			if cards(byPlan.s) != cards(t0) {
				changed++
			}
			if a, b := cards(byPlan.s), cards(byMoves); a != b {
				t.Fatalf("snapshot %d, part %s: the moves do not make the plan's changes:\n%s", i, part.Name, firstDifference(a, b))
			}
		}
	}
	if changed < minSamplesWithMoves {
		t.Errorf("only %d of %d plans changed a card: the walks do not try the moves", changed, tried)
	}
}
