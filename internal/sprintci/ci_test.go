package sprintci

import (
	"fmt"
	"testing"
)

// TestCIPassIsACardUnderSlotAccounting is the #2842 acceptance test: a CI
// pass that is not a card, or that would hold more than the dealer's CI
// share, does not run. Half the machine stays for model cards.
func TestCIPassIsACardUnderSlotAccounting(t *testing.T) {
	t.Parallel()

	const width = 8
	d, err := New(width)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.CIShare(), width/2; got != want {
		t.Fatalf("CI share = %d, want %d", got, want)
	}
	if got, want := d.ModelReserve(), width-width/2; got != want {
		t.Fatalf("model reserve = %d, want %d: half the slots stay for model cards", got, want)
	}

	// Nine heads is the old shard shape (one pass per package). The dealer
	// admits one card per head, one slot each, and only inside the share.
	for i := 0; i < 9; i++ {
		c := Card{Repo: "example/nova", PR: 2800 + i, SHA: shaN(i)}
		id, err := c.ID()
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("ci-%d-%s", c.PR, c.SHA[:8]); id != want {
			t.Fatalf("card id = %s, want %s", id, want)
		}
		ran, reason := d.RunCI(c)
		if i < d.CIShare() {
			if !ran {
				t.Fatalf("card %s is inside the dealer's share and did not run: %s", id, reason)
			}
			continue
		}
		if ran {
			t.Fatalf("a CI pass ran outside the dealer's shares: %s", id)
		}
		if reason != OutsideShares {
			t.Fatalf("reason = %q, want %q", reason, OutsideShares)
		}
	}
	if d.CIHeld() != width/2 {
		t.Fatalf("CI held %d slots, want the share %d", d.CIHeld(), width/2)
	}

	// A rerun of the same head is the same card. It does not take a second slot.
	again := Card{Repo: "example/nova", PR: 2800, SHA: shaN(0)}
	if ran, reason := d.RunCI(again); !ran {
		t.Fatalf("the same head is the same card: %s", reason)
	}
	if d.CIHeld() != width/2 {
		t.Fatalf("a rerun took a second slot: CI held %d", d.CIHeld())
	}

	// A pass that is not a card does not run and does not take a slot.
	if ran, _ := d.RunCI(Card{Repo: "example/nova", PR: 1, SHA: "not-a-head"}); ran {
		t.Fatal("a CI pass ran outside the dealer's shares")
	}
	if d.CIHeld() != width/2 {
		t.Fatalf("a non-card took a slot: CI held %d", d.CIHeld())
	}

	// The reserved half is still there for model cards, and no further CI pass runs.
	for i := 0; i < d.ModelReserve(); i++ {
		if ran, reason := d.TakeModel(fmt.Sprintf("model-%d", i)); !ran {
			t.Fatalf("model slot %d stayed and must be takeable: %s", i, reason)
		}
	}
	if ran, _ := d.TakeModel("model-extra"); ran {
		t.Fatal("a model card took a slot the machine does not have")
	}
	if ran, _ := d.RunCI(Card{Repo: "example/nova", PR: 2900, SHA: shaN(20)}); ran {
		t.Fatal("a CI pass ran outside the dealer's shares")
	}

	// Idle CI slots may be used by model cards. The model reserve may not be
	// used by CI, even when no model card is running.
	open, err := New(width)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if ran, reason := open.TakeModel(fmt.Sprintf("model-%d", i)); !ran {
			t.Fatalf("model card %d: %s", i, reason)
		}
	}
	for i := 0; i < open.CIShare(); i++ {
		c := Card{Repo: "example/nova", PR: 3000 + i, SHA: shaN(30 + i)}
		if ran, reason := open.RunCI(c); !ran {
			t.Fatalf("CI card inside the share did not run: %s", reason)
		}
	}
	if ran, _ := open.RunCI(Card{Repo: "example/nova", PR: 3099, SHA: shaN(39)}); ran {
		t.Fatal("a CI pass ran outside the dealer's shares")
	}
	if open.ModelHeld() != 4 || open.CIHeld() != 4 {
		t.Fatalf("held model=%d ci=%d, want 4 and 4", open.ModelHeld(), open.CIHeld())
	}

	// A different sha is not this head, so it does not sit in this head's slot.
	narrow, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	head := Card{Repo: "example/nova", PR: 7, SHA: shaN(1)}
	other := Card{Repo: "example/nova", PR: 7, SHA: shaN(2)}
	if ran, reason := narrow.RunCI(head); !ran {
		t.Fatalf("the one CI slot: %s", reason)
	}
	if ran, _ := narrow.RunCI(other); ran {
		t.Fatal("a CI pass for another sha ran as this head")
	}
	if narrow.CIHeld() != 1 {
		t.Fatalf("CI held %d, want the one head", narrow.CIHeld())
	}
	idA, _ := head.ID()
	idB, _ := other.ID()
	if idA == idB {
		t.Fatalf("fixture shas share an id %s", idA)
	}

	// Width 1: the one slot stays for a model card. CI's share is empty.
	solo, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	if solo.CIShare() != 0 || solo.ModelReserve() != 1 {
		t.Fatalf("width 1 share=%d reserve=%d, want 0 and 1", solo.CIShare(), solo.ModelReserve())
	}
	if ran, _ := solo.RunCI(Card{Repo: "example/nova", PR: 1, SHA: shaN(1)}); ran {
		t.Fatal("a CI pass ran outside the dealer's shares")
	}
	if ran, reason := solo.TakeModel("only"); !ran {
		t.Fatalf("the one slot stays for a model card: %s", reason)
	}

	if _, err := New(-1); err == nil {
		t.Fatal("a negative machine width must be refused, not guessed")
	}
}

func shaN(n int) string {
	// The varying digits are the first eight, which is the id's sha8.
	return fmt.Sprintf("%08x%032d", n, 0)
}
