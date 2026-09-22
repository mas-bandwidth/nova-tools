package events

import (
	"context"
	"strings"
	"testing"
	"time"
)

// at is the one stamp these tests use; a fixed clock, because a test that reads the wall
// clock is a test that asserts the machine (AGENTS.md rule 3).
var at = time.Date(2026, 9, 22, 14, 5, 0, 0, time.UTC)

func okEvent() Event {
	return Event{
		Label: "card-42", Attempt: 1, Bench: "studio", Model: "fable", Route: "studio",
		Kind: OK, TokensIn: 12000, TokensOut: 900, USD: 0.11, PR: "2563",
		Head: "5f544272a1b0", At: at,
	}
}

// The stream carries ids and counts and NOTHING else (Johnny, section 8: the diff, the
// test, the prompt and the transcript stay in git). That is a door this package closes, not
// a convention a writer is trusted to keep, so each shape below is refused BY NAME.
func TestValidateRefusesWhatMustNeverReachRedis(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		event Event
		want  string
	}{
		{"no label", func() Event { e := okEvent(); e.Label = ""; return e }(), "label is required"},
		{"unknown kind", func() Event { e := okEvent(); e.Kind = "done"; return e }(), "not one of"},
		{"a payload in a field", func() Event { e := okEvent(); e.Head = strings.Repeat("d", maxFieldBytes+1); return e }(), "ids and counts only"},
		{"a diff in a field", func() Event { e := okEvent(); e.Model = "--- a/x\n+++ b/x"; return e }(), "control character"},
		{"a negative count", func() Event { e := okEvent(); e.TokensIn = -1; return e }(), "are counts"},
		{"a negative price", func() Event { e := okEvent(); e.USD = -0.5; return e }(), "usd is a price"},
		{"a negative attempt", func() Event { e := okEvent(); e.Attempt = -2; return e }(), "attempt is a count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.event.Validate()
			if err == nil {
				t.Fatalf("%s was accepted; the stream must refuse it at the door", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal = %q, want it to name %q", err.Error(), tc.want)
			}
		})
	}
}

// The wire names are #2563's names, so a bash writer and this package agree, and a round
// trip through the field map is the identity.
func TestFieldsAreTheIssuesNamesAndRoundTrip(t *testing.T) {
	t.Parallel()

	fields := okEvent().Fields()
	for _, name := range []string{"label", "attempt", "bench", "model", "route", "event",
		"tokens_in", "tokens_out", "usd", "pr", "head", "at"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("the entry has no %q field; #2563 names it", name)
		}
	}
	if got := fields["event"]; got != "ok" {
		t.Errorf("the kind is written to the %q field as %q, want %q", "event", got, "ok")
	}
	back, err := FromFields(fields)
	if err != nil {
		t.Fatalf("reading the entry back: %v", err)
	}
	if back != okEvent() {
		t.Fatalf("round trip = %+v, want %+v", back, okEvent())
	}
	if got := back.Day(); got != "2026-09-22" {
		t.Errorf("day = %q, want the UTC date of `at`", got)
	}
}

// A writer that omits a number it does not know is writing the truth, and a number that is
// present and unreadable is a writer with a bug. The two are not the same answer.
func TestMissingNumbersAreZeroAndBadNumbersAreRefused(t *testing.T) {
	t.Parallel()

	e, err := FromFields(map[string]string{"label": "card-1", "event": "queued"})
	if err != nil {
		t.Fatalf("a queued entry with no numbers: %v", err)
	}
	if e.TokensIn != 0 || e.USD != 0 || !e.At.IsZero() {
		t.Fatalf("missing fields = %+v, want zeroes", e)
	}
	if _, err := FromFields(map[string]string{"label": "card-1", "event": "ok", "usd": "eleven"}); err == nil {
		t.Fatal("usd=eleven was accepted; an unreadable number is a writer with a bug")
	}
	if _, err := FromFields(map[string]string{"label": "card-1", "event": "ok", "at": "yesterday"}); err == nil {
		t.Fatal("at=yesterday was accepted; the stamp is RFC3339 or nothing")
	}
}

// AGENTS.md: a fake is STRICT LIKE THE REAL TOOL. The fake refuses an event the real store
// refuses, and refuses a read under a group nobody created, with Redis's own NOGROUP word.
func TestFakeIsStrictLikeTheRealStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := NewFakeStream()
	f.Now = func() time.Time { return at }

	if _, err := f.Emit(ctx, Event{Label: "card-1", Kind: "done"}); err == nil {
		t.Fatal("the fake accepted an unknown kind; a lenient fake ships the real thing broken")
	}
	if _, err := f.Next(ctx, "fold", "c1", 10, 0); err == nil || !strings.Contains(err.Error(), "NOGROUP") {
		t.Fatalf("reading an uncreated group = %v, want a NOGROUP refusal", err)
	}
	if err := f.EnsureGroup(ctx, "fold"); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureGroup(ctx, "fold"); err != nil {
		t.Fatalf("a second EnsureGroup = %v, want nil: every fold start runs it", err)
	}
	if _, err := f.Emit(ctx, okEvent()); err != nil {
		t.Fatal(err)
	}
	got, err := f.Next(ctx, "fold", "c1", 10, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("Next = %d entries, %v; want 1, nil", len(got), err)
	}
	if f.PendingCount("fold") != 1 {
		t.Fatalf("pending = %d, want 1: a delivered entry is owed an ack", f.PendingCount("fold"))
	}
	if err := f.Ack(ctx, "fold", got[0].ID); err != nil {
		t.Fatal(err)
	}
	if f.PendingCount("fold") != 0 {
		t.Fatalf("pending after ack = %d, want 0", f.PendingCount("fold"))
	}
}

// An unstamped event is stamped once, by the emitter, and a stamped one is never restamped:
// otherwise a replay would rewrite the day column and the per-day view would drift.
func TestStampHappensOnceAtTheDoor(t *testing.T) {
	t.Parallel()

	stamped := Event{Label: "card-1", Kind: Queued}.Stamp(at)
	if !stamped.At.Equal(at) {
		t.Fatalf("at = %s, want the emitter's clock %s", stamped.At, at)
	}
	later := at.Add(time.Hour)
	again := stamped.Stamp(later)
	if !again.At.Equal(at) {
		t.Fatalf("a second stamp moved `at` to %s; a replay must not restamp", again.At)
	}
}
