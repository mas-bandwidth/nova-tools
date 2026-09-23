package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/deal"
)

var routeNow = time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC)

func linuxBench(name string) deal.Capabilities {
	return deal.Capabilities{
		Name: name, Kind: deal.KindBench, Present: true, Width: 12,
		Legs: []string{"go", "c+cpp", "python"}, Locality: deal.LocalityDatacenter,
		MaxWallMinutes: 180, Isolation: []string{"net", "nonet"},
		Kinds: []string{KindCard, KindFix, KindScript, KindEvaluation},
	}
}

func houseBench(name string) deal.Capabilities {
	c := linuxBench(name)
	c.Locality = deal.LocalityHouse
	return c
}

func friendRow(name string) deal.Capabilities {
	return deal.Capabilities{Name: name, Kind: deal.KindFriend, Present: true, Width: deal.FriendWidth, Locality: deal.LocalityAny}
}

func table() []deal.Capabilities {
	return []deal.Capabilities{
		linuxBench("hulk"), linuxBench("vision"), linuxBench("space"), linuxBench("hetzner"),
		houseBench("superman"), houseBench("batman"),
		friendRow("johnny"), friendRow("stella"), friendRow("emma"),
	}
}

// TestALivePathGoesToAFriend is the first row of the rule table, and the one that costs
// money: a task that touches something already running on a machine is a person's.
func TestALivePathGoesToAFriend(t *testing.T) {
	task := Task{ID: "converge", Kind: KindFix, Ref: "o/n#1", State: StateOpen, EstMinutes: 60, Paths: []string{"fleet/redis.yml"}}
	d, err := Route(task, table(), nil, routeNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Route, RouteFriend) {
		t.Fatalf("the route is %q; a live path is a friend's", d.Route)
	}
	if !strings.Contains(d.Reason, "live or security path") {
		t.Fatalf("the reason is %q; it must name why", d.Reason)
	}
}

// TestAReadIsAFriendsTypedLine: a read cannot be a card, however empty the benches are.
func TestAReadIsAFriendsTypedLine(t *testing.T) {
	d, err := Route(Task{ID: "read-2598", Kind: KindRead, Ref: "o/n#2598", State: StateOpen, EstMinutes: 10}, table(), nil, routeNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Route, RouteFriend) {
		t.Fatalf("the route is %q; a read is a friend's typed line", d.Route)
	}
}

// TestNoFriendPresentIsARefusalNotABench: when nobody is up, the task waits and says so.
// Sending a live-path fix to a bench because no one was awake is exactly the failure the
// rule exists to stop.
func TestNoFriendPresentIsARefusalNotABench(t *testing.T) {
	benchesOnly := []deal.Capabilities{linuxBench("hulk")}
	_, err := Route(Task{ID: "read", Kind: KindRead, Ref: "o/n#1", State: StateOpen, EstMinutes: 10}, benchesOnly, nil, routeNow)
	if err == nil || !strings.Contains(err.Error(), "no friend is present") {
		t.Fatalf("Route answered %v; with no friend up the read waits", err)
	}
}

// TestTheTenSwarmCardsGoLinuxOnly reproduces the routing Rowan did by hand on 2026-09-22:
// A5 (#2544) was open, so the house uplink was a known defect and every card carried
// locality=datacenter. The match sends all ten to the Linux boxes and NAMES the locality;
// nothing lands on superman, batman or the Studio.
func TestTheTenSwarmCardsGoLinuxOnly(t *testing.T) {
	linux := map[string]bool{"hulk": true, "vision": true, "space": true, "hetzner": true}
	load := map[string]int{}
	for i := 0; i < 10; i++ {
		card := Task{
			ID: "cell-" + string(rune('a'+i)), Kind: KindCard, Ref: "o/n#" + string(rune('0'+i)),
			State: StateOpen, EstMinutes: 30, Leg: "go",
			Locality: deal.LocalityDatacenter, Repo: "mas-bandwidth/nova-tools", Base: "dev",
			Routes: []string{"flash", "pro"},
			Paths:  []string{"internal/swarm/cell.go"},
		}
		d, err := Route(card, table(), load, routeNow)
		if err != nil {
			t.Fatalf("card %s: %v", card.ID, err)
		}
		name := strings.TrimPrefix(d.Route, RouteBench)
		if !linux[name] {
			t.Fatalf("card %s went to %q; while A5 is open the house uplink takes no card", card.ID, d.Route)
		}
		if !strings.Contains(d.Reason, "locality datacenter") {
			t.Fatalf("card %s routed for %q; the reason must name the locality", card.ID, d.Reason)
		}
		if strings.Join(d.Routes, ",") != "flash,pro" {
			t.Fatalf("card %s lost its tier: %v -- the tier is a field the launcher reads, not a queue", card.ID, d.Routes)
		}
		load[name] += card.EstMinutes
	}
	// Ten cards over four benches, and the router balanced them rather than stacking the
	// first name in the alphabet: that is the wall getting shorter for nothing.
	for name, minutes := range load {
		if minutes > 120 {
			t.Fatalf("%s took %d minutes of the ten cards; the lanes are meant to balance (%v)", name, minutes, load)
		}
	}
}

