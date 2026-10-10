package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// TestAOneShotFriendRunsAReadCardFromItsBrief pins the read card's delivery to a friend
// (docs/SPEC-SPRINT.md section 6, "A read is a consumer card"): a one-shot friend whose
// roles name reader, with no session (no AGENTS.md, nothing of the sprint's but her inbox),
// is dealt a read card by the tick, and friend sync delivers it as a card: inbox/<job>/BRIEF.md
// under the job a work card of hers would have, whole (what a read is, the head, how to
// read, how to finish, the card verbatim). Her runner's REPORT.md at outbox/<job>/ whose
// first line is Verdict: LAND closes the read ok, and the primary is acceptable on it.
func TestAOneShotFriendRunsAReadCardFromItsBrief(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	home, prev := t.TempDir(), ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return prev(k)
	}
	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]config.Row, error) {
		return cfg.List(ctx, config.KindFriend)
	}
	for _, f := range []config.Row{
		{Name: "fred", Fields: map[string]string{"slots": "2", "tiers": "flash", "mode": "one-shot", "width": "32", "roles": "builder,reader"}},
		{Name: "alex", Fields: map[string]string{"slots": "2", "tiers": "flash", "mode": "one-shot", "width": "8", "roles": "builder"}},
	} {
		_, err := cfg.Insert(context.Background(), config.KindFriend, f, "t")
		require.NoError(t, err)
	}
	ta.a.tip = tipIs(t, landHead)
	ta.ok("init --readers reader-a --members m1:8")
	ta.ok("set --read-cards on")
	root := t.TempDir()
	ta.ok("friend sync --root " + root) // roster only: they stay down, so the card is the machine's work

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-1.md"), []byte(passingBrief("s1-1: the flash card (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nHOW THIS CARD IS JUDGED\nthe flag is reachable from the verb\n")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 1")
	ta.ok("finish --as m1 s1-1.w1@1 --report done --head " + landHead)
	ta.beatUp("fred")
	ta.beatUp("alex")
	ta.ok("tick")

	var card cardView
	ta.json("card s1-1", &card)
	require.Len(t, card.Reads, 1, "one read card, dealt")
	rc := card.Reads[0]
	require.Equal(t, sprint.FriendRow("fred"), rc.Row, "the reader, never alex, a builder alone")

	out := ta.ok("friend sync --root " + root)
	require.Contains(t, out, "FRIEND-READ DELIVERED friend=fred card="+rc.ID)
	inbox := filepath.Join(root, "fred-working", "inbox")
	entries, err := os.ReadDir(inbox)
	require.NoError(t, err)
	job := ""
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), rc.ID) {
			job = e.Name()
		}
	}
	require.NotEmpty(t, job, "her inbox holds the read card: %v", entries)
	require.Equal(t, friendJobOf(sprint.Packet{Card: rc.ID, Kind: "read", Epoch: epochOf(t, ta), Gen: 1}), job, "the job a card of hers would have")
	text, err := os.ReadFile(filepath.Join(inbox, job, "BRIEF.md"))
	require.NoError(t, err)
	for _, want := range []string{
		"STATUS: nova-sprint read card " + rc.ID,
		"commit nothing, push nothing",
		"outbox/" + job + "/REPORT.md whose first line is Verdict: LAND or Verdict: HOLD",
		"# This card is a READ",
		"- repository: mas-bandwidth/nova-tools",
		"- head: " + landHead,
		"## How to read",
		"git checkout --detach " + landHead,
		"HOW THIS CARD IS JUDGED",
		"## How to finish",
		"a HOLD names each defect",
		"## The card under review: s1-1, attempt 1, verbatim",
		"the flag is reachable from the verb",
	} {
		require.Contains(t, string(text), want)
	}

	outboxReport(t, root, "fred", job, "Verdict: LAND\nHead: "+landHead+"\n\nthe flag is reachable; go test ./... green\n")
	out = ta.ok("friend sync --root " + root)
	require.Contains(t, out, "FRIEND-READ finished friend=fred card="+rc.ID)
	ta.json("card s1-1", &card)
	require.Len(t, card.Reads, 1)
	require.Equal(t, "ok", card.Reads[0].F("verdict"), "her LAND closed the read ok")
}

// epochOf is the sprint's epoch, as where says it.
func epochOf(t *testing.T, ta *testApp) uint64 {
	t.Helper()
	var w struct {
		Epoch uint64 `json:"epoch"`
	}
	ta.json("where", &w)
	return w.Epoch
}

// TestAReadAskedTheOldWayKeepsItsInboxPath pins the install with live reads (PR 5392's cold
// read, finding 1): a friend's read asked before read cards (no read_deadline: the friends'
// read ask's card) is delivered, queued and closed at inbox/<card id> and outbox/<card id>
// at every epoch, as it was, so friend sync finds the BRIEF.md already there and delivers
// nothing again; a read card (with its read_deadline) has a card's job.
func TestAReadAskedTheOldWayKeepsItsInboxPath(t *testing.T) {
	t.Parallel()
	id := sprint.ReadCardID("dep-postgres-b", 3, "zhi")
	old := &sprint.Card{ID: id, Row: sprint.FriendRow("zhi"), Col: sprint.Working, Fields: map[string]string{"kind": "read", "primary": "dep-postgres-b", "attempt": "3", "reader": "zhi", "gen": "1"}}
	p := sprint.PacketOf("t-", 15, old, nil, nil, nil)
	require.Equal(t, id, friendJobOf(p), "an old read keeps its card id as its job")
	card := &sprint.Card{ID: id, Row: old.Row, Col: old.Col, Fields: map[string]string{"kind": "read", "primary": "dep-postgres-b", "attempt": "3", "reader": "zhi", "gen": "1", sprint.FieldReadCard: "1"}}
	require.Equal(t, sprint.StoredID(id, 15), friendJobOf(sprint.PacketOf("t-", 15, card, nil, nil, nil)), "a read card has a card's job")
}
