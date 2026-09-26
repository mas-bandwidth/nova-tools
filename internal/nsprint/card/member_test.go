//go:build functional

package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestFsckDutyRemovesMembersNotACard (#4054): the reconciler's fsck duty
// walks every ws, bench and friend set, not only a sprint's own card ids,
// so a member that is not the id of any record (the import-pipe shape found
// in a friend's ready set, and a card-shaped id whose record never existed)
// is removed with one ws:log receipt each, while every real card keeps its
// views; the next walk finds nothing.
func TestFsckDutyRemovesMembersNotACard(t *testing.T) {
	t.Parallel()

	w := newGhWorld(t)
	const s = "live-4054"
	w.open(t, s, 1)
	w.push(t, s, "real", "none", "pool")
	real := card.CardKey(s, "real")
	pipeShape := "/private/tmp/nova/import-ab.pipe:task:build-3155-ghost"
	noRecord := card.CardKey(s, "never-pushed")
	benchReady := card.BenchCardsKeyAt(0, ghBench, "ready")
	wsReady := "ws:" + ghStream + ":ready"
	friendReady := "friend:emma:cards:ready"
	w.do(t,
		[]any{"SADD", "ws:names", ghStream},
		[]any{"SADD", "friends", "emma"},
		[]any{"ZADD", friendReady, 1, pipeShape},
		[]any{"ZADD", benchReady, 1, pipeShape},
		[]any{"ZADD", wsReady, 1, noRecord},
	)
	if !zHas(t, w.ctx, w.c, card.BenchCardsKeyAt(0, "_pool", "ready"), real) || !zHas(t, w.ctx, w.c, wsReady, real) {
		t.Fatalf("fixture: %s is not in its ready views", real)
	}

	mem, err := card.Members(w.ctx, w.c, false, "")
	if err != nil || mem.Bad != 3 || mem.Removed != 0 || mem.Clean() {
		t.Fatalf("read-only members walk = %+v, %v; want 3 bad, none removed", mem, err)
	}
	for _, want := range []string{
		"MEMBER-NOT-A-CARD " + benchReady + " " + pipeShape,
		"MEMBER-NOT-A-CARD " + friendReady + " " + pipeShape,
		"MEMBER-NOT-A-CARD " + wsReady + " " + noRecord,
	} {
		if !strings.Contains(strings.Join(mem.Lines, "\n"), want) {
			t.Fatalf("members walk lines %q lack %q", mem.Lines, want)
		}
	}

	walk, err := reconcile.FsckAll(w.ctx, w.c, w.lease.Token())
	if err != nil {
		t.Fatalf("fsck duty: %v", err)
	}
	// The card-shaped id names sprint s, so that sprint's repair (first in
	// the walk's pipeline) removes it; the member walk removes the other two.
	lines := strings.Join(walk.Lines, "\n")
	if walk.Fixed != 3 || walk.NotACard != 2 || !strings.Contains(lines, "MEMBER-NOT-A-CARD "+wsReady+" "+noRecord) ||
		!strings.Contains(lines, "MEMBER-NOT-A-CARD "+friendReady+" "+pipeShape) {
		t.Fatalf("fsck duty walk = %+v, want 3 members removed, each named MEMBER-NOT-A-CARD", walk)
	}
	for _, k := range []string{friendReady, benchReady, wsReady} {
		for _, m := range []string{pipeShape, noRecord} {
			if zHas(t, w.ctx, w.c, k, m) {
				t.Fatalf("%s still holds %q after the duty", k, m)
			}
		}
	}
	if !zHas(t, w.ctx, w.c, card.BenchCardsKeyAt(0, "_pool", "ready"), real) || !zHas(t, w.ctx, w.c, wsReady, real) {
		t.Fatalf("the duty removed the real card %s from a view", real)
	}
	receipts := 0
	for _, e := range w.c.XRange(w.ctx, "ws:log", "-", "+").Val() {
		if e.Values["why"] == "MEMBER-NOT-A-CARD" && e.Values["to"] == "" && e.Values["from"] == "ready" {
			receipts++
		}
	}
	if receipts != 3 {
		t.Fatalf("ws:log holds %d MEMBER-NOT-A-CARD receipts, want one per member removed (3)", receipts)
	}
	fsckClean(t, w, s, "after the duty")
	if again, err := reconcile.FsckAll(w.ctx, w.c, w.lease.Token()); err != nil || again.NotACard != 0 {
		t.Fatalf("second duty walk = %+v, %v; want nothing removed", again, err)
	}

	// The one move refuses both shapes: nothing is added back.
	for _, id := range []string{pipeShape, noRecord} {
		got, err := w.c.FCall(w.ctx, "ns_card_move", nil, id, "ready").Text()
		if err != nil || !strings.HasPrefix(got, "REFUSED ") {
			t.Fatalf("ns_card_move %q = %q, %v; want REFUSED", id, got, err)
		}
	}
	if mem, err := card.Members(w.ctx, w.c, false, ""); err != nil || mem.Bad != 0 {
		t.Fatalf("members walk after the refused moves = %+v, %v", mem, err)
	}
}
