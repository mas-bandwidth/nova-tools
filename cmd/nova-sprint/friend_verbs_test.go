package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The verbs friends lacked that machines have (the owner, 2026-10-04: "What else is like
// this? Missing verbs we need for friends, that machines already have"), on the twin.

// friendWidth is the friend's width as where --json shows it.
func friendWidth(ta *testApp, friend string) string {
	var w whereView
	ta.json("where", &w)
	return cellText(w.Tables[sprint.Friends][friend]["width"])
}

func TestFriendUpWidthSetsHerWidthAsFleetUpWidthSetsAMembers(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	assert.Equal(t, "8", friendWidth(ta, "amy"))
	out := ta.ok("friend up amy --width 3")
	assert.Contains(t, out, "FRIEND-UP OK amy held=false width=3")
	assert.Equal(t, "3", friendWidth(ta, "amy"))
	ta.ok("friend down amy")
	ta.ok("friend up amy")
	assert.Equal(t, "3", friendWidth(ta, "amy"), "a release without --width leaves it as it is")

	code, _, errs := ta.do("friend up amy --width 0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--width: a width wants a whole number from 1 to")
	code, _, _ = ta.do("friend down amy --width 3")
	assert.Equal(t, 2, code, "friend down takes no width")

	// friend sync sets her nova-config row's again, as fleet sync sets a member's
	ta.ok("friend sync")
	assert.Equal(t, "8", friendWidth(ta, "amy"))
}

// whereFriends is where --json's friends, by name.
func whereFriends(ta *testApp) map[string]store.FriendRow {
	var w struct {
		Friends []store.FriendRow `json:"friends"`
	}
	ta.json("where", &w)
	out := map[string]store.FriendRow{}
	for _, f := range w.Friends {
		out[f.Name] = f
	}
	return out
}

// A friend's machinery beats with her counts and her load, as a machine's beats with its
// load; the beat took her name alone and refused them (Johnny, 2026-10-04).
func TestFriendBeatTakesHerCountsAndLoadAsFleetBeatTakesALoad(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.pong("amy")
	out := ta.ok("friend beat amy --working 2 --queue 3 --width 4 --running s1-1.w1,s1-2.w1 --load 40%")
	assert.Contains(t, out, "FRIEND-BEAT OK amy at=")
	assert.Contains(t, out, " working=2 queue=3 width=4 load=40.0% running=s1-1.w1,s1-2.w1")
	f := whereFriends(ta)["amy"]
	require.NotNil(t, f.Report)
	assert.Equal(t, 2, *f.Report.Working)
	assert.Equal(t, 3, *f.Report.Queue)
	assert.Equal(t, 4, *f.Report.Width)
	assert.Equal(t, []string{"s1-1.w1", "s1-2.w1"}, f.Report.Running)
	assert.Equal(t, 40.0, f.Load)
	assert.Equal(t, 8, f.Width, "her width is the roster's, not her word")
	assert.Equal(t, sprint.Up, f.Status, "on her session's pong, never on the beat")

	// a beat with none reports none: the last one's counts are not kept
	ta.beatUp("amy")
	f = whereFriends(ta)["amy"]
	assert.Nil(t, f.Report)
	assert.Zero(t, f.Load)

	for _, bad := range []string{"--working -1", "--queue x", "--width 0", "--load lots"} {
		code, _, errs := ta.do("friend beat amy " + bad)
		assert.Equal(t, 2, code, bad)
		assert.Contains(t, errs, "wants", bad)
	}
	// and the server runs it with them
	_, _, why := workerVerb([]string{"friend", "beat", "amy", "--working", "2", "--queue", "3", "--width", "4", "--load", "40"})
	assert.Empty(t, why)
	_, _, why = workerVerb([]string{"friend", "beat", "amy", "--width", "0"})
	assert.NotEmpty(t, why)
}

// friend level on the twin: a card queued on one friend moves to another of her class with
// room, which friend sync delivers as a new job, and the queue file of the friend it left
// marks it taken.
func TestFriendLevelMovesAQueuedCardAndTheQueueFilesFollow(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 4, nil, "amy", "bob")
	ta.ok("friend up amy --width 1")
	ta.ok("friend up bob --width 1")
	ta.ok("friend down bob")
	ta.ok("tick") // amy alone: s1-1 and s1-2 ready, and she starts s1-1
	ta.startFriend("amy", 1)
	ta.ok("friend sync --root " + root)
	require.Equal(t, "queued", queueStates(t, root, "amy")["s1-2.w1"])
	ta.ok("friend up bob")
	// friend sync set her width back to the config's 8; at 1 her lane is her started card's,
	// and s1-2.w1 waits behind it (an unstarted card in a free lane of hers is hers to start)
	ta.ok("friend up amy --width 1")
	ta.beatUp("bob")

	assert.Contains(t, ta.dry("friend level --dry-run"), "FRIEND-LEVEL DRY-RUN up=amy,bob; nothing was changed")
	out := ta.ok("friend level")
	assert.Contains(t, out, "s1-2.w1 friend.amy:ready -> friend.bob:ready gen=2; moved=1 to bob(1) from amy(1)")
	assert.Contains(t, out, "FRIEND-LEVEL OK moved=1")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bob card=s1-2.w1 job=s1-2.w1.g2")
	assert.Equal(t, "taken", queueStates(t, root, "amy")["s1-2.w1"], "not hers to start any more")
	assert.Equal(t, "queued", queueStates(t, root, "bob")["s1-2.w1"], "ready on his row until he starts it")
	assert.Contains(t, ta.ok("friend level"), "FRIEND-LEVEL OK moved=0")
	ta.clean()
}

