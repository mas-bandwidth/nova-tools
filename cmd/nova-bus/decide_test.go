package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeDecider is the fake provider behind --decide: it records the state and the
// typed questions the command built and answers with the choice the test names.
type fakeDecider struct {
	state   string
	qs      map[string]decide.Question
	answer  string
	conf    float64
	calls   int
	states  []string
	failErr error
}

func (f *fakeDecider) Decide(_ context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.calls++
	f.state = state
	f.states = append(f.states, state)
	f.qs = qs
	if f.failErr != nil {
		return nil, decide.Usage{}, f.failErr
	}
	return map[string]decide.Answer{
		"class": {Type: "choice", Choice: f.answer, Confidence: f.conf},
	}, decide.Usage{}, nil
}

// withDecider points the command's provider seam at the fake for one test and puts
// it back. The seam is only read by a --decide run, so a non-parallel test owns it.
func withDecider(t *testing.T, d noteDecider) {
	t.Helper()
	old := newNoteDecider
	newNoteDecider = func() (noteDecider, error) { return d, nil }
	t.Cleanup(func() { newNoteDecider = old })
}

// The fake proves the question is built from the subject line, the From name and
// the addr and nothing else, and that an above-floor answer prints class= on the
// INBOX NOTE line.
func TestInboxDecideAsksSubjectOnlyAndPrintsClass(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	fake := &fakeDecider{answer: "act-now", conf: 0.95}
	withDecider(t, fake)

	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--decide").
		mustCode(t, 0).
		mustContain(t, "stdout", "class=act-now conf=0.95")
	if fake.calls == 0 {
		t.Fatal("--decide made no provider call for the new note")
	}
	if len(fake.qs) != 1 {
		t.Fatalf("want exactly one typed question, got %d: %v", len(fake.qs), fake.qs)
	}
	q, ok := fake.qs["class"]
	if !ok {
		t.Fatalf("question is not named class: %v", fake.qs)
	}
	for _, opt := range []string{"act-now", "read-later", "receipt-only"} {
		if _, ok := q.Choice[opt]; !ok {
			t.Fatalf("choice question is missing option %q: %v", opt, q.Choice)
		}
	}
	if q.Score != nil || q.Noul {
		t.Fatalf("class question must be a choice: %+v", q)
	}
	for _, want := range []string{"A question about the gate", "Bo", "to"} {
		if !strings.Contains(fake.state, want) {
			t.Fatalf("state does not carry %q: %q", want, fake.state)
		}
	}
	if strings.Contains(fake.state, "merge queue too?") {
		t.Fatalf("state must never carry the body: %q", fake.state)
	}
}

// STOP and HOLD are act-now by deterministic machinery, with no provider call.
func TestInboxDecideStopAndHoldAreDeterministic(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-bo/2026-09-07T0003Z-stop-222222222222.md",
		"From: Bo\nTo: Ada\nDate: Mon Sep  7 00:03:00 UTC 2026\nId: bo-222222222222\nSubject: STOP the merge\n\nbody\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "stop")
	fake := &fakeDecider{answer: "read-later", conf: 0.99}
	withDecider(t, fake)

	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--decide").
		mustCode(t, 0).
		mustContain(t, "stdout", "class=act-now conf=1.00")
	for _, s := range fake.states {
		if strings.Contains(s, "STOP the merge") {
			t.Fatalf("the provider was asked about a deterministic STOP: %q", s)
		}
	}
}

// A below-floor answer leaves the note printing exactly as today, class=unknown,
// with below=class naming the suggestion.
func TestInboxDecideBelowFloorKeepsTheNote(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	fake := &fakeDecider{answer: "read-later", conf: 0.50}
	withDecider(t, fake)

	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--decide").
		mustCode(t, 0).
		mustContain(t, "stdout", "A question about the gate").
		mustContain(t, "stdout", "class=unknown conf=0.50").
		mustContain(t, "stdout", "below=class")
}

// Above the floor, --only-act-now drops a read-later note from the listing.
func TestInboxDecideOnlyActNowFilters(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	fake := &fakeDecider{answer: "read-later", conf: 0.95}
	withDecider(t, fake)

	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--decide", "--only-act-now").
		mustCode(t, 0)
	if strings.Contains(r.stdout, "A question about the gate") {
		t.Fatalf("--only-act-now printed a read-later note:\n%s", r.stdout)
	}
}
