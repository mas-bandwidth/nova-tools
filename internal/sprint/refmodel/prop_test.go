package refmodel_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// propSamples is how many snapshots the property tests read, and deepSamples
// how many the ones that do more than decide once read: the tests of a
// clone's hold on its own state and of the planners run one by one.
const (
	propSamples = 1000
	deepSamples = 250
)

// snapshots are the snapshots of the walks, drawn once and shared by the
// tests, which only read them.
var snapshots = sync.OnceValue(func() []sample { return samples(propSamples) })

// dump is a snapshot as text that holds all of it: the same snapshot gives the
// same text, and a change to any part of it, in any table, card, field, note,
// beat, goal or span, changes the text.
func dump(s refmodel.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "running=%v since=%s untold=%q\n", s.Running, s.Since.Format(time.RFC3339Nano), s.Untold)
	for _, sp := range s.Stopped {
		fmt.Fprintf(&b, "stopped %s %s\n", sp.From.Format(time.RFC3339Nano), sp.To.Format(time.RFC3339Nano))
	}
	if s.Beats == nil {
		b.WriteString("beats nil\n")
	}
	for _, m := range slices.Sorted(maps.Keys(s.Beats)) {
		j, _ := json.Marshal(s.Beats[m])
		fmt.Fprintf(&b, "beat %s %s\n", m, j)
	}
	j, _ := json.Marshal(s.Goals)
	fmt.Fprintf(&b, "goals %s\n", j)
	t := s.Tables
	fmt.Fprintf(&b, "tables now=%s epoch=%d cleared=%s coordinator=%q actor=%q\n", t.Now.Format(time.RFC3339Nano), t.Epoch, t.Cleared.Format(time.RFC3339Nano), t.Coordinator, t.Actor)
	for _, tb := range []*sprint.Table{t.Work, t.Readers, t.Merge, t.Fleet} {
		fmt.Fprintf(&b, "table %s epoch=%d revision=%d rows=%q\n", tb.Name, tb.Epoch, tb.Revision, tb.Rows())
		for _, row := range slices.Sorted(maps.Keys(tb.Texts)) {
			fmt.Fprintf(&b, "  text %s %v\n", row, tb.Texts[row])
		}
		for _, id := range slices.Sorted(maps.Keys(tb.Cards)) {
			c := tb.Cards[id]
			fmt.Fprintf(&b, "  card %s %s:%s score=%v rev=%d %v\n", c.ID, c.Row, c.Col, c.Score, c.Rev, c.Fields)
		}
	}
	for _, set := range []struct {
		name string
		os   []sprint.Open
	}{{"open", t.Open}, {"acked", t.Acked}} {
		fmt.Fprintf(&b, "%s nil=%v\n", set.name, set.os == nil)
		for _, o := range set.os {
			j, _ := json.Marshal(o.Note)
			fmt.Fprintf(&b, "  %s %s\n", o.Key, j)
		}
	}
	return b.String()
}

func TestDumpSeesEveryPartOfASnapshot(t *testing.T) {
	t.Parallel()
	base := snapshots()[len(snapshots())-1].snap
	for name, change := range map[string]func(*refmodel.Snapshot){
		"running":     func(s *refmodel.Snapshot) { s.Running = !s.Running },
		"since":       func(s *refmodel.Snapshot) { s.Since = s.Since.Add(time.Second) },
		"span":        func(s *refmodel.Snapshot) { s.Stopped = append(s.Stopped, sprint.Span{From: t0}) },
		"beat":        func(s *refmodel.Snapshot) { s.Beats = map[string]sprint.Beat{"m1": {At: t0}} },
		"no beats":    func(s *refmodel.Snapshot) { s.Beats = nil },
		"goal":        func(s *refmodel.Snapshot) { s.Goals.People = append(s.Goals.People, sprint.Goal{Name: "zed"}) },
		"untold":      func(s *refmodel.Snapshot) { s.Untold = append(s.Untold, "zed") },
		"now":         func(s *refmodel.Snapshot) { s.Tables.Now = t0 },
		"coordinator": func(s *refmodel.Snapshot) { s.Tables.Coordinator = "zed" },
		"row":         func(s *refmodel.Snapshot) { s.Tables.Work.SetRows(append(s.Tables.Work.Rows(), "zed")) },
		"text":        func(s *refmodel.Snapshot) { s.Tables.Fleet.Texts["zed"] = map[string]string{"a": "b"} },
		"card":        func(s *refmodel.Snapshot) { s.Tables.Work.Put(&sprint.Card{ID: "zed", Row: "s1", Col: "ready"}) },
		"field": func(s *refmodel.Snapshot) {
			for _, c := range s.Tables.Work.Cards {
				c.Fields["zed"] = "1"
				return
			}
		},
		"note":  func(s *refmodel.Snapshot) { s.Tables.Open = append(s.Tables.Open, sprint.Open{Key: "n0|zed"}) },
		"acked": func(s *refmodel.Snapshot) { s.Tables.Acked = append(s.Tables.Acked, sprint.Open{Key: "n0|zed"}) },
	} {
		c := base.Clone()
		change(&c)
		if dump(c) == dump(base) {
			t.Errorf("a change to the %s of a snapshot is not in its dump", name)
		}
	}
	if dump(base.Clone()) != dump(base) {
		t.Error("a clone does not dump as its snapshot does")
	}
}

