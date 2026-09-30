package machine

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// keyRule is a rule that reads one related query of its keys' subjects and
// takes at most take keys, the rest left.
func keyRule(name string, take int) sprint.Rule {
	return testRule(name, func(keys []sprint.AgendaKey, b sprint.ReadBounds, h int) (sprint.ReadPlan, []sprint.AgendaKey) {
		n := min(len(keys), sprint.Halved(take, h))
		return sprint.ReadPlan{Sprint: []sprint.SprintQ{{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: []string{},
			Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: keysOf(keys[:n])}}}}, keys[n:]
	}, func(*sprint.Snapshot, []sprint.AgendaKey, sprint.Now) sprint.RulePlan { return sprint.RulePlan{} })
}

// TestDispatchByRulePriorityAndOrder: keys are grouped by the rule that serves
// them, in the priority order of 1.4.2 (the round robin's), each rule's keys
// in agenda order (1.1), cut by its read; held back keys are left out, keys at
// a halving read apart at it, and keys no rule serves named (1.3.5, 1.4.2).
func TestDispatchByRulePriorityAndOrder(t *testing.T) {
	t.Parallel()
	rules := []sprint.Rule{keyRule("deal", 10), keyRule("resolve", 2), keyRule("seen", 10)}
	keys := []sprint.AgendaKey{{Key: "deal", Seq: 9}, {Key: "resolve:s2", Seq: 3}, {Key: "resolve:s1", Seq: 3},
		{Key: "resolve:s3", Seq: 1}, {Key: "seen:m1", Seq: 20}, {Key: "resolve:s4", Seq: 2}, {Key: "ask:p1", Seq: 1},
		{Key: "resolve:s9", Seq: 4}, {Key: "deal", Seq: 9}}
	got := dispatch(rules, keys, map[string]bool{"resolve:s4": true}, map[string]int{"resolve:s9": 1}, DefaultBudget())
	var lines []string
	for _, b := range got {
		lines = append(lines, b.Rule+"/"+strings.Join(keysOf(b.Keys), ",")+"/"+strings.Join(keysOf(b.Rest), ",")+"/"+string(rune('0'+b.Halvings)))
	}
	want := []string{
		"seen/seen:m1//0",
		"resolve/resolve:s1,resolve:s3/resolve:s2/0", // s3 (1) then s1 and s2 (3, by name): two taken
		"resolve/resolve:s9//1",                      // halved, read apart
		"deal/deal//0",                               // once
		"ask//ask:p1/0",                              // no rule serves it
	}
	if strings.Join(lines, " ") != strings.Join(want, " ") {
		t.Fatalf("batches\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !got[len(got)-1].NoRule {
		t.Fatal("the key no rule serves is not marked")
	}
	if ks := got[1].Keys; ks[0].Key != "resolve:s3" || ks[1].Key != "resolve:s1" {
		t.Fatalf("resolve's keys are not in agenda order: %v", ks)
	}
}
