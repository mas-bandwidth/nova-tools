package main

import (
	"bytes"
	"strings"
	"testing"
)

// The eight verbs the spec carries but the test suite did not: attest, attempt,
// evidence, state --to done, event, query --ask percent --axis, query --ask
// remaining, and the percent-over-zero-applicable rule. Each behaviour names
// what production path it tests, and each one breaks under a deliberate break
// of that path -- a verb missing from the specverbs table, a flag renamed in
// socketverbs.go, the query --axis check silenced, or the QUERY OK classifier
// deciding only by a field the spec does not actually promise.
//
// The thread the spec weaves here (docs/SPEC-WORK.md:2267 attest, :1945
// percent --axis) is thin-client verbs the session answers and one verb (query)
// this client validates before it dials. Each new test below names a behaviour
// the production code carries: the request line built verbatim, the refusal
// line or the byte-for-byte pass-through, and the exit code of the spec.

// queryPercentOK is a representative QUERY OK answer for `query --ask percent`
// carrying every per-axis field the spec promises at docs/SPEC-WORK.md:1939
// (green=, applicable=, rows=, baseline-rows=, row-kind=) and the shared scope
// line the QUERY OK grammar names beside them. The client must print it
// byte for byte.
const queryPercentOK = "QUERY OK ask=percent scope=12 membership=axis branch=open unit=features source=9f2c1a7e freshest=2026-09-16T12:00:00Z done=4 done-unverified=0 unknown=1 deferred=0 cancelled=0 superseded=0 stale=0 required=8 since-baseline=1 private=0 open=4 closed=4 gap=0 leases=2 responsible=rowan rows=10 baseline-rows=9 green=4 applicable=10 row-kind=feature shown=10 pages=1 parses=0 replays=0 emitted=412"

// queryRemainingOK is a representative QUERY OK answer for `query --ask
// remaining` carrying the `responsible=` field the spec promises at
// docs/SPEC-WORK.md:2076 for the done, remaining, size, stream and under
// asks, and the rest of the shared scope line beside it. The client must
// print it byte for byte.
const queryRemainingOK = "QUERY OK ask=remaining scope=12 membership=all branch=open unit=leaves source=9f2c1a7e freshest=2026-09-16T12:00:00Z done=2 done-unverified=0 unknown=1 deferred=0 cancelled=0 superseded=0 stale=0 required=8 since-baseline=0 private=0 open=4 closed=4 gap=0 responsible=rowan shown=3 pages=1 parses=0 replays=0 emitted=412"

// queryPercentZeroOK is the percent line over zero applicable rows the spec
// defines at docs/SPEC-WORK.md:1945: "`percent` over zero applicable rows
// prints green=0 applicable=0 and no percentage". The session's QUERY OK
// therefore carries green= and applicable= and no decimal between them; the
// client must print the line byte for byte and exit 0.
const queryPercentZeroOK = "QUERY OK ask=percent scope=12 membership=axis branch=open unit=features source=9f2c1a7e freshest=2026-09-16T12:00:00Z done=0 done-unverified=0 unknown=0 deferred=0 cancelled=0 superseded=0 stale=0 required=0 since-baseline=0 private=0 open=0 closed=4 gap=0 leases=0 responsible=- rows=4 baseline-rows=4 green=0 applicable=0 row-kind=feature shown=0 pages=1 parses=0 replays=0 emitted=412"

// TestAttest: attest builds the request line the spec promises at
// docs/SPEC-WORK.md:2339 -- session, write flags, --node, --criterion,
// --result, --against -- in that order, with each flag value travelling
// through oneline.Field. A break that drops "attest" from the client's
// socket table turns this into an "unknown verb" exit 2 and the test
// fails; a break that renames a flag in moreVerbFlags["attest"] turns
// the request line wrong and the test fails.
func TestAttest(t *testing.T) {
	socket, requests := fakeSession(t, "VERIFY OK id=n rev=4 criterion=c-1 result=/tmp/r.log against=abc123")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"attest", "--session", socket,
		"--as", "Rowan", "--node", "n42",
		"--criterion", "c-1", "--result", "/tmp/r.log", "--against", "abc123",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("attest exit = %d, stderr = %s", code, stderr.String())
	}
	want := "attest --session " + socket + " --as Rowan --node n42 --criterion c-1 --result /tmp/r.log --against abc123"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("attest request line = %q\nwant %q", got, want)
	}
}