func TestACloneSharesNothingWithItsSnapshot(t *testing.T) {
	t.Parallel()
	for _, s := range snapshots()[:deepSamples] {
		before := dump(s.snap)
		c := s.snap.Clone()
		for _, tb := range []*sprint.Table{c.Tables.Work, c.Tables.Readers, c.Tables.Merge, c.Tables.Fleet} {
			tb.SetRows(append(tb.Rows(), "zed"))
			for _, card := range tb.Cards {
				card.Fields["zed"], card.Score = "1", card.Score+1
			}
			for row := range tb.Texts {
				tb.Texts[row]["zed"] = "1"
			}
		}
		for i := range c.Tables.Open {
			c.Tables.Open[i].Note.Primaries = append(c.Tables.Open[i].Note.Primaries, "zed")
			c.Tables.Open[i].Note.Decisions = append(c.Tables.Open[i].Note.Decisions, "zed")
		}
		for m, b := range c.Beats {
			b.Samples = append(b.Samples, sprint.LoadSample{})
			c.Beats[m] = b
		}
		c.Goals.People = append(c.Goals.People, sprint.Goal{Name: "zed"})
		c.Goals.Noted = map[string]string{"zed": "1"}
		c.Untold = append(c.Untold, "zed")
		c.Stopped = append(c.Stopped, sprint.Span{})
		if after := dump(s.snap); after != before {
			t.Fatalf("what was done to a clone changed its snapshot:\n%s", firstDifference(before, after))
		}
	}
}

// firstDifference is the first line at which the two texts differ.
func firstDifference(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < max(len(la), len(lb)); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n  %s\n  %s", i+1, x, y)
		}
	}
	return "none"
}

func TestDecideIsDeterministicAndLeavesItsSnapshotAlone(t *testing.T) {
	t.Parallel()
	for i, s := range snapshots() {
		before := dump(s.snap)
		got := refmodel.Decide(s.snap, s.now)
		first := lines(got)
		if again := lines(refmodel.Decide(s.snap, s.now)); !slices.Equal(first, again) {
			t.Fatalf("snapshot %d: the same snapshot at the same time gave two decisions:\n%s\n%s", i, strings.Join(first, "\n"), strings.Join(again, "\n"))
		}
		if after := dump(s.snap); after != before {
			t.Fatalf("snapshot %d: deciding changed the snapshot:\n%s", i, firstDifference(before, after))
		}
		// Decide copies the snapshot once for every duty; each duty asked alone
		// copies it for itself, and the moves are the same
		var alone []refmodel.Move
		for _, d := range refmodel.Duties {
			alone = append(alone, d.Moves(s.snap, s.now)...)
		}
		if ok, diff := refmodel.Equal(got, alone); !ok {
			t.Fatalf("snapshot %d: Decide, on one copy of the snapshot, is not the duties each on a copy of its own: %s", i, diff)
		}
		if !slices.IsSortedFunc(got, func(a, b refmodel.Move) int { return rank(a) - rank(b) }) {
			t.Fatalf("snapshot %d: the duties are out of the tick's order:%s", i, show(got))
		}
		if ok, diff := refmodel.Equal(got, slices.Clone(got)); !ok {
			t.Fatalf("snapshot %d: the moves are not equal to themselves: %s", i, diff)
		}
	}
}

func TestEveryDutyIsDeterministicAndLeavesItsSnapshotAlone(t *testing.T) {
	t.Parallel()
	for i, s := range snapshots()[:deepSamples] {
		before := dump(s.snap)
		clone := s.snap.Clone()
		for _, d := range refmodel.Duties {
			one := lines(d.Moves(s.snap, s.now))
			if two := lines(d.Moves(s.snap, s.now)); !slices.Equal(one, two) {
				t.Fatalf("snapshot %d: the duty %s gave two decisions", i, d.Name)
			}
			if viaClone := lines(d.Moves(clone, s.now)); !slices.Equal(one, viaClone) {
				t.Fatalf("snapshot %d: a clone of the snapshot is decided differently by the duty %s", i, d.Name)
			}
			if after := dump(s.snap); after != before {
				t.Fatalf("snapshot %d: the duty %s changed the snapshot:\n%s", i, d.Name, firstDifference(before, after))
			}
		}
	}
}

// The planners the duties call are pure as they are read: a tick's part run on
// a snapshot changes nothing of it. Decide would hold either way (it decides on
// a clone); this says the clone is a guard and not a patch over a planner that
// writes to what it reads.
func TestThePlannersDoNotModifyWhatTheyRead(t *testing.T) {
	t.Parallel()
	for i, s := range snapshots()[:deepSamples] {
		c := s.snap.Clone()
		c.Tables.Now = s.now
		before := dump(c)
		for _, p := range sprint.TickParts {
			plan, _ := p.Fn(c.Tables, sprint.TickReq{Who: sprint.MachineActor, Beats: c.Beats})
			sprint.Applied(c.Tables, plan)
			if after := dump(c); after != before {
				t.Fatalf("snapshot %d: the tick's part %s changed what it read:\n%s", i, p.Name, firstDifference(before, after))
			}
		}
	}
}