// TestSplitFirstNamesTheSeamsByFile: a task across packages is not routed at all until it is
// split, and the seams are files, not a suggestion.
func TestSplitFirstNamesTheSeamsByFile(t *testing.T) {
	wide := Task{
		ID: "wide", Kind: KindFix, Ref: "o/n#1", State: StateOpen, EstMinutes: 120,
		Paths: []string{"internal/merge/batch.go", "internal/swarm/provider.go"},
	}
	d, err := Route(wide, table(), nil, routeNow)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Split {
		t.Fatalf("a task across two packages was routed whole: %s", d.Line())
	}
	if !strings.Contains(d.Line(), "internal/merge=internal/merge/batch.go") {
		t.Fatalf("the seams are %q; they are named by file", d.Line())
	}
	children, err := Split(wide, d.Seams)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("Split made %d children, wants 2", len(children))
	}
	if children[0].EstMinutes+children[1].EstMinutes != wide.EstMinutes {
		t.Fatalf("the children's estimates are %d and %d; they must add up to the parent's %d", children[0].EstMinutes, children[1].EstMinutes, wide.EstMinutes)
	}
	for _, c := range children {
		if c.Owner != "" {
			t.Fatalf("child %s kept an owner; a split offers the work, it does not assign it", c.ID)
		}
	}
}

// TestSplitKeepsTheOwnerAsTheRequiredReader: the typing moves, the verdict does not.
func TestSplitKeepsTheOwnerAsTheRequiredReader(t *testing.T) {
	wide := Task{
		ID: "wide", Kind: KindFix, Ref: "o/n#1", Owner: "johnny", State: StateOpen, EstMinutes: 120,
		Paths: []string{"internal/merge/batch.go", "internal/swarm/provider.go"},
	}
	_, seams := SplitFirst(wide)
	children, err := Split(wide, seams)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range children {
		if c.Reader != "johnny" {
			t.Fatalf("child %s has reader %q, wants johnny", c.ID, c.Reader)
		}
	}
}

// TestSplitRefusesHalvesThatShareAFile: two names and a rebase is not a split.
func TestSplitRefusesHalvesThatShareAFile(t *testing.T) {
	task := Task{ID: "t", Kind: KindFix, Ref: "o/n#1", State: StateOpen, EstMinutes: 120}
	seams := []Seam{{Package: "a", Paths: []string{"x.go"}}, {Package: "b", Paths: []string{"x.go"}}}
	if _, err := Split(task, seams); err == nil || !strings.Contains(err.Error(), "must be disjoint") {
		t.Fatalf("Split answered %v; halves that share a file are one task", err)
	}
}

// TestOneBigTaskInOnePackageStaysOne: over the bound with no seam to cut on is a fact to
// state, not a split to invent.
func TestOneBigTaskInOnePackageStaysOne(t *testing.T) {
	big := Task{ID: "big", Kind: KindFix, Ref: "o/n#1", State: StateOpen, EstMinutes: 240, Paths: []string{"internal/merge/batch.go", "internal/merge/land.go"}}
	if split, _ := SplitFirst(big); split {
		t.Fatalf("a task inside one package was split; the seam would be a rebase")
	}
}

// TestACrossPackageCardKeepsAFriendAsReader: the judgement half of the rule table.
func TestACrossPackageCardKeepsAFriendAsReader(t *testing.T) {
	card := Task{
		ID: "c", Kind: KindCard, Ref: "label@1", Owner: "", State: StateOpen, EstMinutes: 30,
		Reader: "stella", Paths: []string{"internal/swarm/a.go", "internal/swarm/b.go"},
	}
	d, err := Route(card, table(), nil, routeNow)
	if err != nil {
		t.Fatal(err)
	}
	if d.Reader != "stella" {
		t.Fatalf("the reader is %q, wants stella", d.Reader)
	}
	if !strings.Contains(d.Line(), "reader=stella") {
		t.Fatalf("the line is %q; the required reader belongs on it", d.Line())
	}
}
