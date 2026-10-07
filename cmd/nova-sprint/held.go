package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// held and sentinels are reads (docs/SPEC-SPRINT.md section 11): each changes
// nothing, needs no actor, and lists in one call what a wave waits on, which
// took a chain of card calls before (the comfort list of 2026-10-03, item 1).
func init() {
	verbClasses["held"] = classRead
	verbClasses["sentinels"] = classRead
}

// heldCard is one held primary: admitted held (add --held, until release), or
// waiting behind a sentinel not released by its place in line, with the needs
// it names that have not landed.
type heldCard struct {
	ID     string   `json:"id"`
	Stream string   `json:"stream"`
	Held   bool     `json:"held"`
	Behind []string `json:"behind,omitempty"`
	Needs  []string `json:"needs,omitempty"`
}

// heldTarget is one hold in force on a stream or a fleet member: its control
// card's held stamp (FieldHeld), the reason recorded beside it (FieldHeldReason)
// and who held it, with the age of the hold. A stream hold is what left its
// cards off the work table, so `held` names it here even though no work card is
// waiting (the defect of 2026-10-07: 186 cards idle 26 hours under a stream
// hold that `held` printed 0 for).
type heldTarget struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // stream or member
	Reason string `json:"reason,omitempty"`
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	Age    string `json:"age,omitempty"`
}

// sentinelCard is one sentinel on the table: whether it is reached, the needs
// it names that have not landed, and the waiting cards it gates (what its
// release lets go).
type sentinelCard struct {
	ID      string   `json:"id"`
	Stream  string   `json:"stream"`
	Reached bool     `json:"reached"`
	Needs   []string `json:"needs,omitempty"`
	Behind  int      `json:"behind"`
}

