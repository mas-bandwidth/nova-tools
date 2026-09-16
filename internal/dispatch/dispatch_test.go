package dispatch

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The dispatch discipline is one file: an item moves queued -> dispatching ->
// (delivered | uncertain), an uncertain item blocks the queue and is a
// person's to resolve, and an interrupted dispatching is recovered to
// uncertain. This package is that discipline lifted out of nova-wake serve so
// nova-chat takes it rather than re-spelling it (SPEC-CHAT work-list item 13).
func TestDispatchLedgerStatesAndTransitions(t *testing.T) {
	st, err := wake.Load(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	l := New(st, "serve:")

	// A note addressed To is queued, a Cc is recorded and never a turn.
	l.Queue("aaa111", "2026-09-11T11:00:00Z")
	l.CC("ccc333", "2026-09-11T11:02:00Z")
	if got := l.StateOf("aaa111"); got != "queued" {
		t.Errorf("StateOf(aaa111) = %q, want queued", got)
	}
	if got := l.StateOf("ccc333"); got != "cc" {
		t.Errorf("StateOf(ccc333) = %q, want cc", got)
	}

	// Dispatching is written before the spawn, delivered after it returned 0.
	l.MarkDispatching("aaa111", "2026-09-11T11:00:05Z", 1)
	if state, stamp, attempt, rc := l.Record("aaa111"); state != "dispatching" || stamp != "2026-09-11T11:00:05Z" || attempt != 1 || rc != 0 {
		t.Errorf("Record(aaa111) = %q %q attempt=%d rc=%d, want dispatching 2026-09-11T11:00:05Z attempt=1 rc=0", state, stamp, attempt, rc)
	}
	l.MarkDelivered("aaa111", "2026-09-11T11:00:10Z", 0, false)
	if got := l.StateOf("aaa111"); got != "delivered" {
		t.Errorf("StateOf(aaa111) = %q, want delivered", got)
	}

	// A command that exited non-zero is uncertain, not a silent loss, and it
	// blocks the queue until a person resolves it.
	l.Queue("bbb222", "2026-09-11T11:01:00Z")
	l.MarkDispatching("bbb222", "2026-09-11T11:01:05Z", 1)
	l.MarkUncertain("bbb222", "2026-09-11T11:01:10Z", 1, 7)
	if state, _, attempt, rc := l.Record("bbb222"); state != "uncertain" || attempt != 1 || rc != 7 {
		t.Errorf("Record(bbb222) = %q attempt=%d rc=%d, want uncertain attempt=1 rc=7", state, attempt, rc)
	}

	// An interrupted dispatching found on restart is recovered to uncertain,
	// keeping the attempt.
	l.Queue("ddd444", "2026-09-11T11:03:00Z")
	l.MarkDispatching("ddd444", "2026-09-11T11:03:05Z", 2)
	l.Recover("ddd444", "2026-09-11T11:04:00Z", 2)
	if state, _, attempt, _ := l.Record("ddd444"); state != "uncertain" || attempt != 2 {
		t.Errorf("Record(ddd444) = %q attempt=%d, want uncertain attempt=2", state, attempt)
	}

	// IDs reports every id with a record, in key (sorted) order.
	if got := l.IDs(); len(got) != 4 {
		t.Fatalf("IDs() = %v, want the four ids", got)
	}
}
