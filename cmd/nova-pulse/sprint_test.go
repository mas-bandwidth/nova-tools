package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func sprintTestDeps(store *sprint.FakeStore) sprintDeps {
	return sprintDeps{
		open:    func(addr, user, password string) (sprint.Store, error) { return store, nil },
		cards:   &sprint.FakeCards{},
		getenv:  func(string) string { return "" },
		records: nil,
	}
}

func runVerb(t *testing.T, store *sprint.FakeStore, now time.Time, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runSprint(args, &out, &errOut, now, sprintTestDeps(store))
	return code, out.String(), errOut.String()
}

var testNow = time.Date(2026, 9, 22, 17, 0, 0, 0, time.UTC)

// TestSprintRefusesWithoutTheStore: every verb needs the store address and refuses to guess
// one (docs/SPEC.md's `refusing to guess`, never a default host).
func TestSprintRefusesWithoutTheStore(t *testing.T) {
	store := sprint.NewFakeStore()
	code, _, errOut := runVerb(t, store, testNow, "status")
	if code != 2 {
		t.Fatalf("sprint status with no --store exited %d, wants 2", code)
	}
	if !strings.Contains(errOut, "--store is required") || !strings.Contains(errOut, "refusing to guess") {
		t.Fatalf("the refusal is %q; it must name --store and refuse to guess", errOut)
	}
}

// TestSprintRefusesAnUnknownVerbByName, with the whole list on the line.
func TestSprintRefusesAnUnknownVerbByName(t *testing.T) {
	store := sprint.NewFakeStore()
	code, _, errOut := runVerb(t, store, testNow, "burndown")
	if code != 2 || !strings.Contains(errOut, `unknown verb "burndown"`) {
		t.Fatalf("exit %d, refusal %q", code, errOut)
	}
	for _, verb := range []string{"open", "add", "status", "route", "split", "refill", "wall", "calibration", "close"} {
		if !strings.Contains(errOut, verb) {
			t.Fatalf("the refusal does not name the verb %q: %s", verb, errOut)
		}
	}
}

// TestSprintAddRefusesAnUnknownKind, naming the kinds, because a kind is what the router's
// rule table branches on and a typo would route by accident.
func TestSprintAddRefusesAnUnknownKind(t *testing.T) {
	store := sprint.NewFakeStore()
	runVerb(t, store, testNow, "open", "--name", "s", "--goal", "g", "--store", "store.invalid:6380")
	code, _, errOut := runVerb(t, store, testNow, "add", "--name", "s", "--ref", "o/n#1", "--kind", "chore", "--store", "store.invalid:6380")
	if code != 2 || !strings.Contains(errOut, "not one of") {
		t.Fatalf("exit %d, refusal %q", code, errOut)
	}
}

// TestSprintStatusWithNoSprintSaysHowToOpenOne: the remedy on the line is the contract.
func TestSprintStatusWithNoSprintSaysHowToOpenOne(t *testing.T) {
	store := sprint.NewFakeStore()
	code, _, errOut := runVerb(t, store, testNow, "status", "--store", "store.invalid:6380")
	if code != 2 || !strings.Contains(errOut, "nova-pulse sprint open") {
		t.Fatalf("exit %d, refusal %q", code, errOut)
	}
}

// TestSeveralOpenSprintsPrintATable is Glenn's second format, and the one nobody has needed
// yet: the sprint name leftmost, then the same line.
func TestSeveralOpenSprintsPrintATable(t *testing.T) {
	store := sprint.NewFakeStore()
	addr := []string{"--store", "store.invalid:6380"}
	runVerb(t, store, testNow, append([]string{"open", "--name", "fixes-2026-09-22", "--goal", "the fixes day"}, addr...)...)
	runVerb(t, store, testNow.Add(time.Minute), append([]string{"open", "--name", "week-39", "--goal", "the week"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "fixes-2026-09-22", "--ref", "o/n#1", "--kind", "fix", "--owner", "johnny", "--est", "60"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "week-39", "--ref", "o/n#2", "--kind", "fix", "--owner", "stella", "--est", "120"}, addr...)...)
	code, out, errOut := runVerb(t, store, testNow, append([]string{"status"}, addr...)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "fixes-2026-09-22  0/1 0% -> ~1h") || !strings.Contains(out, "week-39           0/1 0% -> ~2h") {
		t.Fatalf("the table is:\n%s", out)
	}
}

