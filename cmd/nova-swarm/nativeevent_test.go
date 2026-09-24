package main

// The card-end writer's tests (nova-tools #2563 item 1). Two halves:
//
//   - the CLASSIFICATION, which is the part a reader of the fold depends on: `ok` is earned
//     and `fail` is read off the report's own words, never inferred from an exit code alone.
//   - the CONTRACT, which is that none of it can fail a card. The store is a
//     events.FakeStream set to error on every entry, and the assertion is that emitCardEnd
//     returns, writes one line, and leaves the card's own files and verdict untouched.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// deadlineStore records the deadline of the context each Emit was handed, which is the only
// way to assert on a bound without asserting on a clock (AGENTS.md rules 3 and 4).
type deadlineStore struct {
	*events.FakeStream
	deadlines []time.Time
	hadNone   int
}

func (d *deadlineStore) Emit(ctx context.Context, e events.Event) (string, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.deadlines = append(d.deadlines, dl)
	} else {
		d.hadNone++
	}
	return d.FakeStream.Emit(ctx, e)
}

// jobWithResult writes one RESULT.md under a job directory and returns that directory.
func jobWithResult(t *testing.T, body string) string {
	t.Helper()
	job := filepath.Join(t.TempDir(), "jobs", "card-1")
	if err := os.MkdirAll(job, 0o750); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

// okRow is the usage row a card that ran and was billed leaves behind.
func okRow() swarm.UsageRow {
	return swarm.UsageRow{
		"job": "card-1", "attempt": "1", "provider": "opencode", "model": "deepseek-v4-flash",
		"tokens_in": "1200", "tokens_out": "340", "usd": "0.0731", "rc": "0", "end": "done",
	}
}

// TestCardEndEventIsOKOnlyWhenTheCardEarnedIt: the entry's kind is the NATIVE line's own
// verdict refined by the report. A run the line calls INCOMPLETE is never `ok`, a report
// whose verdict line opens BLOCKED/FAILED/FAIL/RED is `fail`, and a report the MACHINERY
// signed is `fail` however it opens.
func TestCardEndEventIsOKOnlyWhenTheCardEarnedIt(t *testing.T) {
	green := "RESULT card-1 sha=abc123 — the cell\nGREEN the assertion holds\nBRANCH rowan/card-1\n"
	for _, tc := range []struct {
		name    string
		verdict string
		result  string
		want    events.Kind
	}{
		{"a green report on an OK run is ok", "OK", green, events.OK},
		{"a RED verdict line is fail", "OK", "RESULT card-1 sha=abc — t\nRED FAIL bits(12) law: raw 5000 clamps\n", events.Fail},
		{"a FAILED verdict line is fail", "OK", "RESULT card-1 sha=abc — t\nFAILED the build never ran\n", events.Fail},
		{"a BLOCKED verdict line is fail", "OK", "RESULT card-1 sha=abc — t\nBLOCKED the wall refused /etc\n", events.Fail},
		{"a report the machinery wrote is fail", "OK",
			"RESULT: BLOCKED card-1\nWALL task=card-1 path=/x step=-\nblocked: still for 300s\nwritten-by: nova-swarm native (the card published no report of its own)\n",
			events.Fail},
		{"an INCOMPLETE run is fail whatever the report says", "INCOMPLETE", green, events.Fail},
		{"a run with no report at all is fail", "INCOMPLETE", "", events.Fail},
		{"a title that merely says red is not a verdict", "OK",
			"RESULT card-1 sha=abc — make the red bar green\nGREEN the assertion holds\n", events.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := jobWithResult(t, tc.result)
			e, ok := cardEndEvent(nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
				nativeRunResult{job: job, usage: okRow()}, tc.verdict, "hulk")
			if !ok {
				t.Fatal("no entry was built for a finished card")
			}
			if e.Kind != tc.want {
				t.Fatalf("kind is %q, want %q", e.Kind, tc.want)
			}
		})
	}
}

