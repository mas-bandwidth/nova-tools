package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// admit-with-no-stop-admits-everything: no STOP is no state to be in; every card is
// admitted and no reason is given, because there is nothing to say.
func TestAdmitWithNoStopAdmitsEverything(t *testing.T) {
	for _, stop := range [][]byte{nil, {}, []byte("\n  \n")} {
		ok, reason := Admit(stop, "RESULT card-1290 sha=abc123abc123 #900")
		if !ok || reason != "" {
			t.Fatalf("Admit(%q) = %v, %q; want admitted with no reason", stop, ok, reason)
		}
	}
}

// admit-only-the-reds-own-card (class C): STOP is a state with an admission list, not an
// all-or-nothing halt. While red, the one card that launches is the one whose line 1 names
// the red's issue or test -- bug 6, where the red's own fix card could not launch and went
// out by hand three times.
func TestAdmitOnlyTheCardThatNamesTheRed(t *testing.T) {
	stop := []byte("MAIN-RED 8f3714d1c0de run=35120376309 job=studio-fast test=TestGateHoldsTheBench\n#828\n")

	ok, reason := Admit(stop, "RESULT card-8140 sha=abc123abc123 fix #828 the gate's own red")
	if !ok {
		t.Fatalf("the red's own fix card was refused: %q", reason)
	}
	if reason != "" {
		t.Fatalf("an admitted card carries a reason: %q", reason)
	}

	ok, reason = Admit(stop, "RESULT card-8141 sha=abc123abc123 fix #900 something else")
	if ok {
		t.Fatal("a card that does not name the red was admitted while the bench is red")
	}
	if !strings.Contains(reason, "#828") {
		t.Fatalf("the refusal does not say what STOP admits: %q", reason)
	}

	// The admission name may be the failing test's, when the job log named no issue.
	byTest := []byte("MAIN-RED 8f3714d1c0de run=7 job=space test=TestRefillCounts\nTestRefillCounts\n")
	if ok, reason := Admit(byTest, "RESULT card-8142 sha=abc123abc123 red: TestRefillCounts green: the count"); !ok {
		t.Fatalf("the card naming the failing test was refused: %q", reason)
	}
	if ok, _ := Admit(byTest, "RESULT card-8143 sha=abc123abc123 fix #900"); ok {
		t.Fatal("a card naming neither the test nor the issue was admitted")
	}
}

// admit-refuses-all-without-an-admission-name: a STOP with no line 2 -- a person's hold,
// or a gate that could not name the red -- admits nothing, and says why.
func TestAdmitRefusesAllWithoutAnAdmissionName(t *testing.T) {
	for _, stop := range []string{
		"Glenn: hold the bench until I say\n",
		"MAIN-RED 8f3714d1c0de run=7 job=space test=-\n",
		"MAIN-RED 8f3714d1c0de run=7 job=space test=-\n\n",
	} {
		ok, reason := Admit([]byte(stop), "RESULT card-8140 sha=abc123abc123 fix #828")
		if ok {
			t.Fatalf("a STOP with no admission name admitted a card: %q", stop)
		}
		if !strings.Contains(reason, "admission name") {
			t.Fatalf("the refusal does not name the missing half: %q", reason)
		}
	}
}

// launch-admits-only-the-reds-own-card: the wiring. A STOP in the queue directory holds
// every card but the red's own, one ADMIT line per refusal capped at five with a MORE line
// carrying the count, nothing goes to nova-swarm, and the exit is 0 -- an admission refusal
// is not a failed run (rule 9).
func TestLaunchRefusesEveryCardTheStopDoesNotAdmit(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 8)
	if err := os.WriteFile(filepath.Join(root, StopFile), []byte("MAIN-RED 8f3714d1c0de run=1 job=studio test=TestOne\n#828\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 8, Deadline: "120", Queue: true,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- an admission refusal is not a failed run; stderr=%s", code, errb)
	}
	refusals, more := 0, 0
	for _, l := range nonEmptyLines(errb) {
		switch {
		case strings.HasPrefix(l, "ADMIT REFUSED "):
			refusals++
			if !strings.Contains(l, "gate=stop") {
				t.Errorf("ADMIT line does not name the gate: %q", l)
			}
		case strings.HasPrefix(l, "ADMIT MORE "):
			more++
			if !strings.Contains(l, "total=8") {
				t.Errorf("the MORE line does not carry the count: %q", l)
			}
		}
	}
	if refusals != 5 || more != 1 {
		t.Fatalf("want 5 ADMIT REFUSED lines and one ADMIT MORE, got %d and %d: %q", refusals, more, errb)
	}
	if !strings.Contains(out, "PULSE STOP admitted=0 refused=8") {
		t.Fatalf("stdout = %q, want one PULSE STOP line", out)
	}
	if raw, err := os.ReadFile(argvLog); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("nova-swarm ran while the bench was red: %q", raw)
	}
}

// launch-launches-the-reds-own-card: the same STOP, one card naming the red, and that card
// alone reaches nova-swarm.
func TestLaunchAdmitsTheCardThatNamesTheRed(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	fix := filepath.Join(src, "card-8140")
	if err := os.WriteFile(fix, []byte(Stamp("RESULT card-8140 sha=000000000000 fix #828\nbody\n", "test")), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(src, "card-8141")
	if err := os.WriteFile(other, []byte(Stamp("RESULT card-8141 sha=000000000000 fix #900\nbody\n", "test")), 0o644); err != nil {
		t.Fatal(err)
	}
	cards := filepath.Join(root, "cards.tsv")
	rows := "card-8140\t-\tpro\t" + fix + "\ncard-8141\t-\tpro\t" + other + "\n"
	if err := os.WriteFile(cards, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, StopFile), []byte("MAIN-RED 8f3714d1c0de run=1 job=studio test=TestOne\n#828\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Queue: true,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb)
	}
	if !strings.Contains(out, "PULSE OK ") || !strings.Contains(out, "n=1") {
		t.Fatalf("stdout = %q, want one card launched", out)
	}
	if !strings.Contains(errb, "ADMIT REFUSED card=card-8141") {
		t.Fatalf("stderr = %q, want the other card refused by name", errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil || !strings.Contains(string(raw), "nova-swarm batch") {
		t.Fatalf("nova-swarm argv = %q (err=%v), want one batch", raw, err)
	}
	id := pulseID(t, out)
	tasks, err := os.ReadDir(filepath.Join(root, "cards", id, "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("the batch carried %d cards, want the red's own alone", len(tasks))
	}
}

// launch-with-no-stop-is-unchanged: the guard. With no STOP in the queue directory the
// launch path is exactly what it was, and nothing about admission is printed.
func TestLaunchWithNoStopPrintsNoAdmitLine(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 2)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb)
	}
	if strings.Contains(errb, "ADMIT ") || strings.Contains(out, "PULSE STOP ") {
		t.Fatalf("a launch with no STOP said something about admission: out=%q err=%q", out, errb)
	}
}
