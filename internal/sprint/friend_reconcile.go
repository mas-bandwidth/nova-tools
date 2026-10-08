package sprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// friend reconcile (docs/SPEC-SPRINT.md section 1, friend reconcile; the owner, 2026-10-04:
// "trust but VERIFY"; "Are they actually doing the work that is shown in the friend table?
// Really?"). A friend keeps her own account of her cards in inbox/QUEUE.json; the store
// keeps the sprint's, the cards working on her row. When the two disagree (she says every
// card is done while the table shows one working), reconcile settles each card working on
// her row: collected when her outbox/<job>/REPORT.md is there (the finish friend sync
// gives it), kept while her queue holds it queued or working, and returned to ready (its
// work card withdrawn, its primary ready for the tick to deal again) when no report is
// there and her queue says done or does not hold it. An id of her queue that is no card
// working on her row is named and never acted on.

// The states of a task in a friend's QUEUE.json.
const (
	FriendTaskQueued  = "queued"
	FriendTaskWorking = "working"
	FriendTaskDone    = "done"
)

// What reconcile does with one card working on a friend's row (FriendReconcileOf).
const (
	ReconcileCollect = "collect" // her report is there: friend sync's finish
	ReconcileKeep    = "keep"    // she holds it: left working
	ReconcileReturn  = "return"  // she does not: FriendReturn
)

