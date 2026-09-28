// Package dispatch is the queued|dispatching|delivered|uncertain file
// discipline, lifted out of nova-wake serve's own bookkeeping so that
// nova-chat takes it rather than re-spelling it (SPEC-CHAT work-list item 13).
//
// One item is one row in a state file, keyed prefix+"<id>", holding the
// composed fields "<state>|<stamp>|attempt=<n>|rc=<n>". The states are:
//
//	queued      the item is waiting to be dispatched
//	dispatching the item is in flight; a kill before the outcome is uncertain
//	delivered   the receiver accepted it (exit 0)
//	uncertain   the outcome is unknown and a person's to resolve
//	cc          recorded for the line's attention, never a dispatch
//
// An uncertain item blocks the queue: nothing behind it is dispatched until a
// person resolves it. This file only moves an item between states; the caller
// owns the spawn, the print and the remedy, which differ between a bus note
// and a Discord post.
package dispatch

import (
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The five states, spelled once so a reader needs one file.
const (
	Queued      = "queued"
	Dispatching = "dispatching"
	Delivered   = "delivered"
	Uncertain   = "uncertain"
	CC          = "cc"
)

// Ledger is a keyed dispatch ledger over a wake.State. Each item is stored at
// prefix+"<id>", and every write is made through wake.Compose so the fields
// round-trip byte for byte.
type Ledger struct {
	st     *wake.State
	prefix string
}

// New returns a Ledger over st, keying each item under prefix.
func New(st *wake.State, prefix string) *Ledger {
	return &Ledger{st: st, prefix: prefix}
}

func (l *Ledger) key(id string) string { return l.prefix + id }

// Has reports whether id has a record.
func (l *Ledger) Has(id string) bool {
	_, ok := l.st.Get(l.key(id))
	return ok
}

// IDs returns every id with a record, in key (sorted) order.
func (l *Ledger) IDs() []string {
	var out []string
	for _, k := range l.st.Keys() {
		if id, ok := strings.CutPrefix(k, l.prefix); ok {
			out = append(out, id)
		}
	}
	return out
}

// Record returns the state, stamp, attempt and rc stored for id. A missing
// record is state "" and attempt forced to 1, matching a first attempt.
func (l *Ledger) Record(id string) (state, stamp string, attempt, rc int) {
	raw, ok := l.st.Get(l.key(id))
	if !ok {
		return "", "", 0, 0
	}
	p := wake.Decompose(raw)
	state = p[0]
	if len(p) > 1 {
		stamp = p[1]
	}
	for _, f := range p[2:] {
		if v, ok := strings.CutPrefix(f, "attempt="); ok {
			attempt, _ = strconv.Atoi(v)
		}
		if v, ok := strings.CutPrefix(f, "rc="); ok {
			rc, _ = strconv.Atoi(v)
		}
	}
	if attempt == 0 {
		attempt = 1
	}
	return state, stamp, attempt, rc
}

// StateOf returns the stored state for id.
func (l *Ledger) StateOf(id string) string {
	state, _, _, _ := l.Record(id)
	return state
}

// Queue records id at queued. Use Queue for an item that must be acted on, CC
// for one that should be known.
func (l *Ledger) Queue(id, stamp string) {
	l.st.Set(l.key(id), wake.Compose(Queued, stamp))
}

// CC records id at cc: counted, never a turn.
func (l *Ledger) CC(id, stamp string) {
	l.st.Set(l.key(id), wake.Compose(CC, stamp))
}

// MarkDispatching writes dispatching with the attempt, before the spawn.
func (l *Ledger) MarkDispatching(id, stamp string, attempt int) {
	l.st.Set(l.key(id), wake.Compose(Dispatching, stamp, "attempt="+strconv.Itoa(attempt)))
}

// MarkDelivered writes delivered with the exit code, after the receiver
// returned 0. redelivered marks a person's retry (0 for a first dispatch).
func (l *Ledger) MarkDelivered(id, done string, rc int, redelivered bool) {
	mark := "0"
	if redelivered {
		mark = "1"
	}
	l.st.Set(l.key(id), wake.Compose(Delivered, done, "rc="+strconv.Itoa(rc), "redelivered="+mark))
}

// MarkUncertain writes uncertain with the attempt and exit code, when the
// receiver did not accept the item. It stays unresolved until a person acts.
func (l *Ledger) MarkUncertain(id, done string, attempt, rc int) {
	l.st.Set(l.key(id), wake.Compose(Uncertain, done, "attempt="+strconv.Itoa(attempt), "rc="+strconv.Itoa(rc)))
}

// Recover rewrites an interrupted dispatching as uncertain, keeping the
// attempt. It is called at start when a dispatching record is found.
func (l *Ledger) Recover(id, stamp string, attempt int) {
	l.st.Set(l.key(id), wake.Compose(Uncertain, stamp, "attempt="+strconv.Itoa(attempt)))
}
