package land

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

// The class tests of card pr-record-follows-github: the rule that moves the
// PR record from what GitHub said, with no store. The deliveries are the
// webhook fixture already in the tree (internal/ghevent/testdata), decoded
// by the receiver's own decoder and read back as the stream entry a consumer
// sees.

var (
	shaOld = strings.Repeat("1", 40) // the fixture's head
	shaNew = strings.Repeat("2", 40)
)

const nowMS = int64(1790443327000)

// delivery is the fixture as action with head, as the stream entry id.
func delivery(t *testing.T, action, head, id string, merged bool) redis.XMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "ghevent", "testdata", "pull_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p["action"] = action
	pr := p["pull_request"].(map[string]any)
	pr["head"].(map[string]any)["sha"] = head
	pr["base"] = map[string]any{"ref": "dev"}
	pr["merged"] = merged
	raw, _ = json.Marshal(p)
	e, err := ghevent.Decode("pull_request", raw)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ghevent.Fields(e)
	if err != nil {
		t.Fatal(err)
	}
	return redis.XMessage{ID: id, Values: f}
}

func claimOf(t *testing.T, m redis.XMessage) PRClaim {
	t.Helper()
	c, ok := ClaimOf(m)
	if !ok {
		t.Fatalf("not a claim: %v", m.Values)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

// setOf is the plan's HSET as a map.
func setOf(p prHeadPlan) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(p.set); i += 2 {
		m[p.set[i].(string)] = p.set[i+1].(string)
	}
	return m
}

// TestPRRecordFollowsOpenedSynchronizeClosed: opened creates the record
// (stream and task from the branch's card, the head indexed), synchronize
// moves the head (ci and mergeable back to pending, the index follows),
// closed with merged=true is merged, a redelivery or an older entry is
// KEPT, and a delivery never touches the stream of a record it did not
// create.
func TestPRRecordFollowsOpenedSynchronizeClosed(t *testing.T) {
	t.Parallel()
	const key = "pr:nova-tools:42"

	opened := claimOf(t, delivery(t, "opened", shaOld, "1790443000000-0", false))
	if opened.N != 42 || opened.Branch != "johnny/ev-github-2685" || opened.Base != "dev" || opened.Source != ClaimDelivery {
		t.Fatalf("claim: %+v", opened)
	}
	p := planPRHead(key, map[string]string{}, opened, cardOf{ID: "ev-github-2685", Stream: "github"}, nowMS)
	got := setOf(p)
	if p.res.Outcome != "created" || p.addIdx != shaOld || p.remIdx != "" || got["head"] != shaOld || got["state"] != "open" ||
		got["stream"] != "github" || got["task"] != "ev-github-2685" || got["ci"] != "pending" || got["gh_head"] != shaOld ||
		got["gh_ev"] != "1790443000000-0" || got["repo"] != "mas-bandwidth/nova-tools" || got["branch"] != "johnny/ev-github-2685" {
		t.Fatalf("opened: %s %v", p.res.Line(), got)
	}
	if p = planPRHead(key, map[string]string{}, opened, cardOf{}, nowMS); setOf(p)["stream"] != "-" {
		t.Fatalf("no card: stream %q, want -", setOf(p)["stream"])
	}

	rec := map[string]string{"head": shaOld, "state": "open", "stream": "table", "task": "", "gh_ev": "1790443000000-0"}
	sync := claimOf(t, delivery(t, "synchronize", shaNew, "1790443327311-0", false))
	p = planPRHead(key, rec, sync, cardOf{ID: "ev-github-2685", Stream: "github"}, nowMS)
	got = setOf(p)
	if p.res.Outcome != "moved" || p.res.Prev != shaOld || p.addIdx != shaNew || p.remIdx != shaOld ||
		got["head"] != shaNew || got["head_prev"] != shaOld || got["ci"] != "pending" || got["mergeable"] != "" ||
		got["gh_head"] != shaNew || got["task"] != "ev-github-2685" {
		t.Fatalf("synchronize: %s %v", p.res.Line(), got)
	}
	if _, touched := got["stream"]; touched {
		t.Fatalf("synchronize rewrote the lander's stream: %v", got)
	}
	want := "PR HEAD pr:nova-tools:42 outcome=moved head=22222222 prev=11111111 state=open stream=table task=ev-github-2685"
	if p.res.Line() != want {
		t.Fatalf("line %q, want %q", p.res.Line(), want)
	}

	// The same entry twice, and an older one, change nothing.
	rec["gh_ev"] = "1790443327311-0"
	for _, id := range []string{"1790443327311-0", "1790443000000-5"} {
		again := sync
		again.EvID = id
		if p = planPRHead(key, rec, again, cardOf{}, nowMS); p.writes || p.res.Outcome != "kept" {
			t.Fatalf("ev %s: %s writes=%t", id, p.res.Line(), p.writes)
		}
	}

	rec["head"] = shaNew
	closed := claimOf(t, delivery(t, "closed", shaNew, "1790443400000-0", true))
	if !closed.Merged {
		t.Fatalf("closed merged: %+v", closed)
	}
	p = planPRHead(key, rec, closed, cardOf{}, nowMS)
	if p.res.Outcome != "same" || setOf(p)["state"] != "merged" || p.addIdx != shaNew || p.remIdx != "" {
		t.Fatalf("closed merged: %s %v", p.res.Line(), setOf(p))
	}
	unmerged := claimOf(t, delivery(t, "closed", shaNew, "1790443400000-0", false))
	if p = planPRHead(key, rec, unmerged, cardOf{}, nowMS); setOf(p)["state"] != "closed" {
		t.Fatalf("closed unmerged: %v", setOf(p))
	}
	rec["state"] = "merged"
	reopen := claimOf(t, delivery(t, "reopened", shaOld, "1790443500000-0", false))
	if p = planPRHead(key, rec, reopen, cardOf{}, nowMS); p.writes || p.res.Outcome != "kept" {
		t.Fatalf("a merged record's head moved: %s", p.res.Line())
	}
}

// TestRunnerClaimOrdersByRunID: a runner receipt of a pull_request run is a
// claim ordered by run id, so an older run's receipt never rewinds the head
// a newer run named; it never reopens a closed or merged record.
func TestRunnerClaimOrdersByRunID(t *testing.T) {
	t.Parallel()
	const key = "pr:nova-tools:4377"
	run := func(head, id string) PRClaim {
		return PRClaim{Repo: "mas-bandwidth/nova-tools", N: 4377, Head: head, Action: "run", Source: ClaimRunner,
			EvID: "1790443327311-0", RunID: id, Branch: "rowan/sprint-epoch"}
	}
	if err := run(shaNew, "x").Validate(); err == nil {
		t.Fatal("a runner claim with no run id was valid")
	}
	// The measured #4377: the record at 537c3e22, the receipt at ee0191c4.
	rec := map[string]string{"head": shaOld, "state": "open", "stream": "table"}
	p := planPRHead(key, rec, run(shaNew, "36258723969"), cardOf{}, nowMS)
	if got := setOf(p); p.res.Outcome != "moved" || got["head"] != shaNew || got["gh_run_id"] != "36258723969" || got["gh_src"] != "runner" {
		t.Fatalf("receipt: %s %v", p.res.Line(), got)
	}
	rec = map[string]string{"head": shaNew, "state": "open", "gh_run_id": "36258723969"}
	if p = planPRHead(key, rec, run(shaOld, "36258136552"), cardOf{}, nowMS); p.writes || p.res.Outcome != "kept" {
		t.Fatalf("an older run rewound the head: %s", p.res.Line())
	}
	rec["state"] = "closed"
	if p = planPRHead(key, rec, run(shaOld, "36258900000"), cardOf{}, nowMS); p.writes {
		t.Fatalf("a run reopened a closed record: %s", p.res.Line())
	}
}

// TestStaleHeadNamesBothHeads: the typed STALE line names the record's head,
// GitHub's and where GitHub's came from; no claim is no refusal.
func TestStaleHeadNamesBothHeads(t *testing.T) {
	t.Parallel()
	const key = "pr:nova-tools:4388"
	rec := map[string]string{"head": shaOld, "state": "open", "gh_head": shaNew, "gh_src": "runner", "gh_ev": "1790443567406-0"}
	err := StaleHead(key, rec)
	want := "STALE pr:nova-tools:4388 head=11111111 github=22222222 src=runner ev=1790443567406-0"
	if err == nil || err.Error() != want {
		t.Fatalf("stale: %v, want %s", err, want)
	}
	for name, r := range map[string]map[string]string{
		"same":     {"head": shaNew, "gh_head": shaNew},
		"no claim": {"head": shaOld},
		"merged":   {"head": shaOld, "gh_head": shaNew, "state": "merged"},
	} {
		if err := StaleHead(key, r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestBranchCardIDAndOrder: the branch spellings that name a card, and the
// stream id and run id orders.
func TestBranchCardIDAndOrder(t *testing.T) {
	t.Parallel()
	for branch, want := range map[string]string{
		"nova/quack-0926/gh-client-a2":   "gh-client",
		"rowan/pr-record-follows-github": "pr-record-follows-github",
		"refs/heads/rowan/x":             "x",
		"main":                           "",
		"rowan/":                         "",
	} {
		if got := BranchCardID(branch); got != want {
			t.Errorf("BranchCardID(%q) = %q, want %q", branch, got, want)
		}
	}
	if !evAfter("1790443327311-0", "1790443000000-9") || evAfter("1-1", "1-1") || !evAfter("1-10", "1-9") {
		t.Error("evAfter")
	}
	if !decimalAfter("36258723969", "36258136552") || decimalAfter("9", "10") || decimalAfter("x", "1") {
		t.Error("decimalAfter")
	}
}
