package deal

import (
	"strings"
	"testing"
	"time"
)

// bench is one datacenter Linux bench with every leg and a three hour bound.
func bench(name string) Capabilities {
	return Capabilities{
		Name: name, Kind: KindBench, Present: true, Width: 12,
		Legs: []string{"go", "c+cpp", "python"}, Locality: LocalityDatacenter,
		MaxWallMinutes: 180, Isolation: []string{"net", "nonet"},
		Kinds: []string{"card", "fix", "script"}, Mirrors: []string{"mas-bandwidth/nova-tools@dev"},
	}
}

func friend(name string) Capabilities {
	return Capabilities{Name: name, Kind: KindFriend, Present: true, Width: FriendWidth, Locality: LocalityAny}
}

// TestMatchRefusesByTheFieldThatDidNotFit is the table of axes. Each row is a requirement
// this week's faults taught us to carry, and each asserts that the refusal NAMES the field --
// a router that says only "no match" leaves the next person guessing what to change.
func TestMatchRefusesByTheFieldThatDidNotFit(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Requirements
		want string
	}{
		{"a leg the bench has not got", Requirements{Leg: "squirrel"}, "no squirrel leg"},
		{"the house uplink when the task wants a datacenter", Requirements{Locality: LocalityHouse}, "locality datacenter, wants house"},
		{"a task longer than the bench's idle bound", Requirements{WallMinutes: 240}, "over its 180m bound"},
		{"an isolation the bench does not offer", Requirements{Isolation: "kvm"}, "no kvm isolation"},
		{"a kind the bench does not take", Requirements{Kind: "evaluation"}, "does not take evaluation"},
		{"a route the bench cannot reach", Requirements{Routes: []string{"muse"}}, "no route in muse"},
		{"somebody else's task", Requirements{Owner: "johnny"}, "owned by johnny"},
		{"a friend's task on a bench", Requirements{ConsumerKind: KindFriend}, "wants a friend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := []Capabilities{withRoutes(bench("hulk"), []string{"flash", "pro"})}
			_, _, err := Match(tc.req, table, nil)
			if err == nil {
				t.Fatalf("Match took a consumer it should have refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal is %q; it must name the field that did not fit (%q)", err, tc.want)
			}
		})
	}
}

func withRoutes(c Capabilities, routes []string) Capabilities {
	c.Routes = routes
	return c
}

// TestMatchNeverTakesAnAwayConsumer is the rule that is not about the work: presence is
// measured, and an away consumer is not offered anything, ever.
func TestMatchNeverTakesAnAwayConsumer(t *testing.T) {
	away := friend("freddy")
	away.Present = false
	if _, _, err := Match(Requirements{Kind: "read", ConsumerKind: KindFriend}, []Capabilities{away}, nil); err == nil {
		t.Fatalf("a task was routed to a consumer with no heartbeat")
	}
	up := friend("johnny")
	got, reason, err := Match(Requirements{Kind: "read", ConsumerKind: KindFriend}, []Capabilities{away, up}, nil)
	if err != nil {
		t.Fatalf("Match refused the present friend: %v", err)
	}
	if got.Name != "johnny" || reason == "" {
		t.Fatalf("Match returned %q with reason %q; wants johnny with a reason", got.Name, reason)
	}
}

