/*
Receipt — the compact failure receipt for one nova-test run. SPEC-TEST.md:46-69
names four outcomes — a generic failure, a timeout, a missing-job, and a
superseded cancellation — but each is the same shape: identity, equivalence
key, bounded failing steps and excerpts, latency, and at most one MORE line
naming the log that holds the whole list.

The cap, the count, and the escape are the three things a receipt cannot
get wrong without becoming the state it was meant to be a window into, and
those are exactly the three jobs the rest of this package already does.
Defining Receipt here, beside List and Group, means the verb in cmd/nova-test
will reuse the same machinery its listings already use, and a reader who
parses one RECEIPT line parses every RECEIPT line.

A receipt printed with zero failing steps — the run never reached one
because it timed out, the job never existed, or it was superseded — is one
header line plus zero or one MORE line: the same bound as a receipt with a
hundred failures. The bound is "at most the failing steps, plus excerpts,
plus a single remedy line", and it is checked on every receipt whether the
list was filled or never started.
*/
package bounded

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Receipt kinds. SPEC-TEST.md:68-69 names the three non-fail outcomes:
// a timeout, a missing job and a superseded cancellation each read as
// their own receipt rather than as one shared "fail". These strings are
// what appears after kind= in the receipt header, and they are what the
// "failures" verb greps for when a reader wants to count, say, timeouts
// across a week of runs without dragging the failed runs along.
const (
	ReceiptKindFail       = "fail"
	ReceiptKindTimeout    = "timeout"
	ReceiptKindMissing    = "missing"
	ReceiptKindSuperseded = "superseded"
)

// Receipt is the compact failure receipt for one nova-test run. Build one
// with NewReceipt; never copy a zero value and print it — a receipt that
// says nothing is what this package exists to refuse.
//
// The failing-step list is capped by the same Default this package uses
// for listings, and an over-cap run leaves a single MORE line naming the
// log that holds the un-bounded list. The reader types that path the same
// way the verb types it: an improvement on redisplaying a thousand-step
// failure into a single screen-sized receipt.
type Receipt struct {
	w io.Writer

	kind        string
	identity    string
	equivalence string
	latency     time.Duration
	log         string // path to the log holding the un-bounded list — the remedy

	// items holds the rendered failing-step lines until Print, so the
	// header is always the first line of the receipt whatever order the
	// caller offers Failure and Print in.
	items   bytes.Buffer
	failing *List
}

// NewReceipt builds a receipt printing to w. kind names the outcome and is
// one of ReceiptKindFail, ReceiptKindTimeout, ReceiptKindMissing or
// ReceiptKindSuperseded: those four names are how a timeout, a missing
// job, and a superseded cancellation read as their own receipt rather
// than as a shared "fail" header.
//
// identity, equivalence, and latency are the fields the run record
// carried; the verb passes them through oneline.Field so a stored
// identity, an equality-sign in the equivalence key, or a path holding
// whitespace does not pose as a token.
//
// log is the path to the file holding the un-bounded list and is the
// remedy line the spec demands. It is the way a reader would type it
// from the receipt:
//
//	nova-test receipt <identity> --log <log>
func NewReceipt(w io.Writer, kind, identity, equivalence string, latency time.Duration, log string) *Receipt {
	r := &Receipt{
		w:           w,
		kind:        kind,
		identity:    identity,
		equivalence: equivalence,
		latency:     latency,
		log:         log,
	}
	r.failing = Capped(&r.items, Default, "RECEIPT", "fail", "see "+log)
	return r
}

// Failure offers one failing step and its source-backed excerpt to the
// receipt's bounded list. Each call counts toward the cap, and the cap
// is why a timeout or a missing-job receipt without an item still prints
// a header rather than the whole list. The pair renders as one line,
// so an excerpt cannot add an item line the cap did not account for.
func (r *Receipt) Failure(step, excerpt string) {
	r.failing.Line(fmt.Sprintf("RECEIPT FAIL step=%s excerpt=%s",
		oneline.Field(step), oneline.Field(excerpt)))
}

// Print writes the receipt: a single header line carrying the identity,
// the kind, the equivalence key, and the latency; then the failing
// steps with their excerpts (buffered through r.failing.Line as Failure
// was called, and written only now, after the header); and at most one
// MORE line naming the log that
// holds the un-bounded list. The MORE line is written only when the
// call sequence elided at least one failure; an empty receipt prints
// just the header and nothing else.
func (r *Receipt) Print() {
	fmt.Fprintf(r.w, "RECEIPT identity=%s kind=%s equivalence=%s latency=%s\n",
		oneline.Field(r.identity),
		oneline.Field(r.kind),
		oneline.Field(r.equivalence),
		formatLatency(r.latency))
	r.failing.More()
	_, _ = r.items.WriteTo(r.w)
}

// formatLatency prints a duration in a form that survives the line
// escape: a "1m30s" holds no whitespace, no newline, and no equality-
// sign. Zero or negative latency prints as "0s": a future-looking
// ticket that did not record its end time should still produce a
// parseable RECEIPT line, not a silently dropped field.
func formatLatency(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.String()
}