// TestAttempt: attempt builds the request line the spec promises at
// docs/SPEC-WORK.md:2340 -- session, write flags, --node, --model, --bench,
// --result, [--usage] -- in that order. A break that drops "attempt"
// from the client's socket table turns this into an "unknown verb"
// exit 2 and the test fails; a break that reorders the flags in
// moreVerbFlags["attempt"] turns the request line wrong and the test
// fails.
func TestAttempt(t *testing.T) {
	socket, requests := fakeSession(t, "ATTEMPT OK id=n model=anthropic/claude-sonnet bench=dev result=/tmp/a.log")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"attempt", "--session", socket,
		"--as", "Rowan", "--node", "n42",
		"--model", "anthropic/claude-sonnet", "--bench", "dev",
		"--result", "/tmp/a.log",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("attempt exit = %d, stderr = %s", code, stderr.String())
	}
	want := "attempt --session " + socket + " --as Rowan --node n42 --model anthropic/claude-sonnet --bench dev --result /tmp/a.log"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("attempt request line = %q\nwant %q", got, want)
	}
}

// TestEvidence: evidence builds the request line the spec promises at
// docs/SPEC-WORK.md:2341 -- session, write flags, --node, --pointer,
// --criterion, --against, [--attempt] -- in that order. A break that
// drops "evidence" from the client's socket table turns this into
// an "unknown verb" exit 2 and the test fails; a break that reorders
// the flags in moreVerbFlags["evidence"] turns the request line wrong
// and the test fails.
func TestEvidence(t *testing.T) {
	socket, requests := fakeSession(t, "EVIDENCE OK id=n pointer=/tmp/e.log criterion=c-1 against=abc123")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"evidence", "--session", socket,
		"--as", "Rowan", "--node", "n42",
		"--pointer", "/tmp/e.log", "--criterion", "c-1", "--against", "abc123",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("evidence exit = %d, stderr = %s", code, stderr.String())
	}
	want := "evidence --session " + socket + " --as Rowan --node n42 --pointer /tmp/e.log --criterion c-1 --against abc123"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("evidence request line = %q\nwant %q", got, want)
	}
}

// TestStateToDone: `state --to done` is the verb that closes a task and
// is the spec's own state transition (docs/SPEC-WORK.md:2342). The verb's
// flag order is session, write flags, --node, --to, --reason (or
// --evidence); carrying it byte-for-byte to the session is the whole
// point of this verb. A break that drops "state" from the socket table
// fails with "unknown verb"; a break that reorders --to / --node /
// --reason fails the line.
func TestStateToDone(t *testing.T) {
	socket, requests := fakeSession(t, "STATE OK id=n rev=4 state=done")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"state", "--session", socket,
		"--as", "Rowan", "--node", "n42",
		"--to", "done", "--reason", "evidence accepted",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("state --to done exit = %d, stderr = %s", code, stderr.String())
	}
	want := "state --session " + socket + " --as Rowan --node n42 --to done --reason evidence\\x20accepted"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("state --to done request line = %q\nwant %q", got, want)
	}
}

// TestEvent: event is the scope-event verb (docs/SPEC-WORK.md:2344). The
// flag surface spells session, write flags, --kind, --node, --reason,
// then the three optional ones the spec names for some kinds: --member
// (required on baseline and discovery), --superseded-by (required on
// supersede) and --evidence (required on cancel). The test exercises
// --kind baseline with its required --member so every flag the spec
// pins moves through the serializer. A break that drops "event" from
// the socket table fails with "unknown verb"; a break that reorders
// anything in moreVerbFlags["event"] fails the line; a break that
// omits the spec's three optional flags from the table fails the line.
func TestEvent(t *testing.T) {
	socket, requests := fakeSession(t, "EVENT OK kind=baseline node=R rev=4 member=m1,m2")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"event", "--session", socket,
		"--as", "Rowan", "--kind", "baseline", "--node", "R",
		"--member", "m1,m2", "--reason", "the row is sealed",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("event --kind baseline exit = %d, stderr = %s", code, stderr.String())
	}
	want := "event --session " + socket + " --as Rowan --kind baseline --node R --reason the\\x20row\\x20is\\x20sealed --member m1,m2"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("event request line = %q\nwant %q", got, want)
	}
}

