package machine

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// The tick-end note (errata 3 amendment 8; SprintEvents.tla, TickEnd and
// TickEndOnce): the coordinator is woken once a tick, at its end, with every
// item of the tick ready to read, and not at all by a tick that addressed it
// nothing (the owner's rule, 2026-09-30: "one wake, end of turn, with all
// inbox ready to read and act on"; "and no wake, if nothing in the inbox").
//
// N, the note's judgments=N, is the items addressed to the coordinator
// (sprint.TickEndCounts) that the tick writes and the lines of them it
// ingests that no tick-end line has covered yet:
//
//   - the notes of the steps RT3 sends: the quarantine step's and the dealt
//     steps' (a step that is refused writes none of its own, so N may count
//     more than the batch holds; never fewer);
//   - the lines the ingest moves the cursor past after the last tick-end line
//     among them (sprint.TickEndCountsLine): a verb's judgment (the merger's
//     stop, a return, a red CI), and the error step's, which RT1 writes before
//     its page. A line before a tick-end line was covered by that note, whose
//     wake read it. A page cut short of Layer 2's last may have a tick-end
//     after it, so its count waits in Loop.unwoken for a page that reaches
//     the last.
//
// The note is the tick's last step: a notes-only step of its own after the
// rules' steps in RT3 (so no guard race of a rule's step loses it, and no rule
// step is pushed over a bound by it), or the ingest step when RT3 is sure to be
// empty (nothing read, no note, no look). It costs one note and one step of
// the tick's budget, reserved before the deal, and no round trip. A tick-end
// that finds no carrier (RT3 came out empty) or whose step did not apply is
// owed to the next RT1's error step, which writes it as it writes every note
// a tick owes.

// TickEndRule is the rule the tick-end step names in its meta.
const TickEndRule = sprint.TickEnd

// tickEndStep is the notes-only step that carries the tick-end note of n.
func (l *Loop) tickEndStep(n int) *sprintfn.Request {
	return &sprintfn.Request{Epoch: l.epoch, Meta: sprintfn.Meta{Rule: TickEndRule, Tick: true, Gen: l.gen},
		Body: sprintfn.Body{Notes: []sprint.NoteReq{sprint.TickEndNote(n)}}}
}

// tickEndReserve is what the tick-end step may cost: its bytes at the largest
// count a tick could write, one note and one step.
func (l *Loop) tickEndReserve() (stepCost, *sprintfn.Refusal) {
	return costOf(l.cfg.Names.Prefix, l.tickEndStep(1<<31-1))
}

// addressed counts the notes of requests that are addressed to the
// coordinator (sprint.TickEndCounts).
func addressed(reqs ...*sprintfn.Request) int {
	n := 0
	for _, r := range reqs {
		if r == nil {
			continue
		}
		for _, nr := range r.Body.Notes {
			if sprint.TickEndCounts(nr) {
				n++
			}
		}
	}
	return n
}

// pageWake is the lines of a page addressed to the coordinator and not
// covered by a tick-end line, counted on from the loop's unwoken: a tick-end
// line covers every line before it.
func pageWake(unwoken int, events []sprint.Event) int {
	for _, e := range events {
		switch {
		case e.Kind == sprint.TickEnd:
			unwoken = 0
		case sprint.TickEndCountsLine(e):
			unwoken++
		}
	}
	return unwoken
}

// plansAddress says a plan of the tick has a note addressed to the
// coordinator: the tick may write a tick-end, so the deal reserves it.
func plansAddress(ps []*planned) bool {
	for _, p := range ps {
		if addressed(p.reqs...) > 0 {
			return true
		}
	}
	return false
}

// tickEndOf is the count of a request's tick-end note, its last note; 0 when
// it carries none.
func tickEndOf(r *sprintfn.Request) int {
	if r == nil || len(r.Body.Notes) == 0 {
		return 0
	}
	last := r.Body.Notes[len(r.Body.Notes)-1]
	if last.Op != sprint.NoteOpTickEnd {
		return 0
	}
	n, _ := sprint.TickEndCount(last.Text)
	return n
}
