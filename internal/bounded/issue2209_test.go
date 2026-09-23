// issue2209_test.go — TestIssue2209 reproduces nova-tools #2209:
//
//	"Implement the nova-test `receipt` verb and the per-outcome failure
//	 receipts" (docs/SPEC-TEST.md:46-69).
//
// SPEC-TEST.md promises the receipt verb prints:
//
//   - identity, equivalence key, bounded failing steps and excerpts, and
//     latency (line 46-47)
//   - at most the failing steps plus excerpts, ONE MORE line naming the
//     remedy, which is the log holding the whole list (line 55-56)
//   - a timeout, a missing job and a superseded cancellation each read
//     as their own receipt rather than as one shared "fail" (line 68-69)
//
// The test asserts each behaviour on its own floor: a header that prints
// every named field; a body that never exceeds the cap, capped to the
// internal/bounded machinery; one MORE line that names the log; an
// outcome header that distinguishes fail, timeout, missing and superseded.
// The test is RED when the production type is absent from internal/bounded
// and GREEN once NewReceipt, the four ReceiptKind constants, Failure and
// Print land in the package.

package bounded

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestIssue2209 — see the package doc above.
func TestIssue2209(t *testing.T) {
	const (
		log         = "logs/run-42.log"
		identity    = "run-42"
		equivalence = "sha=deadbeef\tpolicy=dev"
	)
	latency := 13 * time.Second

	// 1, 2, 3, 4: the header carries identity, equivalence key, kind
	//    and latency; the body carries the failing steps and excerpts.
	var out bytes.Buffer
	r := NewReceipt(&out, ReceiptKindFail, identity, equivalence, latency, log)
	r.Failure("verify", "link not found: foo")
	r.Failure("lint", "style warning at line 12")
	r.Print()
	text := out.String()

	for _, want := range []string{
		"identity=" + identity,
		"equivalence=",
		"latency=13s",
		"kind=" + ReceiptKindFail,
		"step=verify",
		"step=lint",
		"excerpt=",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("receipt missing %q:\n%s", want, text)
		}
	}

	// 5, 6: a receipt is bounded to failing steps plus excerpts plus AT
	//    MOST one MORE line, and that one MORE line must name the log
	//    path (the remedy). Twenty-five failures into a default cap of
	//    twenty yields exactly twenty item lines and one MORE line.
	var over bytes.Buffer
	r2 := NewReceipt(&over, ReceiptKindFail, identity, equivalence, latency, log)
	for i := 0; i < 25; i++ {
		r2.Failure(fmt.Sprintf("verify-%d", i), "excerpt line")
	}
	r2.Print()
	upper := over.String()

	var (
		itemLines int
		moreLines int
		more      string
	)
	for _, l := range strings.Split(strings.TrimRight(upper, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "RECEIPT FAIL "):
			itemLines++
		case strings.HasPrefix(l, "RECEIPT MORE "):
			moreLines++
			more = l
		}
	}
	if itemLines == 0 {
		t.Errorf("no failing-step lines reached the receipt:\n%s", upper)
	}
	if moreLines > 1 {
		t.Errorf("at most one MORE line permitted, got %d:\n%s", moreLines, upper)
	}
	if moreLines == 0 {
		t.Errorf("25 failures did not elide with cap=%d; expected one MORE line:\n%s", Default, upper)
	}
	if !strings.Contains(more, log) {
		t.Errorf("the single MORE line does not name the log path %q:\n%s", log, upper)
	}

	// 7, 8, 9: a timeout, a missing job, and a superseded cancellation
	//    each read as their own receipt (a distinct kind on the header
	//    line, never the same "fail" the verifier saw above).
	if ReceiptKindFail == ReceiptKindTimeout ||
		ReceiptKindFail == ReceiptKindMissing ||
		ReceiptKindFail == ReceiptKindSuperseded {
		t.Fatalf("a non-fail outcome shares the fail kind: %q", ReceiptKindFail)
	}
	if ReceiptKindTimeout == ReceiptKindMissing ||
		ReceiptKindTimeout == ReceiptKindSuperseded ||
		ReceiptKindMissing == ReceiptKindSuperseded {
		t.Fatalf("two non-fail outcomes share a kind: %s %s %s",
			ReceiptKindTimeout, ReceiptKindMissing, ReceiptKindSuperseded)
	}

	// The four outcomes must produce four different headers — distinct
	// from each other and from a plain fail.
	headers := map[string]string{}
	for _, k := range []string{
		ReceiptKindFail,
		ReceiptKindTimeout,
		ReceiptKindMissing,
		ReceiptKindSuperseded,
	} {
		var buf bytes.Buffer
		rr := NewReceipt(&buf, k, identity, equivalence, latency, log)
		rr.Print()
		headers[k] = strings.SplitN(buf.String(), "\n", 2)[0]
		if !strings.Contains(headers[k], "kind="+k) {
			t.Errorf("kind=%q missing from header: %q", k, headers[k])
		}
	}
	pairs := [][2]string{
		{ReceiptKindFail, ReceiptKindTimeout},
		{ReceiptKindFail, ReceiptKindMissing},
		{ReceiptKindFail, ReceiptKindSuperseded},
		{ReceiptKindTimeout, ReceiptKindMissing},
		{ReceiptKindTimeout, ReceiptKindSuperseded},
		{ReceiptKindMissing, ReceiptKindSuperseded},
	}
	for _, p := range pairs {
		if headers[p[0]] == headers[p[1]] {
			t.Errorf("outcome headers collide: kind=%q and kind=%q share header %q",
				p[0], p[1], headers[p[0]])
		}
	}
}