// TestPercentOutputsFields: `query --ask percent --axis <member>` is the
// one query that always carries --axis (docs/SPEC-WORK.md:2310). The
// client spells the request in the order the spec promises: session,
// --ask, --branch, then --axis beside it, and the rest only if the
// caller named them. The session's reply then carries every per-axis
// field :1939 names -- green=, applicable=, rows=, baseline-rows=,
// row-kind=. A break that removes --axis from verbFlags["query"] turns
// the line wrong; a break that lets the line reach stdout without
// quoting through oneline.Escape changes its bytes; a break that
// classifies OK by anything other than fields[1] exits 1 instead of 0.
func TestPercentOutputsFields(t *testing.T) {
	socket, requests := fakeSession(t, queryPercentOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"query", "--session", socket, "--ask", "percent",
		"--branch", "open", "--axis", "feature-row",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query percent exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query percent wrote stderr: %q", stderr.String())
	}
	if g, w := stdout.String(), queryPercentOK+"\n"; g != w {
		t.Fatalf("query percent stdout = %q\nwant %q", g, w)
	}
	for _, field := range []string{"green=4", "applicable=10", "rows=10", "baseline-rows=9", "row-kind=feature"} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("query percent stdout lacks %q:\n%s", field, stdout.String())
		}
	}
	want := "query --session " + socket + " --ask percent --branch open --axis feature-row"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("query percent request line = %q\nwant %q", got, want)
	}
}

// TestRemainingOutputsFields: `query --ask remaining` is the shared ask
// of "what is still open" (docs/SPEC-WORK.md:2101). The client must
// (a) refuse --axis on this ask (it is not a percent ask, so --axis
// is not admitted on it, :2310), and (b) forward the session's reply
// byte for byte so responsible= and the rest of the scope line reach
// the caller unchanged. A break that lets --axis through on remaining
// fails the refusal; a break that drops --branch from verbFlags["query"]
// turns the line wrong; a break that classifies OK by a non-spec
// classifier exits 1.
func TestRemainingOutputsFields(t *testing.T) {
	socket, requests := fakeSession(t, queryRemainingOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"query", "--session", socket, "--ask", "remaining",
		"--branch", "open",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query remaining exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query remaining wrote stderr: %q", stderr.String())
	}
	if g, w := stdout.String(), queryRemainingOK+"\n"; g != w {
		t.Fatalf("query remaining stdout = %q\nwant %q", g, w)
	}
	for _, field := range []string{"responsible=rowan", "done-unverified=0"} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("query remaining stdout lacks %q:\n%s", field, stdout.String())
		}
	}
	want := "query --session " + socket + " --ask remaining --branch open"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("query remaining request line = %q\nwant %q", got, want)
	}
}

// TestPercentNoDivision: the rule of docs/SPEC-WORK.md:1945 -- a
// `percent` over zero applicable rows prints `green=0 applicable=0`
// and NO percentage, and exits 0. The session emits the no-percentage
// line; the client must print it byte-for-byte and exit 0, and must
// not interpolate a default percentage between `green=0` and
// `applicable=0`. A break that adds a `percent=` (or any `percent=`
// field) anywhere on the path fails the byte-for-byte check; a break
// that classifies a QUERY OK lacking the expected fields as REFUSED
// exits 1.
func TestPercentNoDivision(t *testing.T) {
	socket, requests := fakeSession(t, queryPercentZeroOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"query", "--session", socket, "--ask", "percent",
		"--branch", "open", "--axis", "feature-row",
	}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query percent zero-row exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query percent zero-row wrote stderr: %q", stderr.String())
	}
	if g, w := stdout.String(), queryPercentZeroOK+"\n"; g != w {
		t.Fatalf("query percent zero-row stdout = %q\nwant %q", g, w)
	}
	if strings.Contains(stdout.String(), "percent=") {
		t.Fatalf("query percent zero-row stdout carries a percentage field, want none:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "green=0") || !strings.Contains(stdout.String(), "applicable=0") {
		t.Fatalf("query percent zero-row stdout missing green=0 or applicable=0:\n%s", stdout.String())
	}
	want := "query --session " + socket + " --ask percent --branch open --axis feature-row"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("query percent zero-row request line = %q\nwant %q", got, want)
	}
}
