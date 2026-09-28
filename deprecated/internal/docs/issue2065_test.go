package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2065_test.go pins nova-tools #2065 (parent: the coordinator-handover
// tracking issue): standing rulings and the Glenn's-hands queue are records
// in the repo.
//
// Glenn's lens, 2026-09-20, verbatim: "Everything that you have done as
// coordinator in the past few days, is something that Stella needs to be able
// to do easily." What a handover cannot carry is what only one assistant's
// memory holds: the rulings a coordinator must apply existed as verbatim
// quotes scattered through cairns and one assistant's memory files, and each
// request waiting on an act only Glenn can do was a chat message that
// scrolled away. #2065 asks that both are records in the repo — a ruling with
// id, date, verbatim words, scope, who may apply it and superseded-by; a
// hands row with the exact command or decision wanted, why, what it blocks,
// and state (asked, done, verified by whom and how).
//
// The bounded first step pinned here is the record on the page the house
// reads first: ROADMAP.md carries both registers with the fields the issue
// names. The rest of the ask — docs/RULINGS.md or a sexp as the primary
// form, a verb to add a ruling from a quote, and `nova-pulse hands` showing
// the queue in the brief — is the follow-up the section points at.
func TestIssue2065(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../ROADMAP.md")
	if err != nil {
		t.Fatalf("ROADMAP.md: %v", err)
	}
	content := string(raw)

	start := strings.Index(content, "## Standing rulings and the Glenn's-hands queue")
	if start < 0 {
		t.Fatalf("ROADMAP.md has no \"## Standing rulings and the Glenn's-hands queue\" section (nova-tools #2065): what Glenn has ruled and what is waiting on an act only he can do are still quotes scattered through cairns and one assistant's memory files, not records in the repo")
	}
	section := content[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	// Collapse the section's markdown line wrapping so a phrase wrapped over
	// two lines still reads as one text.
	flat := strings.Join(strings.Fields(section), " ")

	// The lens the section exists for, verbatim, and the diagnosis it answers.
	for _, want := range []string{
		"Everything that you have done as coordinator in the past few days, is something that Stella needs to be able to do easily.",
		"issues/2065",
		"coordinator-handover",
		"records in this repo",
		"verbatim quotes scattered through cairns and one assistant's memory files",
		"chat message that scrolled away",
		"inputs to a handover",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the standing-rulings section is missing %q (nova-tools #2065)", want)
		}
	}

	// A ruling is a record with id, date, verbatim words, scope, who may
	// apply it and superseded-by; a hands row is a record with the exact
	// command or decision wanted, why, what it blocks and state.
	for _, want := range []string{
		"| ID | Date | Verbatim words | Scope | Who may apply | Superseded-by |",
		"| ID | Wanted | Exact command or decision | Why | Blocks | State |",
		"the exact command or decision wanted",
		"verified names who verified it and how",
		"leaves the queue only on verified",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the standing-rulings section is missing the record shape %q that #2065 asks every row to carry (nova-tools #2065)", want)
		}
	}

	// Every ruling the issue names is recorded, words as carried.
	for _, want := range []string{
		"free route first with a short deadline, paid fallback",
		"cheapest good model wins, log every route",
		"every bench runs any card (one bench standard)",
		"fleet setup is zero manual steps",
		"rent before buy",
		"never chase a moving dev, adopt at a frozen sha",
		"canary five before widening",
		"a failed card is parked after two tries and raised",
		"count nova-work from the sexp, not ROADMAP.md",
		"friends fix what they find",
		"force-push under lease to fix a break",
		"a lone two-parent merge of dev is acceptable for Lisp PRs",
		"Stella's scoped-policy caveat",
		"WSL-only",
		"drop the native windows CI runners. WSL only from now on.",
		"UNKNOWN is never requeued",
		"what a wake may execute",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the standing rulings are missing %q (nova-tools #2065)", want)
		}
	}

	// Every row of the Glenn's-hands queue of 2026-09-20 is recorded, each
	// still waiting on an act only Glenn can do.
	for _, want := range []string{
		"sshd limits",
		"two Macs",
		"failed silently",
		"the bench user ruling",
		"the one Go version",
		"scoped provider keys with caps for hetzner",
		"a darwin-x64 harness download",
		"spend limits",
		"asked 2026-09-20",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the Glenn's-hands queue is missing %q (nova-tools #2065)", want)
		}
	}

	// Stella's hold at 8a099710: a Wanted label ("the bench user ruling") is an
	// index entry, not the exact command or decision #2065 asks every hands
	// row to carry. Each row either carries the words from its source or says
	// the words are unavailable and is marked incomplete -- and an incomplete
	// row is never done or verified, because nothing exact was there to act on
	// or to verify.
	for _, want := range []string{
		"An incomplete row is an index entry, not a request a handover can act on or verify",
		"cannot move to done or verified until that field is filled",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the Glenn's-hands queue does not define the incomplete row: missing %q (nova-tools #2065)", want)
		}
	}
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| H-") {
			continue
		}
		rows++
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) != 6 {
			t.Errorf("hands row %q has %d cells, want 6 (ID, Wanted, Exact command or decision, Why, Blocks, State) (nova-tools #2065)", line, len(cells))
			continue
		}
		id, exact, state := cells[0], cells[2], cells[5]
		unavailable := strings.HasPrefix(exact, "unavailable:")
		incomplete := strings.Contains(state, "**incomplete**")
		switch {
		case unavailable && !incomplete:
			t.Errorf("hands row %s has no exact command or decision (%q) but its state %q is not marked **incomplete** (nova-tools #2065)", id, exact, state)
		case !unavailable && incomplete:
			t.Errorf("hands row %s is marked incomplete but carries an exact command or decision %q; fill one or the other (nova-tools #2065)", id, exact)
		case !unavailable && !strings.ContainsAny(exact, "`\""):
			t.Errorf("hands row %s: the exact command or decision %q is neither quoted from its source nor marked unavailable (nova-tools #2065)", id, exact)
		}
		if incomplete && (strings.Contains(state, "; done") || strings.Contains(state, "verified by")) {
			t.Errorf("hands row %s is incomplete and cannot be done or verified: %q (nova-tools #2065)", id, state)
		}
	}
	if rows != 6 {
		t.Errorf("the Glenn's-hands queue has %d rows, want the six of 2026-09-20 (nova-tools #2065)", rows)
	}
}