// TestCloseRefusesWithoutEvidence: a task closes from a PRIMARY RECORD, so the verb will not
// close one without naming which.
func TestCloseRefusesWithoutEvidence(t *testing.T) {
	store := sprint.NewFakeStore()
	addr := []string{"--store", "store.invalid:6380"}
	runVerb(t, store, testNow, append([]string{"open", "--name", "s", "--goal", "g"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "s", "--id", "t", "--ref", "o/n#1", "--kind", "fix"}, addr...)...)
	code, _, errOut := runVerb(t, store, testNow, append([]string{"close", "--name", "s", "--task", "t"}, addr...)...)
	if code != 2 || !strings.Contains(errOut, "--evidence is required") {
		t.Fatalf("exit %d, refusal %q", code, errOut)
	}
}

// TestStatusFlipClosesFromTheRecordAndNothingElse: the --flip pass asks the primary records
// and writes what they say, with the evidence on the task.
func TestStatusFlipClosesFromTheRecordAndNothingElse(t *testing.T) {
	store := sprint.NewFakeStore()
	addr := []string{"--store", "store.invalid:6380"}
	runVerb(t, store, testNow, append([]string{"open", "--name", "s", "--goal", "g"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "s", "--id", "landed", "--ref", "cell-a@1", "--kind", "card"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "s", "--id", "out", "--ref", "cell-b@1", "--kind", "card"}, addr...)...)

	deps := sprintTestDeps(store)
	deps.cards = &sprint.FakeCards{Landings: map[string]sprint.Record{
		"cell-a@1": {Closed: true, Evidence: "landed 0cda23d5", At: testNow},
	}}
	var out, errOut bytes.Buffer
	if code := runSprint(append([]string{"status", "--flip"}, addr...), &out, &errOut, testNow, deps); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "CLOSED landed cell-a@1 landed 0cda23d5") {
		t.Fatalf("the flip did not close the landed card:\n%s", out.String())
	}
	if strings.Contains(out.String(), "CLOSED out") {
		t.Fatalf("the flip closed a card the stream said nothing about:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1/2 50%") {
		t.Fatalf("the line after the flip is:\n%s", out.String())
	}
}

// TestRefillDryRunWritesNothing: a coordinator sees what a refill would do before it does it.
func TestRefillDryRunWritesNothing(t *testing.T) {
	store := sprint.NewFakeStore()
	store.SetPresent("johnny", true)
	addr := []string{"--store", "store.invalid:6380"}
	runVerb(t, store, testNow, append([]string{"open", "--name", "s", "--goal", "g"}, addr...)...)
	runVerb(t, store, testNow, append([]string{"add", "--name", "s", "--id", "t", "--ref", "o/n#1", "--kind", "fix", "--owner", "johnny"}, addr...)...)
	code, out, errOut := runVerb(t, store, testNow, append([]string{"refill", "--friends", "johnny", "--dry-run"}, addr...)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "PLACE t o/n#1 -> q:johnny") {
		t.Fatalf("the dry run printed:\n%s", out)
	}
	if got := len(store.Queue("q:johnny")); got != 0 {
		t.Fatalf("the dry run placed %d tasks on the stream; it writes nothing", got)
	}
}

// TestTaskIDFromRefIsTheStoresKey: the id must not carry the one-line grammar's separator,
// whatever the ref looked like.
func TestTaskIDFromRefIsTheStoresKey(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"mas-bandwidth/nova-tools#2550", "nova-tools-2550"},
		{"cell-2026-09-22-a@1", "cell-2026-09-22-a@1"},
		{"memory: the ruling", "memory-the-ruling"},
	} {
		if got := taskIDFrom(tc.ref); got != tc.want {
			t.Fatalf("taskIDFrom(%q) is %q, wants %q", tc.ref, got, tc.want)
		}
		if err := sprint.ValidateName("task", taskIDFrom(tc.ref)); err != nil {
			t.Fatalf("the id made from %q is not a name the store takes: %v", tc.ref, err)
		}
	}
}

// TestSprintVerbIsInTheUsageBanner: every verb a person can type is on the page they are
// pointed at when they get it wrong.
func TestSprintVerbIsInTheUsageBanner(t *testing.T) {
	for _, want := range []string{
		"nova-pulse sprint open", "nova-pulse sprint add", "nova-pulse sprint status",
		"nova-pulse sprint route", "nova-pulse sprint split", "nova-pulse sprint refill",
		"nova-pulse sprint wall", "nova-pulse sprint calibration", "nova-pulse sprint close",
	} {
		if !strings.Contains(usage, want) {
			t.Fatalf("the usage banner has no %q", want)
		}
	}
}