// TestCardEndEventCarriesTheUsageRow: the numbers in the stream are the numbers in
// usage.tsv, so the fold and the file can never disagree about one card's spend.
func TestCardEndEventCarriesTheUsageRow(t *testing.T) {
	job := jobWithResult(t, "RESULT card-1 sha=abc — t\nGREEN it holds\n")
	e, _ := cardEndEvent(nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")
	if e.TokensIn == nil || e.TokensOut == nil || *e.TokensIn != 1200 || *e.TokensOut != 340 {
		t.Errorf("tokens are %v/%v, want 1200/340", e.TokensIn, e.TokensOut)
	}
	if e.USD == nil || *e.USD != 0.0731 {
		t.Errorf("usd is %v, want 0.0731", e.USD)
	}
	if e.Model != "opencode/deepseek-v4-flash" {
		t.Errorf("model is %q", e.Model)
	}
	if e.Route != "opencode" {
		t.Errorf("route is %q, want the row's provider", e.Route)
	}
	if e.Attempt != 1 || e.Bench != "hulk" || e.Label != "card-1" {
		t.Errorf("the entry lost a field: %+v", e)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("the card-end entry does not pass the stream's own door: %v", err)
	}
}

// TestCardEndEventKeepsDashesOutOfTheStream: a fast failure whose provider reported nothing
// keeps dashes in the row, and the entry carries NO cost at all: an absent cost is not a
// zero cost, so a dash is nil here and no tokens_in, tokens_out or usd field is written.
func TestCardEndEventKeepsDashesOutOfTheStream(t *testing.T) {
	row := swarm.UsageRow{"job": "card-1", "attempt": "2", "provider": swarm.Dash, "model": swarm.Dash,
		"tokens_in": swarm.Dash, "tokens_out": swarm.Dash, "usd": swarm.Dash}
	job := jobWithResult(t, "")
	e, _ := cardEndEvent(nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: row}, "INCOMPLETE", "hulk")
	if e.TokensIn != nil || e.TokensOut != nil || e.USD != nil {
		t.Fatalf("a dashed row became %v/%v/%v, want all absent (nil), never 0", e.TokensIn, e.TokensOut, e.USD)
	}
	for _, name := range []string{"tokens_in", "tokens_out", "usd"} {
		if v, ok := e.Fields()[name]; ok {
			t.Errorf("a dashed row writes %s=%q; an unmeasured cost is no field", name, v)
		}
	}
	if e.Model != "opencode/deepseek-v4-flash" {
		t.Fatalf("a dashed row lost the model the run was launched with: %q", e.Model)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("a dashed row built an entry the door refuses: %v", err)
	}
}

