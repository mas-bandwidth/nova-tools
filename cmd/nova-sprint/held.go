package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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

// cmdHeld lists the held cards and what each waits on: every waiting primary
// admitted held or behind a sentinel by its place, one line a card, from one
// read of the work table (--stream keeps one stream's; --json one object).
func (a *app) cmdHeld(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("held")
	stream := fs.String("stream", "", "the held cards of one stream (default: every stream)")
	s, code := a.workSnapshot("held", fs, args, c, stderr)
	if code != 0 {
		return code
	}
	cards := sprintHeld(s, *stream)
	if c.json {
		b, _ := json.Marshal(struct {
			Cards []heldCard `json:"cards"`
		}{cards})
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
	fmt.Fprintf(stdout, "HELD OK cards=%d held=%d behind=%d\n", len(cards), held, behind)
	return 0
}

// cmdSentinels lists the sentinels on the table and what each gates: whether it
// is reached, the needs it names that have not landed, and how many waiting
// cards its release lets go, one line a sentinel, from one read of the work
// table (--stream keeps one stream's; --json one object).
func (a *app) cmdSentinels(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("sentinels")
	stream := fs.String("stream", "", "the sentinels of one stream (default: every stream)")
	s, code := a.workSnapshot("sentinels", fs, args, c, stderr)
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

// workSnapshot is a read verb's one read of the work table: its flags parsed
// (no words), the store opened, the table loaded; the exit code when it was
// refused or the read failed.
func (a *app) workSnapshot(verbName string, fs flagSet, args []string, c *common, stderr io.Writer) (*sprint.Snapshot, int) {
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return nil, refuse(stderr, verbName, argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	if err != nil {
		return nil, a.readFailed(verbName, err, stderr)
	}
	return s, 0
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
