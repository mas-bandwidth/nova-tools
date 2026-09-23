package decide

import (
	"strings"
	"testing"
)

// `chore` is the name the shift lanes actually typed on 2026-09-19, and the
// ladder refused it: `ROUTE REFUSED reason=no-rung ... has kind "chore", want
// one of rebase, stack, fixture-retarget, fleet-chore, ...` (two refusals in
// the shared route log; schema-issues HANDOFF D1). A chore of the fleet IS a
// fleet-chore under a shorter name -- same start height, same mechanical
// eligibility -- so it is an ALIAS and not a new kind: it adds no row to Kinds
// and no rung to the ladder.
//
// Found and built by the fix-runner lane, 2026-09-19; folded in here with its
// test.
func TestChoreIsAnAliasForFleetChoreAndNotANewKind(t *testing.T) {
	if got := CanonicalKind("chore"); got != KindFleetChore {
		t.Errorf("CanonicalKind(chore) = %q, want %q", got, KindFleetChore)
	}
	if got := CanonicalKind(KindRowTest); got != KindRowTest {
		t.Errorf("a kind the table holds is returned unchanged, got %q", got)
	}
	for _, k := range Kinds {
		if k == "chore" {
			t.Error("an alias must not become a row in Kinds; it is a second name, not a second kind")
		}
	}
	if KnownKind("chore") {
		t.Error("KnownKind answers about the table, and the table holds fleet-chore")
	}
}

// The alias is resolved ONCE, where the evidence is read, so the kind the
// ladder decides on and the kind the route log records are the same name. An
// alias reaching the log would split every per-kind floor in two.
func TestTheAliasIsResolvedAtTheReadSoTheLogKeepsTheCanonicalName(t *testing.T) {
	u, err := ParseUnit([]byte(`{"id":"u-chore","kind":"chore","files":1,"packages":1,"lanes":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if u.Kind != KindFleetChore {
		t.Fatalf("the parsed unit carries kind %q; the log would record the alias", u.Kind)
	}
	reg := testRegistry(t)
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name != "flash" {
		t.Errorf("a chore routed to %s, want flash (%s)", res.Rung.Name, res.Reason)
	}
	if res.Kind != KindFleetChore {
		t.Errorf("the route result carries kind %q, want %q", res.Kind, KindFleetChore)
	}
	if strings.Contains(res.Reason, `"chore"`) || strings.Contains(res.Reason, "kind chore ") {
		t.Errorf("the reason names the alias rather than the kind: %q", res.Reason)
	}
}

// A near miss is still a refusal. An alias table is a short list of names the
// table already holds, never a spell-corrector.
func TestANameThatIsNotAnAliasStillRefuses(t *testing.T) {
	reg := testRegistry(t)
	for _, bad := range []string{"choree", "chores", "fleetchore", ""} {
		u, err := ParseUnit([]byte(`{"id":"u","kind":"` + bad + `","files":1,"packages":1,"lanes":1}`))
		if err != nil {
			continue
		}
		if _, err := RouteRules(reg, u, DefaultFloor); err == nil {
			t.Errorf("kind %q was accepted; only `chore` is an alias", bad)
		}
	}
}