// friend level treats a one-shot friend as her width (one lane per unit of width, the owner
// 2026-10-10): bob, one-shot at width 2, takes both of amy's ready cards.
func TestFriendLevelRespectsOneShotDeliveryMode(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t)
	ta.a.tip = func(_ context.Context, _, _ string) (string, error) { return "", nil }
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "amy", Fields: map[string]string{"tiers": "flash", "width": "2", "mode": "batch"}}, "t")
	require.NoError(t, err)
	_, err = cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "bob", Fields: map[string]string{"tiers": "flash", "width": "2", "mode": "one-shot"}}, "t")
	require.NoError(t, err)

	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.beatUp("amy")
	ta.beatUp("bob")

	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("s1-%d", i)
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend")), 0o644))
		ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	}
	ta.ok("start")

	ta.ok("friend down bob")
	ta.ok("tick") // amy (batch, width 2) fills her room of 4, all ready, and starts 2
	ta.startFriend("amy", 2)
	ta.ok("friend sync --root " + root)

	ta.ok("friend up bob")
	ta.beatUp("bob")

	out := ta.ok("friend level")
	// bob's room is his width's (one-shot as batch), so both of amy's ready cards move
	assert.Contains(t, out, "FRIEND-LEVEL OK moved=2")
	assert.Contains(t, ta.ok("friend level"), "FRIEND-LEVEL OK moved=0")

	f := whereFriends(ta)
	assert.Equal(t, 2, f["amy"].Working)
	assert.Equal(t, 0, f["amy"].Ready)
	assert.Equal(t, 0, f["bob"].Working, "ready on his row until he starts them")
	assert.Equal(t, 2, f["bob"].Ready)
	ta.clean()
}

// stats fills a friend's run wall and report lag from her finish records, where they were
// dashes: her card reports no usage, so her run is her take to her report (REPORT.md's
// time, kept on the card as reported) and her lag her report to the sync that finished it.
func TestStatsTimesAFriendsRunFromHerReport(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick") // dealt at t0, and she starts it then
	ta.startFriend("amy", 1)
	ta.ok("friend sync --root " + root)
	taken := ta.a.now()
	ta.a.sleep(10 * time.Minute)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	report := filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "REPORT.md")
	require.NoError(t, os.Chtimes(report, taken.Add(8*time.Minute), taken.Add(8*time.Minute)))
	ta.ok("friend sync --root " + root)

	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, taken.Add(8*time.Minute).UTC().Format(time.RFC3339), c.Work[0].F(sprint.FieldReported))
	var ps sprint.PassStats
	ta.json("stats", &ps)
	require.Len(t, ps.Work, 1)
	assert.Equal(t, sprint.FriendRow("amy"), ps.Work[0].Member)
	assert.Equal(t, sprint.Measure{Median: 480, Max: 480, N: 1}, ps.Work[0].RunWall, "her take to her report")
	assert.Equal(t, sprint.Measure{Median: 120, Max: 120, N: 1}, ps.Work[0].ReportLag, "her report to the finish")
	ta.clean()
}

// On the twin: once amy has an ok attempt whose run (her take to her report) was an hour,
// a card she takes after it carries her deadline, three hours from its take, past the
// fleet's two, as where --json --cards shows it; one taken before it keeps the fleet's.
func TestAFriendsNextCardsDeadlineFollowsHerRunWall(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 3, map[string]string{"sprint/s1-1.w1.g1.e0": landHead}, "amy")
	ta.ok("friend up amy --width 1")
	ta.ok("tick") // s1-1 and s1-2 ready, s1-3 waits (friend sync sets her width back to 8); she starts s1-1
	ta.startFriend("amy", 1)
	ta.ok("friend sync --root " + root)
	taken := ta.a.now()
	ta.a.sleep(70 * time.Minute)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	report := filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "REPORT.md")
	require.NoError(t, os.Chtimes(report, taken.Add(time.Hour), taken.Add(time.Hour)))
	ta.ok("friend sync --root " + root)
	ta.beatUp("amy")
	ta.ok("tick")            // s1-3 dealt to her, s1-1's hour behind her
	ta.startFriend("amy", 2) // she starts s1-2 and s1-3 after her ok attempt

	var w whereView
	ta.json("where --cards", &w)
	limit := map[string]time.Duration{}
	for _, c := range w.Cards {
		assert.Equal(t, sprint.Working, c.State, c.ID)
		limit[c.ID] = c.Deadline.Sub(c.Since)
	}
	assert.Equal(t, map[string]time.Duration{"s1-2.w1": 3 * time.Hour, "s1-3.w1": 3 * time.Hour}, limit, "each started after her ok attempt: three times her one hour, from her start")
	ta.clean()
}

