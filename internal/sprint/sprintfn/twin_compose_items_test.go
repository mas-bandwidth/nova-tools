package sprintfn

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The five items' real phases composed in one twin (the cold read of PR 4788,
// L3): X (IT13), the intents (IT14), J (IT15), the parts (IT16) and the queries
// (IT30), read back through IT08's alignment. Each item's own tests stand in
// for the others; these compose them.

// composedItems is a twin with every item's phase and part: NewTwin's phases
// (X, the intents' Derive, J), the registered parts, the intents' X commands
// and the queries; the counter is seeded, as a sprint's first step does.
func composedItems(t *testing.T) *Twin {
	t.Helper()
	tw, _, _ := newTestTwin(t, defaultPhases)
	tw.parts = defaultParts
	tw.UseIntents()
	tw.UseQueries()
	composedStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "seed"}, Sprint: &SprintPart{Counter: &CounterChange{
		Read: map[string]string{"score": "", "streams": ""}, Set: map[string]string{"score": "1000", "streams": "1"}}}})
	composedStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}}}}})
	return tw
}

// composedStep applies one step and fails on a refusal.
func composedStep(t *testing.T, tw *Twin, req *Request) {
	t.Helper()
	res, err := Step(context.Background(), tw, req)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if res.Refusal != nil {
		t.Fatalf("step refused: %s %s", res.Refusal.Code, res.Refusal.Message)
	}
}

// TestComposedAdmitReadsBackThroughWaiters: admitting p2, which waits for n9
// that has no record, lands wait:n9 = {p2} and missing = {n9} (the intents),
// opens "blocked on something missing" (J); `waiters(n9)` (the queries) answers
// n9 as missing with its waiter, and the answer loads through IT08's
// alignment; `jnote` reads J's judgment back, the note its subject's own.
func TestComposedAdmitReadsBackThroughWaiters(t *testing.T) {
	t.Parallel()
	tw := composedItems(t)
	e, in := admit("p2", "n9", "1")
	composedStep(t, tw, intentStep(e, in))
	q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("n9"), Limit: 5, Fields: []string{"open"}, Keys: []string{sprint.KeyDropping}}
	a, err := tw.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sprint.LoadPartial(sprint.ReadPlan{Sprint: []sprint.SprintQ{q}}, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0",
		TimeMS: "1790000000123", Sprint: []sprint.Answer{a}}); err != nil {
		t.Fatalf("the answer does not load: %v", err)
	}
	if len(a.Needs) != 1 || !a.Needs[0].Missing || len(a.Needs[0].Waiters) != 1 || a.Needs[0].Waiters[0] != "p2" || a.Needs[0].Last != "p2" {
		t.Fatalf("waiters of n9: %+v", a.Needs)
	}
	full, _, err := tw.QueryFull(sprint.SprintQ{Kind: sprint.QueryJnote, Source: head("jnotes", 2), Fields: []string{}, Subjects: 4})
	if err != nil {
		t.Fatal(err)
	}
	jr := full.(JnoteResult)
	if len(jr.Items) != 1 || len(jr.Items[0].Subjects) != 1 || jr.Items[0].Subjects[0].Own == nil || *jr.Items[0].Subjects[0].Own != jr.Items[0].Note {
		t.Fatalf("jnote does not read J's judgment back: %+v", jr)
	}
}

// TestComposedIntentAndSprintPartInOneStep: one coordinator step carries a
// waitfor intent of two needs and the sprint part (the coordinator): both
// needs' wait keys, missing, J's notes and the coordinator key all land.
func TestComposedIntentAndSprintPartInOneStep(t *testing.T) {
	t.Parallel()
	tw := composedItems(t)
	e, in := admit("p3", "n9,n8", "2")
	req := intentStep(e, in)
	req.Sprint = &SprintPart{Coordinator: "coord"}
	composedStep(t, tw, req)
	k := tw.SprintKeys()
	for _, n := range []string{"n9", "n8"} {
		if _, ok := k[ek("wait:"+n)].ZSet["p3"]; !ok {
			t.Errorf("the intents' wait:%s lost", n)
		}
		if _, ok := k[ek("missing")].ZSet[n]; !ok {
			t.Errorf("%s is not in missing", n)
		}
	}
	if got := k[ek("jopen:p3")].Hash; len(got) != 2 {
		t.Errorf("J opened %d judgments on p3, want 2: %v", len(got), got)
	}
	if k[testPrefix+"sprint:coordinator"].String != "coord" {
		t.Errorf("the part's coordinator lost")
	}
}
