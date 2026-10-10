package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// friendReadTwin is a twin store with s1-1, a flash card, finished by m1 and its read
// asked of friend amy (flash, one-shot) on her fleet row by the tick's ask, as the live
// sprint asks one; the root her working directory is under, and the read card's id.
// gen0 seeds today's shape: the read as the ask wrote it before it wrote a generation.
func friendReadTwin(t *testing.T, gen0 bool) (*testApp, string, string) {
	t.Helper()
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
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{
		Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "mode": "one-shot", "width": "1"},
	}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	root := t.TempDir()
	ta.ok("friend sync --root " + root) // roster only: she is down, so the card is not dealt to her as work
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-1.md"), []byte(passingBrief("s1-1: the flash card (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nAS A READ\nread the flash card\n")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 1")
	ta.ok("finish --as m1 s1-1.w1@1 --head " + landHead + " --report done")
	ta.beatUp("amy")
	ta.ok("tick")
	id := sprint.ReadCardID("s1-1", 1, "amy")
	rc := ta.fleetCard(id)
	require.NotNil(t, rc, "the read is asked of amy")
	require.Equal(t, sprint.FriendRow("amy"), rc.Row)
	if gen0 {
		st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
		require.NoError(t, err)
		res, err := st.Run(context.Background(), store.Step{Verb: "seed", Load: []string{sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			c := s.Fleet.Card(id)
			e := ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}, Unset: []string{"gen"}}
			return sprint.Plan{Units: []sprint.Unit{{Key: id, Stream: "s1", Changes: []sprint.Change{{Table: sprint.Fleet, Entry: e}}, Moved: id + " seeded at gen 0"}}}
		}})
		require.NoError(t, err)
		require.Empty(t, res.Refused)
		require.Equal(t, 0, ta.fleetCard(id).Int("gen"), "today's shape: a friend read at gen 0")
	}
	return ta, root, id
}

// fleetCard is the fleet table's record of the card, placed or kept.
func (ta *testApp) fleetCard(id string) *sprint.Card {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	cs, err := st.Records(context.Background(), sprint.Fleet, []string{id})
	require.NoError(ta.t, err)
	if len(cs) == 0 {
		return nil
	}
	return cs[0]
}

// packetReadLine is the read verb the packet of the read prints, with --ok and no finding.
func packetReadLine(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`run: nova-sprint (read --as \S+ \(--ok \| --broken\) \S+ --epoch \d+)`).FindStringSubmatch(out)
	require.Len(t, m, 2, "the packet prints the read verb that returns it:\n%s", out)
	return strings.Replace(m[1], "(--ok | --broken)", "--ok", 1)
}

// requireClosedOK says the friend's read is retired off her row with an ok verdict, and
// the next tick moves its primary on from review as a reader-row ok read moves a flash
// card: the one ok read it needs stands.
func requireClosedOK(t *testing.T, ta *testApp, id string) {
	t.Helper()
	rc := ta.fleetCard(id)
	require.NotNil(t, rc)
	require.False(t, rc.Placed(), "the read is retired off her row")
	require.Equal(t, "ok", rc.F("verdict"))
	require.Equal(t, sprint.Review, ta.primary("s1-1").Col)
	ta.ok("tick")
	require.NotEqual(t, sprint.Review, ta.primary("s1-1").Col, "her ok read moved the primary on")
}

// The packet of a read asked of a friend row names how it is returned: her outbox
// report, or the read verb; the line it prints, run as printed, stores the verdict,
// retires the read and moves the primary as a reader-row return does.
func TestAFriendRowReadsPacketNamesTheOutboxReturn(t *testing.T) {
	t.Parallel()
	ta, _, id := friendReadTwin(t, false)
	out := ta.ok("queue --as friend.amy")
	require.Contains(t, out, "report it: write outbox/"+id+"/REPORT.md")
	require.Contains(t, out, "PACKET "+id+" attempt=1 gen=1 ")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "CARD "+id) {
			require.NotContains(t, l, "finish:", "a read is never finished")
			require.Contains(t, l, "read: --ok|--broken "+id)
		}
	}
	ta.ok(packetReadLine(t, out))
	requireClosedOK(t, ta, id)
}

// A read already on a friend row at gen 0 (the shape the ask wrote before this change)
// is read and returned by the verb as it stands: no repair step is needed.
func TestAGenZeroFriendReadIsStillReturnable(t *testing.T) {
	t.Parallel()
	ta, _, id := friendReadTwin(t, true)
	out := ta.ok("queue --as friend.amy")
	require.Contains(t, out, "PACKET "+id+" attempt=1 gen=0 ")
	ta.ok(packetReadLine(t, out))
	requireClosedOK(t, ta, id)
}

// finish on a friend's read is refused with the remedy that returns it.
func TestFinishRefusesAReadWithTheOutboxRemedy(t *testing.T) {
	t.Parallel()
	for _, gen0 := range []bool{false, true} {
		ta, _, id := friendReadTwin(t, gen0)
		code, _, errs := ta.do("finish --as friend.amy " + id + "@1 --report done")
		require.NotEqual(t, 0, code)
		require.Contains(t, errs, "a read, not work")
		require.Contains(t, errs, "outbox/"+id+"/REPORT.md")
		require.Contains(t, errs, "read --as friend.amy (--ok | --broken) "+id)
		require.True(t, ta.fleetCard(id).Placed(), "the refusal changed nothing")
	}
}

// friend sync closes a friend's read, today's gen-0 shape and the live generation alike,
// from outbox/<card id>/REPORT.md, and the next tick accepts on her ok read; her QUEUE.json
// names that directory as the read's job.
func TestFriendSyncClosesAFriendRowReadFromItsOutboxReport(t *testing.T) {
	t.Parallel()
	for _, gen0 := range []bool{true, false} {
		ta, root, id := friendReadTwin(t, gen0)
		out := ta.ok("friend sync --root " + root)
		require.Contains(t, out, "FRIEND-READ DELIVERED friend=amy card="+id)
		require.FileExists(t, filepath.Join(root, "amy-working", "inbox", id, "BRIEF.md"))
		var q friendQueue
		b, err := os.ReadFile(filepath.Join(root, "amy-working", filepath.FromSlash(queueFile)))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &q))
		require.Len(t, q.Tasks, 1)
		require.Equal(t, id, q.Tasks[0].Job, "the read's job is its card id, the directory its report is read from")
		outboxReport(t, root, "amy", id, "Verdict: LAND\nHead: "+landHead+"\n")
		out = ta.ok("friend sync --root " + root)
		require.Contains(t, out, "FRIEND-READ finished friend=amy card="+id)
		requireClosedOK(t, ta, id)
	}
}

// A read card is a card: its job is a card's, its stored id, .g<gen> from 2 (friendJobOf),
// so a one-shot runner finds inbox/<job>/BRIEF.md where it finds a work card's.
func TestAReadCardsJobIsACardsJob(t *testing.T) {
	t.Parallel()
	id := sprint.ReadCardID("s1-1", 1, "amy")
	require.Equal(t, sprint.StoredID(id, 15), friendJobOf(sprint.Packet{Card: id, Kind: "read", Epoch: 15}))
	require.Equal(t, sprint.StoredID(id, 15)+".g2", friendJobOf(sprint.Packet{Card: id, Kind: "read", Epoch: 15, Gen: 2}))
	require.Equal(t, id, friendJobOf(sprint.Packet{Card: id, Kind: "read"}))
	require.Equal(t, sprint.StoredID("s1-1.w1", 15)+".g2", friendJobOf(sprint.Packet{Card: "s1-1.w1", Kind: "work", Epoch: 15, Gen: 2}))
}
