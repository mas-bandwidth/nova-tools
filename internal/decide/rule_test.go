package decide

import (
	"context"
	"testing"
	"time"
)

// A committed rule answers its kind without a provider call and is still
// logged; anything that makes the unit not that constant -- a confirmed
// failure, a security touch, a fresh take or a size above the smallest bucket
// -- steps the rule aside and the ladder answers as today. A rule with no by is
// refused at load, because promotion is a person's commit and never the
// tool's (SPEC-DECIDE housekeeping H1).
func TestARuledKindMakesNoCallAndIsStillLogged(t *testing.T) {
	const committed = `{"minds":[
	  {"name":"flash","lineage":"deepseek","height":0,"availability":"available","ask":"card"},
	  {"name":"pro","lineage":"deepseek","height":1,"availability":"available","ask":"card"},
	  {"name":"emma","lineage":"emma","height":1,"availability":"available","ask":"bus"},
	  {"name":"fable","lineage":"rowan","height":2,"availability":"available","ask":"bus"}
	],"rules":[{"kind":"dogfood","rung":"pro","by":"glenn","from":"2026-09-19"}]}`
	reg, err := ParseRegistry([]byte(committed))
	if err != nil {
		t.Fatalf("a registry with a committed rule does not parse: %v", err)
	}

	fake := &fakeDecider{choice: "emma", conf: 0.99}
	u := Unit{ID: "d1", Kind: KindDogfood, Files: 2, Packages: 1, Lanes: 1}
	res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
	if err != nil {
		t.Fatalf("a ruled kind must answer: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("a ruled kind makes no provider call, and %d were made", fake.calls)
	}
	if res.Source != "rule" {
		t.Errorf("source = %q, want %q: the answer is the committed rule", res.Source, "rule")
	}
	if res.Rung.Name != "pro" {
		t.Errorf("the rule names pro; got %s", res.Rung.Name)
	}
	if row := EntryFor(res, u, time.Now()); row.Source != "rule" || row.Calls != 0 {
		t.Errorf("a ruled decision is still logged: source=%q calls=%d", row.Source, row.Calls)
	}

	// A confirmed failure steps the rule aside: the ladder answers and the
	// provider is asked as it is today.
	stepped := Unit{ID: "d2", Kind: KindDogfood, Files: 2, Packages: 1, Lanes: 2,
		Attempts: []Attempt{{Rung: "flash", Outcome: OutcomeFailed}}}
	asked := &fakeDecider{choice: "rung-1", conf: 0.99}
	res2, err := RouteJev(context.Background(), asked, reg, stepped, DefaultFloor)
	if err != nil {
		t.Fatalf("a stepped-aside rule must still route: %v", err)
	}
	if asked.calls == 0 {
		t.Error("a confirmed failure steps the rule aside, so the ladder must ask; no provider call was made")
	}
	if res2.Source == "rule" {
		t.Errorf("a confirmed failure steps the rule aside; got source=%q", res2.Source)
	}

	// Promotion is a person's commit: a rule with no by is refused at load.
	silent := `{"minds":[{"name":"pro","lineage":"deepseek","height":1,"availability":"available","ask":"card"}],
	  "rules":[{"kind":"dogfood","rung":"pro"}]}`
	if _, err := ParseRegistry([]byte(silent)); err == nil {
		t.Error("a rule with no by is refused at load")
	}
}
