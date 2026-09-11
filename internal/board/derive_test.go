package board

import (
	"strings"
	"testing"
	"time"
)

// later builds one of the four later events.
func later(verb, id, ev, after, as, when string, override bool) string {
	e := Event{Verb: verb, ID: id, Ev: ev, After: after, As: as, At: mustTime2(when), Override: override}
	switch verb {
	case "closed":
		e.Tail = Tail("it is done")
	case "probed":
		e.Tail = Tail("./reports/probe.txt")
	case "landed":
		e.In = "mas-bandwidth/nova-tools#42"
	}
	return e.Render()
}

// THE FIRST CLOSE IS THE CLOSE. A second closing event is permitted and changes nothing:
// it is in the log where a reader can see that two lines thought they had finished the
// same thing.
func TestASecondCloseChangesNothing(t *testing.T) {
	lines := []string{
		card(idA, "a thing two lines both finished"),
		later("closed", idA, "0000000000a1", idA, "ada", "2026-09-10T09:10:00Z", false),
		later("landed", idA, "0000000000b2", "0000000000a1", "bo", "2026-09-10T09:20:00Z", false),
	}
	b := Derive(Log{Lines: lines}, mustTime2("2026-09-10T10:00:00Z"), 10*time.Minute)
	c := b.Cards[0]
	if c.State != "CLOSED" || c.Close.As != "ada" || c.Close.Verb != "closed" {
		t.Errorf("the FIRST close is the close: got %s by %s", c.Close.Verb, c.Close.As)
	}
	if c.Conflicts != 0 {
		t.Errorf("a close that names the close it saw is not concurrent: conflicts=%d", c.Conflicts)
	}
	if len(c.Events) != 3 {
		t.Errorf("the later close left the log: %d events", len(c.Events))
	}
}

// A RE-TAKE MOVES THE OWNER, and takes are repeatable because that is the remedy for a
// stale take being a guess about a clock rather than about a line.
func TestARetakeMovesTheOwner(t *testing.T) {
	lines := []string{
		card(idA, "a thing that changes hands"),
		later("taken", idA, "0000000000a1", idA, "ada", "2026-09-10T09:10:00Z", false),
		later("taken", idA, "0000000000b2", "0000000000a1", "bo", "2026-09-10T09:20:00Z", false),
	}
	b := Derive(Log{Lines: lines}, mustTime2("2026-09-10T09:25:00Z"), 10*time.Minute)
	if got := b.Cards[0].Owner; got != "bo" {
		t.Errorf("owner = %q, want the latest take in the fold order", got)
	}
	if b.Cards[0].Conflicts != 0 {
		t.Errorf("conflicts = %d, want 0: the second take names the first", b.Cards[0].Conflicts)
	}
}

// STALENESS IS AN ANNOTATION AND WRITES NOTHING. It is a pure function of (the latest
// event's stamp, now, the window), and the same log at two instants is the same log.
func TestStaleIsAnAnnotationAndAPureFunction(t *testing.T) {
	lines := []string{
		card(idA, "a thing that goes quiet"),
		later("taken", idA, "0000000000a1", idA, "ada", "2026-09-10T09:10:00Z", false),
	}
	fresh := Derive(Log{Lines: lines}, mustTime2("2026-09-10T09:19:59Z"), 10*time.Minute)
	if fresh.Cards[0].Stale {
		t.Error("one second under the window is stale")
	}
	old := Derive(Log{Lines: lines}, mustTime2("2026-09-10T09:20:01Z"), 10*time.Minute)
	if !old.Cards[0].Stale {
		t.Error("past the window the card is not stale")
	}
	if old.Cards[0].Owner != "ada" || old.Cards[0].State != "OPEN" {
		t.Error("staleness reassigned or closed something; nothing is deleted and nothing is reassigned")
	}
	if len(old.Cards[0].Events) != len(fresh.Cards[0].Events) {
		t.Error("the fold wrote an event for something it only inferred from a clock")
	}
	// The window is the caller's, per run: a wider one is not stale.
	if Derive(Log{Lines: lines}, mustTime2("2026-09-10T09:20:01Z"), time.Hour).Cards[0].Stale {
		t.Error("the window came from somewhere other than the argument")
	}
}

// THE SAME EVENTS IN TWO TEXTUAL ORDERS DERIVE ONE OWNER, and a union merge that holds one
// line twice folds it once.
func TestTheTextualOrderAndADuplicatedLineChangeNothing(t *testing.T) {
	ada := later("taken", idA, "0000000000a1", idA, "Ada", "2026-09-10T09:10:00Z", false)
	bo := later("taken", idA, "0000000000b2", idA, "Bo", "2026-09-10T09:10:00Z", false)
	root := card(idA, "one card, two clones")
	forward := Derive(Log{Lines: []string{root, ada, bo}}, mustTime2("2026-09-10T09:15:00Z"), 10*time.Minute)
	backward := Derive(Log{Lines: []string{root, bo, ada}}, mustTime2("2026-09-10T09:15:00Z"), 10*time.Minute)
	twice := Derive(Log{Lines: []string{root, bo, ada, bo, root}}, mustTime2("2026-09-10T09:15:00Z"), 10*time.Minute)
	for _, b := range []*Board{forward, backward, twice} {
		if b.Cards[0].Owner != "Bo" {
			t.Errorf("owner = %q, want Bo — the take whose as= sorts later at one second", b.Cards[0].Owner)
		}
		if b.Cards[0].Conflicts != 1 {
			t.Errorf("conflicts = %d, want the one pair the fold chose over", b.Cards[0].Conflicts)
		}
	}
	if len(twice.Cards[0].Events) != 3 {
		t.Errorf("a union merge holding a line twice folded %d events, want 3", len(twice.Cards[0].Events))
	}
}

// An event whose at= will not parse folds LAST and is counted, never guessed at.
func TestAnUnreadableStampFoldsLastAndIsCounted(t *testing.T) {
	broken := "taken " + idA + " ev=0000000000c3 after=" + idA + " as=zz at=yesterday override=false"
	lines := []string{
		card(idA, "a card with a stamp nobody can read"),
		later("taken", idA, "0000000000a1", idA, "ada", "2026-09-10T09:10:00Z", false),
		broken,
	}
	b := Derive(Log{Lines: lines}, mustTime2("2026-09-10T09:15:00Z"), 10*time.Minute)
	if b.Unparsed != 1 {
		t.Errorf("unparsed = %d, want the one unreadable stamp counted", b.Unparsed)
	}
	if got := b.Cards[0].Owner; got != "zz" {
		t.Errorf("owner = %q; an unreadable stamp folds LAST rather than being guessed at", got)
	}
	if !strings.Contains(b.Cards[0].Events[2].AtRaw, "yesterday") {
		t.Error("the stamp was rewritten; it is carried as it was written")
	}
}