// TestMatchPrefersTheWarmMirrorThenTheEmptiestLane: a cold clone cost three minutes a card on
// 2026-09-21, and piling a second task on the first name in the alphabet is how a wall gets
// longer for nothing.
func TestMatchPrefersTheWarmMirrorThenTheEmptiestLane(t *testing.T) {
	cold := bench("aaa")
	cold.Mirrors = nil
	warm := bench("zzz")
	req := Requirements{Repo: "mas-bandwidth/nova-tools", Base: "dev", Kind: "card"}
	got, _, err := Match(req, []Capabilities{cold, warm}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "zzz" {
		t.Fatalf("Match took %q; the warm mirror wins", got.Name)
	}
	busy, idle := bench("hulk"), bench("vision")
	got, _, err = Match(Requirements{Kind: "card"}, []Capabilities{busy, idle}, map[string]int{"hulk": 240})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "vision" {
		t.Fatalf("Match took %q; the emptiest lane shortens the wall", got.Name)
	}
}

// TestPlanTopsToWidthOwnershipThenAge is the dealer's rule itself, the one a bench's card
// dealer and a friend's refill both call.
func TestPlanTopsToWidthOwnershipThenAge(t *testing.T) {
	base := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{ID: "new-owned", Req: Requirements{Owner: "johnny", Kind: "fix"}, Time: base.Add(3 * time.Hour)},
		{ID: "old-owned", Req: Requirements{Owner: "johnny", Kind: "fix"}, Time: base},
		{ID: "unowned-old", Req: Requirements{Kind: "fix"}, Time: base.Add(time.Hour)},
		{ID: "unowned-new", Req: Requirements{Kind: "fix"}, Time: base.Add(2 * time.Hour)},
		{ID: "fifth", Req: Requirements{Kind: "fix"}, Time: base.Add(4 * time.Hour)},
	}
	got := Plan([]Capabilities{friend("johnny")}, map[string]int{}, items, 0)
	var ids []string
	for _, p := range got {
		ids = append(ids, p.ItemID)
	}
	want := "old-owned,new-owned,unowned-old,unowned-new"
	if strings.Join(ids, ",") != want {
		t.Fatalf("Plan placed %v; wants %s (ownership first, then age, four deep)", ids, want)
	}
}

// TestPlanLeavesAQueueThatIsNotShortAlone: the refill happens when a queue drops BELOW the
// low water mark, not on every tick -- a dealer that tops up a full queue is a dealer that
// hands the same work out twice.
func TestPlanLeavesAQueueThatIsNotShortAlone(t *testing.T) {
	items := []Item{{ID: "a", Req: Requirements{Kind: "fix"}}, {ID: "b", Req: Requirements{Kind: "fix"}}}
	if got := Plan([]Capabilities{friend("emma")}, map[string]int{"emma": 2}, items, 0); len(got) != 0 {
		t.Fatalf("Plan refilled a queue at the low water mark: %v", got)
	}
	got := Plan([]Capabilities{friend("emma")}, map[string]int{"emma": 1}, items, 0)
	if len(got) != 2 {
		t.Fatalf("Plan placed %d items on a queue of one; wants it topped to four with what there is", len(got))
	}
}

// TestPlanSendsAPriorityTaskToTheFrontStream: a fix found in the current batch re-enters at
// the front (ruling 2026-09-20), which is a stream beside the consumer's own, never another
// consumer.
func TestPlanSendsAPriorityTaskToTheFrontStream(t *testing.T) {
	items := []Item{
		{ID: "bulk", Req: Requirements{Kind: "fix"}},
		{ID: "recut", Req: Requirements{Kind: "fix", Priority: true}},
	}
	got := Plan([]Capabilities{bench("hulk")}, map[string]int{}, items, 0)
	if len(got) < 2 {
		t.Fatalf("Plan placed %d items, wants 2", len(got))
	}
	if got[0].ItemID != "recut" || got[0].Queue != "q:hulk:front" {
		t.Fatalf("the priority task went to %s on %s; wants recut first on q:hulk:front", got[0].ItemID, got[0].Queue)
	}
	if got[1].Queue != "q:hulk" {
		t.Fatalf("the bulk task went to %s; wants q:hulk", got[1].Queue)
	}
}

// TestQueueNameIsTheConsumersName pins the one thing that must never be hardcoded: rename a
// consumer in the table and its stream follows, because there is no fixed set of queues.
func TestQueueNameIsTheConsumersName(t *testing.T) {
	c := Capabilities{Name: "leg:c+cpp"}
	if c.Queue() != "q:leg:c+cpp" || c.FrontQueue() != "q:leg:c+cpp:front" {
		t.Fatalf("the queue names are %q and %q", c.Queue(), c.FrontQueue())
	}
}
