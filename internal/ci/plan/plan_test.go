package plan

import (
	"encoding/json"
	"fmt"
	"testing"
)

// idle builds one online-and-idle runner in a pool.
func idle(group, name string) Runner {
	return Runner{Name: name, Status: "online", Busy: false, Labels: []string{"self-hosted", group}}
}

// idleN returns n online-and-idle runners for a pool.
func idleN(group string, n int) []Runner {
	out := make([]Runner, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, idle(group, fmt.Sprintf("%s-%d", group, i)))
	}
	return out
}

func fleetValue(t *testing.T, res Result, key string) []string {
	t.Helper()
	v, ok := res.RunsOn[key]
	if !ok {
		t.Fatalf("no runs-on value for key %q", key)
	}
	labels, ok := v.([]string)
	if !ok {
		t.Fatalf("key %q is %#v, want a self-hosted label array", key, v)
	}
	return labels
}

func hostedValue(t *testing.T, res Result, key string) string {
	t.Helper()
	v, ok := res.RunsOn[key]
	if !ok {
		t.Fatalf("no runs-on value for key %q", key)
	}
	image, ok := v.(string)
	if !ok {
		t.Fatalf("key %q is %#v, want a hosted image string", key, v)
	}
	return image
}

// TestRouteUsesTheFleetWhileItHasCapacity: with every pool idle, every shard's
// runs-on is that pool's label array and nothing spills.
func TestRouteUsesTheFleetWhileItHasCapacity(t *testing.T) {
	runners := append(idleN("studio", 20), idleN("space", 20)...)
	res := Route(runners, Assignments("pull_request"), Reserve, false)

	for i := 1; i <= 4; i++ {
		got := fleetValue(t, res, fmt.Sprintf("studio-%d", i))
		if want := Pools["studio"].Labels; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("studio-%d = %v, want %v", i, got, want)
		}
		got = fleetValue(t, res, fmt.Sprintf("space-%d", i))
		if want := Pools["space"].Labels; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("space-%d = %v, want %v", i, got, want)
		}
	}
	if len(res.Spilled) != 0 {
		t.Errorf("spilled %v, want none when the fleet is idle", res.Spilled)
	}
}

// TestRouteHoldsBackTheReserve: with exactly reserve+1 studio runners idle,
// only one studio shard takes the fleet; the rest spill to macos-latest.
func TestRouteHoldsBackTheReserve(t *testing.T) {
	runners := idleN("studio", Reserve+1)
	assign := []Assignment{
		{Key: "studio-1", Pool: "studio", Shards: 1},
		{Key: "studio-2", Pool: "studio", Shards: 1},
		{Key: "studio-3", Pool: "studio", Shards: 1},
	}
	res := Route(runners, assign, Reserve, false)

	if got := fleetValue(t, res, "studio-1"); fmt.Sprint(got) != fmt.Sprint(Pools["studio"].Labels) {
		t.Errorf("studio-1 = %v, want the fleet", got)
	}
	for _, key := range []string{"studio-2", "studio-3"} {
		if got := hostedValue(t, res, key); got != "macos-latest" {
			t.Errorf("%s = %q, want macos-latest", key, got)
		}
	}
	if want := []string{"studio-2", "studio-3"}; fmt.Sprint(res.Spilled) != fmt.Sprint(want) {
		t.Errorf("spilled %v, want %v", res.Spilled, want)
	}
	if res.Available["studio"] != 0 {
		t.Errorf("available studio = %d, want 0 after one assignment", res.Available["studio"])
	}
}

// TestRouteSpillsAnEmptyPool: an empty pool hands every shard to the hosted
// image for that OS.
func TestRouteSpillsAnEmptyPool(t *testing.T) {
	res := Route(nil, Assignments("pull_request"), Reserve, false)
	for i := 1; i <= 4; i++ {
		if got := hostedValue(t, res, fmt.Sprintf("space-%d", i)); got != "ubuntu-latest" {
			t.Errorf("space-%d = %q, want ubuntu-latest", i, got)
		}
		if got := hostedValue(t, res, fmt.Sprintf("studio-%d", i)); got != "macos-latest" {
			t.Errorf("studio-%d = %q, want macos-latest", i, got)
		}
	}
	if got, want := len(res.Spilled), 8; got != want {
		t.Errorf("spilled %d shards, want %d", got, want)
	}
}

// TestRouteIgnoresBusyAndOfflineRunners: the count is online-and-idle.
func TestRouteIgnoresBusyAndOfflineRunners(t *testing.T) {
	runners := []Runner{
		{Name: "busy", Status: "online", Busy: true, Labels: []string{"self-hosted", "studio"}},
		{Name: "offline", Status: "offline", Busy: false, Labels: []string{"self-hosted", "studio"}},
	}
	res := Route(runners, []Assignment{{Key: "studio-1", Pool: "studio", Shards: 1}}, Reserve, false)
	if got := hostedValue(t, res, "studio-1"); got != "macos-latest" {
		t.Errorf("studio-1 = %q, want macos-latest when the only runners are busy or offline", got)
	}
}