// friendQueueFile is the shape of a friend's inbox/QUEUE.json: her tasks, each a card
// (its id, or its job directory, the id at its epoch as StoredID) and its state.
type friendQueueFile struct {
	Tasks *[]struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"tasks"`
}

// ReadFriendQueue reads a friend's inbox/QUEUE.json into each task's state by its id, the
// state trimmed and in lower case; other keys are passed by. A file that is not JSON, has
// no tasks list, a task with no id, or an id twice is refused: reconcile never guesses
// at her account (docs/SPEC-SPRINT.md section 1, friend reconcile).
func ReadFriendQueue(b []byte) (map[string]string, error) {
	var f friendQueueFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("QUEUE.json is not JSON: %w", err)
	}
	if f.Tasks == nil {
		return nil, fmt.Errorf(`QUEUE.json has no "tasks" list`)
	}
	out := make(map[string]string, len(*f.Tasks))
	for i, t := range *f.Tasks {
		id := strings.TrimSpace(t.ID)
		switch _, twice := out[id]; {
		case id == "":
			return nil, fmt.Errorf("QUEUE.json task %d has no id", i+1)
		case twice:
			return nil, fmt.Errorf("QUEUE.json names %s twice", id)
		}
		out[id] = strings.ToLower(strings.TrimSpace(t.State))
	}
	return out, nil
}

// FriendAccount is a friend's own account of her cards: her QUEUE.json's tasks, each
// state by id (ReadFriendQueue), and when the file was last written.
type FriendAccount struct {
	Tasks   map[string]string
	Written time.Time
}

// FriendReconcileOf is what reconcile does with the work card wc, delivered as job, working
// on a friend's row (docs/SPEC-SPRINT.md section 1, friend reconcile): collect when her
// report is there (reported); else by her account's state for it (by the card's id, else
// by its job): keep while queued or working, return when done, and return when absent
// only if it was dealt before her account was last written: a card dealt since, or with
// no deal stamp to compare, is one she has not had the chance to account for, and is
// kept. A state it does not know is kept and said. why says it in a line.
func FriendReconcileOf(wc *Card, job string, reported bool, a FriendAccount) (action, why string) {
	if reported {
		return ReconcileCollect, "outbox/" + job + "/REPORT.md is there"
	}
	state, ok := a.Tasks[wc.ID]
	if !ok {
		state, ok = a.Tasks[job]
	}
	if wc.Col != Working {
		return ReconcileKeep, "not working (it is " + placeWord(wc) + "): reconcile settles a card working on her row"
	}
	// since when she has held it: the stamp of her own deal, the one deadline's (WorkDeadline;
	// a friend's card is dealt and taken at once, and a working card's deadline reads no
	// snapshot). A stamp is to the second: her account is after it from the next second on.
	_, _, _, own := WorkDeadline(nil, wc)
	held, err := time.Parse(time.RFC3339, wc.F(own))
	written := a.Written.UTC().Truncate(time.Second)
	switch {
	case !ok && (err != nil || !held.Before(written)):
		return ReconcileKeep, "her QUEUE.json does not hold it, and was last written at " + stamp(written) + ", not after it was dealt to her at " + orDash(wc.F(own)) + ": she has not accounted for it yet"
	case !ok:
		return ReconcileReturn, "her QUEUE.json, written at " + stamp(written) + " after it was dealt to her at " + wc.F(own) + ", does not hold it, and outbox/" + job + "/REPORT.md is absent"
	case state == FriendTaskDone:
		return ReconcileReturn, "her QUEUE.json says done and outbox/" + job + "/REPORT.md is absent"
	case state == FriendTaskQueued || state == FriendTaskWorking:
		return ReconcileKeep, "her QUEUE.json says " + state
	}
	return ReconcileKeep, fmt.Sprintf("her QUEUE.json says %q, which is not queued, working or done: left working", state)
}

// FriendQueueStrays is every id of her queue that names none of held (the ids and jobs of
// the cards working on her row), sorted: named by reconcile, never acted on.
func FriendQueueStrays(queue map[string]string, held []string) []string {
	var out []string
	for id := range queue {
		if !slices.Contains(held, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// RetiredByReconcile is the retired_by of a friend's work card friend reconcile returned.
const RetiredByReconcile = "friend reconcile"

// FriendReturnCard is one card reconcile returns: the work card, the generation it read
// and why.
type FriendReturnCard struct {
	ID  string
	Gen int
	Why string
}

// FriendReturnReq returns a friend's abandoned cards to ready.
type FriendReturnReq struct {
	Friend string
	Who    string // who acts: the coordinator
	Cards  []FriendReturnCard
	// Push addresses each card's note to the coordinator, so inbox --push carries it: the
	// run loop's pass, which nobody watches as it acts (friend_reconcile_tick.go).
	Push bool
}

// FriendReturn returns each card named from the friend's row (docs/SPEC-SPRINT.md section
// 1, friend reconcile): a work card working on her row at the generation read, its
// primary working on it, is retired off the table (its record kept, retired_by friend
// reconcile, its return_reason the why), as rework retires a bound card, and its primary
// moves working -> ready, as a member going down leaves one, with no failed-work judgment
// and no redeal spent (the take was never the card's); the tick's friendDealPass places it
// again at its next attempt. A friend's next attempt is a new work card, never this one
// redealt, so the card leaves the table rather than wait withdrawn. One move line and one
// happened note each, saying why. A card not working on her row, at another generation,
// whose primary is not working on it, or named twice is refused.
func FriendReturn(s *Snapshot, r FriendReturnReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	seen := map[string]bool{}
	for _, fc := range r.Cards {
		c := s.Fleet.Card(fc.ID)
		var pr *Card
		if c != nil {
			pr = s.Work.Placed(c.F("primary"))
		}
		twice := seen[fc.ID]
		seen[fc.ID] = true
		switch {
		case twice:
			p.refuse(fc.ID, "named twice")
			continue
		case c == nil:
			p.refuse(fc.ID, "no such work card")
			continue
		case !c.Placed():
			p.refuse(fc.ID, "not working: it is off the table, retired by "+orDash(c.F("retired_by")))
			continue
		case c.Col != Working:
			p.refuse(fc.ID, "not working: it is "+placeWord(c))
			continue
		case c.Row != row:
			p.refuse(fc.ID, "dealt to "+c.Row+", not "+row)
			continue
		case c.Int("gen") != fc.Gen:
			p.refuse(fc.ID, fmt.Sprintf("at generation %d, not %d: it moved since it was read", c.Int("gen"), fc.Gen))
			continue
		case pr == nil || pr.Col != Working || pr.F("work") != c.ID:
			p.refuse(fc.ID, "its primary "+c.F("primary")+" is not working on it")
			continue
		}
		what := "returned by friend reconcile: " + fc.Why
		n := happened(NFriendReturned, pr.Row, s.Now, pr.ID)
		n.Who, n.What = r.Who, what
		if r.Push {
			n.To, n.Hint = s.Coordinator, "run: nova-sprint card "+pr.ID
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{
			change(Fleet, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByReconcile, "return_reason": fc.Why})),
			change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")),
		}, Notes: []Note{n}, Moved: fmt.Sprintf("%s working -> retired, %s; %s working -> ready", c.ID, what, pr.ID)})
	}
	return Lawful(p)
}