// A friend's beat proves her session only by naming a check her daemon's run asked: a bare
// time (outside the server's first hour), a nonce never asked, another run's answer, or
// the same answer twice proves nothing (recorded as a beat with no proof); the answer to
// a check her daemon's run asked within fifteen minutes makes her up, and only while her
// beats go on. It does not stop a caller who beats as her from asking and answering at
// once (TestTheBeatTrustsItsCallersActor).
func TestABareTimeOrAnUnaskedNonceNeverProves(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.ok("friend beat amy --pong " + ta.now.UTC().Format(time.RFC3339))
	assert.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status, "a time on her beat from anyone is no proof")
	out := ta.ok("friend beat amy --pong n1 --run r1")
	assert.Contains(t, out, "no_proof=")
	assert.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status, "a nonce never asked is no proof")

	out = ta.ok("friend beat amy --check n1 --run r1")
	assert.Contains(t, out, "check=n1")
	ta.step(time.Minute)
	ta.ok("friend beat amy --pong n1 --run r2")
	assert.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status, "an answer from another run is no proof")
	out = ta.ok("friend beat amy --pong n1 --run r1")
	assert.Contains(t, out, "proved=n1")
	assert.Equal(t, sprint.Up, whereFriends(ta)["amy"].Status, "the answer to the check her daemon asked")
	out = ta.ok("friend beat amy --pong n1 --run r1")
	assert.Contains(t, out, "no_proof=", "the same answer twice proves once")

	ta.step(sprint.BeatDeadline + time.Second)
	f := whereFriends(ta)["amy"]
	assert.Equal(t, sprint.Down, f.Status, "her beats stopped, so her proof stopped with them")
	assert.Contains(t, f.Evidence, "her beat stopped")
	ta.ok("friend beat amy")
	assert.Equal(t, sprint.Up, whereFriends(ta)["amy"].Status, "beating again, within fifteen minutes of the answer")
	ta.step(sprint.FriendProofLive)
	ta.ok("friend beat amy")
	assert.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status, "fifteen minutes on, the answer is out of its window")

	ta.ok("friend beat amy --check n2 --run r1")
	ta.step(sprint.CheckAnswerWithin + time.Second)
	out = ta.ok("friend beat amy --pong n2 --run r1")
	assert.Contains(t, out, "no_proof=", "an answer later than fifteen minutes after the ask proves nothing")
	_, _, why := workerVerb([]string{"friend", "beat", "amy", "--check", "n3", "--run", "r1", "--pong", "2026-10-06T17:00:00Z"})
	assert.Empty(t, why, "the server runs any --pong and its proof step says what it proved")
}

// The beat verb trusts its caller's actor (a worker verb is run as the friend it names,
// cmd/nova-sprint coordinator.go orActor): one beat that asks a check and answers it
// proves her session, from whoever sends it. The nonce rule stops a bare time and an
// answer to nothing asked, never a caller who speaks as her; that is the server's
// transport's to fence, not the proof's.
func TestTheBeatTrustsItsCallersActor(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "zhi")
	ta.ok("friend sync")
	out := ta.ok("friend beat zhi --check x1 --pong x1 --run r9")
	assert.Contains(t, out, "check=x1")
	assert.Contains(t, out, "proved=x1", "asked and answered in one beat: accepted")
	assert.Equal(t, sprint.Up, whereFriends(ta)["zhi"].Status)
}

// For an hour after the server starts, a beat's old --pong <time> (a daemon from before
// the nonces) counts as it did before, the time her proof; after the hour, and on a verb
// run with no server, it is a beat with no proof. So the server is adopted first and
// every daemon within the hour, and the adoption puts no friend down at once.
func TestAnOldPongCountsForAnHourAfterTheServerStarts(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	out := ta.ok("friend beat amy --pong " + ta.now.UTC().Format(time.RFC3339))
	assert.Contains(t, out, "no_proof=", "no server: a time is no proof")

	ta.a.serveStarted = ta.now
	ta.step(time.Minute)
	out = ta.ok("friend beat amy --pong " + ta.now.Add(-30*time.Second).UTC().Format(time.RFC3339))
	assert.Contains(t, out, "proved=legacy")
	assert.Equal(t, sprint.Up, whereFriends(ta)["amy"].Status, "in the server's first hour the old form counts as before")

	ta.step(sprint.LegacyPongGrace)
	out = ta.ok("friend beat amy --pong " + ta.now.UTC().Format(time.RFC3339))
	assert.Contains(t, out, "no_proof=", "after the hour a time is a beat with no proof")
	assert.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status)
}