// cmdHeld lists what a wave waits on: every held primary and what it waits on,
// and every held stream and member with its reason and age. It is one read of
// the four tables, so a stream hold -- which withdraws its cards from the work
// table -- is named here even though no work card is waiting for it (--stream
// keeps one stream's; --json one object).
func (a *app) cmdHeld(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("held")
	stream := fs.String("stream", "", "the held cards of one stream (default: every stream)")
	s, code := a.workSnapshot("held", fs, args, c, stderr, store.All)
	if code != 0 {
		return code
	}
	cards := sprintHeld(s, *stream)
	targets := sprintHolds(s, *stream, a.now())
	if c.json {
		b, _ := json.Marshal(struct {
			Cards []heldCard   `json:"cards"`
			Holds []heldTarget `json:"holds"`
		}{cards, targets})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	held, behind := 0, 0
	for _, h := range cards {
		if h.Held {
			held++
		}
		if len(h.Behind) > 0 {
			behind++
		}
		fmt.Fprintf(stdout, "HELD %s stream=%s held=%s behind=%s needs=%s\n", oneline.Escape(h.ID), oneline.Escape(h.Stream), yesOrDash(h.Held), oneline.Field(dashed(strings.Join(h.Behind, ","))), oneline.Field(dashed(strings.Join(h.Needs, ","))))
	}
	streams, members := 0, 0
	for _, t := range targets {
		if t.Kind == "stream" {
			streams++
		} else {
			members++
		}
		fmt.Fprintf(stdout, "HELD-TARGET %s kind=%s reason=%s age=%s by=%s\n", oneline.Escape(t.Name), oneline.Escape(t.Kind), oneline.Field(dashed(t.Reason)), oneline.Field(dashed(t.Age)), oneline.Field(dashed(t.By)))
	}
	fmt.Fprintf(stdout, "HELD OK cards=%d held=%d behind=%d streams=%d members=%d\n", len(cards), held, behind, streams, members)
	return 0
}

// cmdSentinels lists the sentinels on the table and what each gates: whether it
// is reached, the needs it names that have not landed, and how many waiting
// cards its release lets go, one line a sentinel, from one read of the work
// table (--stream keeps one stream's; --json one object).
func (a *app) cmdSentinels(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("sentinels")
	stream := fs.String("stream", "", "the sentinels of one stream (default: every stream)")
	s, code := a.workSnapshot("sentinels", fs, args, c, stderr, []string{sprint.Work})
	if code != 0 {
		return code
	}
	cards := sprintSentinels(s, *stream)
	if c.json {
		b, _ := json.Marshal(struct {
			Sentinels []sentinelCard `json:"sentinels"`
		}{cards})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, x := range cards {
		fmt.Fprintf(stdout, "SENTINEL %s stream=%s reached=%s behind=%d needs=%s\n", oneline.Escape(x.ID), oneline.Escape(x.Stream), yesOrDash(x.Reached), x.Behind, oneline.Field(dashed(strings.Join(x.Needs, ","))))
	}
	fmt.Fprintf(stdout, "SENTINELS OK sentinels=%d\n", len(cards))
	return 0
}

// workSnapshot is a read verb's one read of the tables it names: its flags parsed
// (no words), the store opened, the tables loaded in the one call; the exit code
// when it was refused or the read failed.
func (a *app) workSnapshot(verbName string, fs flagSet, args []string, c *common, stderr io.Writer, tables []string) (*sprint.Snapshot, int) {
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return nil, refuse(stderr, verbName, argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	s, err := st.Load(context.Background(), tables, nil)
	if err != nil {
		return nil, a.readFailed(verbName, err, stderr)
	}
	return s, 0
}

// sprintHolds is every hold in force on a stream or a fleet member, in table
// order: a stream whose control card's held field is set (none of its ready
// primaries is dealt until unhold) and a member whose is. The reason and the
// held stamp come from the control card (FieldHeldReason, FieldHeld); age is
// that stamp's distance from now, so a hold that has sat for a day says so.
func sprintHolds(s *sprint.Snapshot, only string, now time.Time) []heldTarget {
	out := []heldTarget{}
	for _, r := range heldRows(s.Merge) {
		if only != "" && r != only {
			continue
		}
		ctl := s.Merge.Card(sprint.CtlID(r))
		if ctl == nil || ctl.F(sprint.FieldHeld) == "" {
			continue
		}
		out = append(out, heldTarget{Name: r, Kind: "stream", Reason: ctl.F(sprint.FieldHeldReason),
			At: ctl.F(sprint.FieldHeld), Age: heldAge(ctl.F(sprint.FieldHeld), now)})
	}
	for _, r := range heldRows(s.Fleet) {
		if only != "" && r != only || sprint.IsFriendRow(r) {
			continue
		}
		ctl := s.Fleet.Card(sprint.CtlID(r))
		if ctl == nil || ctl.F(sprint.FieldHeld) == "" {
			continue
		}
		out = append(out, heldTarget{Name: r, Kind: "member", Reason: ctl.F(sprint.FieldHeldReason), By: ctl.F(sprint.FieldHeldBy),
			At: ctl.F(sprint.FieldHeld), Age: heldAge(ctl.F(sprint.FieldHeld), now)})
	}
	return out
}

// heldRows is a table's rows, empty for a table no read loaded.
func heldRows(t *sprint.Table) []string {
	if t == nil {
		return nil
	}
	return t.Rows()
}

// heldAge is a held stamp's age, "" when it cannot be read.
func heldAge(at string, now time.Time) string {
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return ""
	}
	return now.Sub(when).Truncate(time.Second).String()
}

// sprintHeld is the held cards of one snapshot, in work order: every waiting
// primary admitted held or behind a sentinel by its place (the cards where's
// held count counts), with the sentinels it waits behind and the needs it
// names that have not landed (sprint.PositionWaits, sprint.NamedWaits).
func sprintHeld(s *sprint.Snapshot, only string) []heldCard {
	out := []heldCard{}
	for _, c := range s.Work.Column(sprint.Waiting) {
		if sprint.IsSentinel(c) || only != "" && c.Row != only {
			continue
		}
		behind := sprint.PositionWaits(s, c, nil)
		if !sprint.IsHeld(c) && len(behind) == 0 {
			continue
		}
		out = append(out, heldCard{ID: c.ID, Stream: c.Row, Held: sprint.IsHeld(c), Behind: behind, Needs: sprint.NamedWaits(s, c, nil)})
	}
	return out
}

// sprintSentinels is the sentinels on the table and not landed, in work order,
// each with whether it is reached, its unlanded needs and the count of waiting
// cards its release lets go (sprint.Behind).
func sprintSentinels(s *sprint.Snapshot, only string) []sentinelCard {
	out := []sentinelCard{}
	for _, c := range s.Work.Column(sprint.Waiting) {
		if !sprint.IsSentinel(c) || only != "" && c.Row != only {
			continue
		}
		out = append(out, sentinelCard{ID: c.ID, Stream: c.Row, Reached: c.F("reached") != "", Needs: sprint.NamedWaits(s, c, nil), Behind: len(sprint.Behind(s, c))})
	}
	return out
}

func yesOrDash(b bool) string {
	if b {
		return "yes"
	}
	return "-"
}