// TestCardEndEmitCannotFailTheCard IS THE CONTRACT. The store errors on every entry; the
// call returns, one line lands on the run's stderr, the card's RESULT.md is untouched, and
// nothing about this function can change a caller's exit code -- it returns nothing.
func TestCardEndEmitCannotFailTheCard(t *testing.T) {
	body := "RESULT card-1 sha=abc — t\nGREEN it holds\n"
	job := jobWithResult(t, body)
	fake := events.NewFakeStream()
	fake.FailEmit = errors.New("READONLY You can't write against a read only replica")
	var stderr strings.Builder
	emitCardEnd(context.Background(), events.WriterOptions{
		Log: &stderr,
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return fake, nil },
	}, nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")

	if fake.Len() != 0 {
		t.Fatalf("a store that errors kept %d entries", fake.Len())
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "EVENT SKIPPED label=card-1 event=ok") {
		t.Fatalf("want exactly one EVENT SKIPPED line, got:\n%s", stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil || string(raw) != body {
		t.Fatalf("the card's own report did not survive the failed emit: %v %q", err, string(raw))
	}
}

// TestCardEndEmitIsSkippedWithoutAPassword: a bench that has not been given
// NOVA_REDIS_BENCH_PASSWORD runs cards and says nothing at all about the store. This is the
// state of most of the fleet until #2559's launcher hands the password over, and it must not
// print a line per card.
func TestCardEndEmitIsSkippedWithoutAPassword(t *testing.T) {
	job := jobWithResult(t, "RESULT card-1 sha=abc — t\nGREEN it holds\n")
	var stderr strings.Builder
	emitCardEnd(context.Background(), events.WriterOptions{
		Log:    &stderr,
		Lookup: func(string) string { return "" },
		Dial: func(context.Context, events.Dial) (events.Store, error) {
			t.Fatal("a bench with no password dialled the store")
			return nil, nil
		},
	}, nativeRunConfig{label: "card-1"}, nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")
	if stderr.String() != "" {
		t.Fatalf("a bench with no password wrote %q", stderr.String())
	}
}

// TestCardEndEmitWritesTheEntry is the positive control: without it every assertion above
// would pass on a writer that never emits anything at all.
func TestCardEndEmitWritesTheEntry(t *testing.T) {
	job := jobWithResult(t, "RESULT card-1 sha=abc — t\nGREEN it holds\n")
	fake := events.NewFakeStream()
	var stderr strings.Builder
	emitCardEnd(context.Background(), events.WriterOptions{
		Log: &stderr,
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return fake, nil },
	}, nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")
	if fake.Len() != 1 {
		t.Fatalf("the card end wrote %d entries, want 1 (stderr: %s)", fake.Len(), stderr.String())
	}
	got, err := fake.Range(context.Background(), "-", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Fields["event"] != string(events.OK) || got[0].Fields["usd"] != "0.0731" {
		t.Fatalf("the entry is %v", got[0].Fields)
	}
	if fake.StreamName() != "cards:done" {
		t.Fatalf("the card end wrote to %q, want cards:done", fake.StreamName())
	}
}

// TestCardEndEmitWritesTheCardsDoneKey locks in the KEY, not just the entry. cmdNative hands
// emitCardEnd WriterOptions with no Stream, so the key is whatever events.Open defaults to;
// this runs the real Redis store (Open, XADD) against miniredis and asserts the entry is on
// `cards:done` and that `ev:cards` was never created (Johnny's HOLD on #2619).
func TestCardEndEmitWritesTheCardsDoneKey(t *testing.T) {
	mr := miniredis.RunT(t)
	job := jobWithResult(t, "RESULT card-1 sha=abc — t\nGREEN it holds\n")
	var stderr strings.Builder
	emitCardEnd(context.Background(), events.WriterOptions{
		Addr: mr.Addr(), Log: &stderr,
		Lookup: func(n string) string { return map[string]string{events.DefaultPasswordEnv: "unused"}[n] },
		Dial: func(ctx context.Context, d events.Dial) (events.Store, error) {
			d.Username, d.Password = "", "" // miniredis runs without an ACL
			return events.Open(ctx, d)
		},
	}, nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")
	if stderr.String() != "" {
		t.Fatalf("the emit said: %s", stderr.String())
	}
	if !mr.Exists("cards:done") {
		t.Fatalf("no cards:done key after the card end; keys are %v", mr.Keys())
	}
	if mr.Exists("ev:cards") {
		t.Fatal("the card end created ev:cards; there is one stream and it is cards:done")
	}
	entries, err := mr.Stream("cards:done")
	if err != nil || len(entries) != 1 {
		t.Fatalf("cards:done holds %d entries (%v), want 1", len(entries), err)
	}
}

// TestCardEndEmitBoundsTheWriteAndNotOnlyTheDial: `WriterOptions.Timeout` bounds the
// CONNECTION. An XADD to a store that accepted the connection and then stopped answering
// would wait forever on an unbounded context -- and it would wait HOLDING THIS CARD'S BENCH
// SLOT LEASE, on a card that has already finished and already printed its receipt. Two of
// those and the bench is a seat short for the rest of the sprint.
func TestCardEndEmitBoundsTheWriteAndNotOnlyTheDial(t *testing.T) {
	store := &deadlineStore{FakeStream: events.NewFakeStream()}
	job := jobWithResult(t, "RESULT card-1 sha=abc — t\nGREEN it holds\n")
	emitCardEnd(context.Background(), events.WriterOptions{
		Lookup: func(n string) string {
			return map[string]string{events.DefaultPasswordEnv: "secret", events.AddrEnv: "store.invalid:6380"}[n]
		},
		Dial: func(context.Context, events.Dial) (events.Store, error) { return store, nil },
	}, nativeRunConfig{label: "card-1", model: "opencode/deepseek-v4-flash"},
		nativeRunResult{job: job, usage: okRow()}, "OK", "hulk")

	if store.hadNone > 0 {
		t.Fatalf("the card-end entry was written under a context with NO deadline; a hung store would hold the slot lease")
	}
	if len(store.deadlines) != 1 {
		t.Fatalf("recorded %d deadlines, want one for the one entry", len(store.deadlines))
	}
	if !store.deadlines[0].After(time.Now()) {
		t.Fatal("the entry was written under a deadline already in the past")
	}
}