// TestRouteSharesOnePoolBetweenLegs: two legs on one pool draw on a single
// dwindling count, so the second leg cannot see the runners the first took.
func TestRouteSharesOnePoolBetweenLegs(t *testing.T) {
	runners := idleN("studio", Reserve+3)
	assign := []Assignment{
		{Key: "first", Pool: "studio", Shards: 2},
		{Key: "second", Pool: "studio", Shards: 2},
	}
	res := Route(runners, assign, Reserve, false)
	if got := fleetValue(t, res, "first"); fmt.Sprint(got) != fmt.Sprint(Pools["studio"].Labels) {
		t.Errorf("first = %v, want the fleet (two runners idle after reserve)", got)
	}
	if got := hostedValue(t, res, "second"); got != "macos-latest" {
		t.Errorf("second = %q, want macos-latest (only one studio runner is left)", got)
	}
}

// TestRouteKeepsTheFleetOnPush: a push or the nightly schedule keeps the whole
// fleet whatever the API says, and nothing is consulted.
func TestRouteKeepsTheFleetOnPush(t *testing.T) {
	for _, event := range []string{"push", "schedule"} {
		if !FleetUnconditional(event) {
			t.Fatalf("FleetUnconditional(%q) = false, want true", event)
		}
		res := Route(nil, Assignments(event), Reserve, true)
		for i := 1; i <= 8; i++ {
			fleetValue(t, res, fmt.Sprintf("space-%d", i))
		}
		for i := 1; i <= 16; i++ {
			fleetValue(t, res, fmt.Sprintf("studio-%d", i))
		}
		if len(res.Spilled) != 0 {
			t.Errorf("event %q spilled %v, want none: the push keeps the fleet", event, res.Spilled)
		}
	}
}

// TestAssignmentsCarryTheMergeGateLegs: the merge group's darwin leg draws on
// the studio with its six shards, and the linux and windows legs are hosted.
func TestAssignmentsCarryTheMergeGateLegs(t *testing.T) {
	byKey := map[string]Assignment{}
	for _, a := range Assignments("merge_group") {
		byKey[a.Key] = a
	}
	for _, want := range []struct {
		key  string
		pool string
		host string
	}{{"merge-linux", "", "ubuntu-latest"}, {"merge-darwin", "studio", ""}, {"merge-windows", "", "windows-latest"}} {
		got, ok := byKey[want.key]
		if !ok {
			t.Fatalf("merge_group assignments have no %q", want.key)
		}
		if got.Pool != want.pool || got.Hosted != want.host {
			t.Errorf("%s = pool %q hosted %q, want pool %q hosted %q", want.key, got.Pool, got.Hosted, want.pool, want.host)
		}
	}
}

// TestParseRunnerPageReadsTheAPIShapeAndAFixture: the wire has label objects
// under {"runners":[...]}; a fixture may be a bare array. Both flatten to the
// same runners, and the routing reads the fixture end to end.
func TestParseRunnerPageReadsTheAPIShapeAndAFixture(t *testing.T) {
	api := []byte(`{"total_count":3,"runners":[
		{"name":"s1","status":"online","busy":false,"labels":[{"name":"self-hosted"},{"name":"studio"}]},
		{"name":"s2","status":"online","busy":true,"labels":[{"name":"self-hosted"},{"name":"studio"}]},
		{"name":"s3","status":"offline","busy":false,"labels":[{"name":"self-hosted"},{"name":"studio"}]}
	]}`)
	page, err := ParseRunnerPage(api)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 3 || len(page.Runners) != 3 {
		t.Fatalf("parsed %d runners (total %d), want 3", len(page.Runners), page.TotalCount)
	}
	if got := page.Runners[0].Labels; len(got) != 2 || got[1] != "studio" {
		t.Errorf("labels = %v, want [self-hosted studio]", got)
	}

	bare := []byte(`[{"name":"s1","status":"online","busy":false,"labels":[{"name":"self-hosted"},{"name":"studio"}]}]`)
	list, err := ParseRunners(bare)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("bare fixture parsed %d runners, want 1", len(list))
	}

	// The fixture routing: one idle studio runner, reserve two, so the studio
	// spills even though a runner is online.
	res := Route(list, []Assignment{{Key: "studio-1", Pool: "studio", Shards: 1}}, Reserve, false)
	if got := hostedValue(t, res, "studio-1"); got != "macos-latest" {
		t.Errorf("studio-1 = %q, want macos-latest from the fixture", got)
	}

	// A fixture with the reserve met: three idle studio runners route fleet.
	var many []map[string]any
	for i := 0; i < Reserve+1; i++ {
		many = append(many, map[string]any{
			"name": fmt.Sprintf("s%d", i), "status": "online", "busy": false,
			"labels": []map[string]string{{"name": "self-hosted"}, {"name": "studio"}},
		})
	}
	raw, _ := json.Marshal(many)
	list, err = ParseRunners(raw)
	if err != nil {
		t.Fatal(err)
	}
	res = Route(list, []Assignment{{Key: "studio-1", Pool: "studio", Shards: 1}}, Reserve, false)
	if got := fleetValue(t, res, "studio-1"); fmt.Sprint(got) != fmt.Sprint(Pools["studio"].Labels) {
		t.Errorf("studio-1 = %v, want the fleet from the fixture", got)
	}
}
