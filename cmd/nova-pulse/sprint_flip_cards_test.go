package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// sprint_flip_cards_test.go is the wiring test: `sprint status --flip` through the same
// deps.cards factory defaultSprintDeps installs (sprint.DialCards), against a miniredis
// stand-in for the fleet Redis, so this is the HOLD 7/10 Emma raised at 1e5e0227 -- close no
// longer answers from sprint.FakeCards, it answers from one XADD on cards:done.

// cardsDeps is sprintTestDeps (sprint_test.go) with deps.cards replaced by the real
// sprint.DialCards, the way defaultSprintDeps wires it, instead of the fake.
func cardsDeps(store *sprint.FakeStore, cardsAddr string) sprintDeps {
	d := sprintTestDeps(store)
	d.cards = func(addr, user, password string) (sprint.Cards, error) {
		return sprint.DialCards(context.Background(), cardsAddr, user, password)
	}
	return d
}

// TestStatusFlipReadsRealCardEvidence: one OK entry XADDed to cards:done, read back through
// sprint.DialCards, is what closes the task -- the wiring #2587 -> sprint.RedisCards ->
// sprint status --flip, with no sprint.FakeCards anywhere in the path.
func TestStatusFlipReadsRealCardEvidence(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	evStore, err := events.Open(ctx, events.Dial{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("dialing the miniredis events store: %v", err)
	}
	defer evStore.Close()
	at := time.Date(2026, 9, 22, 19, 0, 0, 0, time.UTC)
	id, err := evStore.Emit(ctx, events.Event{Label: "cell-a", Kind: events.OK, At: at})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	store := sprint.NewFakeStore()
	deps := cardsDeps(store, mr.Addr())
	addr := []string{"--store", "store.invalid:6380"}
	var setup, errOut bytes.Buffer
	setupArgs := [][]string{
		append([]string{"open", "--name", "s", "--goal", "g"}, addr...),
		append([]string{"add", "--name", "s", "--id", "landed", "--ref", "cell-a", "--kind", "card"}, addr...),
		append([]string{"add", "--name", "s", "--id", "out", "--ref", "cell-b", "--kind", "card"}, addr...),
	}
	for _, args := range setupArgs {
		setup.Reset()
		errOut.Reset()
		if code := runSprint(args, &setup, &errOut, testNow, deps); code != 0 {
			t.Fatalf("setup %v exited %d: %s", args, code, errOut.String())
		}
	}

	var out bytes.Buffer
	if code := runSprint(append([]string{"status", "--flip"}, addr...), &out, &errOut, testNow, deps); code != 0 {
		t.Fatalf("status --flip exited %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "CLOSED landed cell-a") {
		t.Fatalf("the flip did not close the card the stream landed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), id) {
		t.Fatalf("the evidence does not carry the event id %q:\n%s", id, out.String())
	}
	if strings.Contains(out.String(), "CLOSED out") {
		t.Fatalf("the flip closed a card the stream said nothing about:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1/2 50%") {
		t.Fatalf("the line after the flip is:\n%s", out.String())
	}
}
