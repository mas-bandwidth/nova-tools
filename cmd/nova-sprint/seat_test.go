package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The coordinator's seat moves by a verb (Glenn, 2026-10-02 5:15 PM: "We need to
// make handover MORE SMOOTH."; 5:20 PM: "it needs to be able to be done, even if
// the old coordinator is asleep or out of credits" and "you can be given
// coordinator status, or you can take it (with my permission only)";
// nova-tools#5096 item 28).

// seatSprint is a sprint whose owner is glenn and whose coordinator is the test
// app's actor, coordinator.
func seatSprint(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	ta.ok("add --stream s1 --count 2")
	return ta
}

// holder is the sprint's coordinator as where --json carries it.
func (ta *testApp) holder() string {
	ta.t.Helper()
	var v struct {
		Coordinator string `json:"coordinator"`
	}
	ta.json("where", &v)
	return v.Coordinator
}

// The holder gives the seat: the log says so with the reason and who gave it,
// every coordinator verb then takes the new name and refuses the old one,
// where names the holder, and the leaving seat gets the handover as a receipt.
func TestTheHolderGivesTheSeat(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	out := ta.ok("coordinator rowan --reason 'stella holds the merges; rowan is back'")
	assert.Contains(t, out, "COORDINATOR OK holder=rowan from=coordinator by=coordinator given\n", out)
	assert.Contains(t, out, "HANDOVER seat=rowan since=", "the leaving seat's receipt is the handover:\n%s", out)
	assert.Equal(t, "rowan", ta.holder())

	log := ta.ok("log")
	assert.Contains(t, log, "seat: coordinator -> rowan: stella holds the merges; rowan is back, by coordinator\n", log)

	code, _, errs := ta.do("add --stream s2 --count 1 --one")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "the coordinator's alone: rowan, not coordinator", errs)
	ta.ok("add --stream s2 --count 1 --one --actor rowan")
	code, _, errs = ta.do("inbox --read")
	assert.Equal(t, 2, code, "the old holder moves no cursor: %s", errs)
	ta.ok("inbox --read --actor rowan")

	where := ta.ok("where")
	assert.Contains(t, where, "SPRINT TABLE  coordinator rowan\n", where)
	assert.NotContains(t, where, "taken", where)
	// inbox --json names the holder: the push through the server reads it there
	var in struct {
		Coordinator string `json:"coordinator"`
	}
	ta.json("inbox", &in)
	assert.Equal(t, "rowan", in.Coordinator)
}

// The owner gives the seat too; anyone else is refused and nothing changes; a
// seat change wants a reason, a name, and a name that is not the holder's.
func TestTheSeatIsTheHoldersOrTheOwnersToGive(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	for _, c := range []struct{ line, says string }{
		{"coordinator rowan --reason r --actor intruder", "the seat is the holder's to give: coordinator, not intruder"},
		{"coordinator rowan --actor coordinator", "--reason"},
		{"coordinator --reason r", "one name"},
		{"coordinator coordinator --reason r", "coordinator holds the seat already"},
		{"coordinator 'a b' --reason r", "a name wants letters, digits, _ and -"},
	} {
		code, out, errs := ta.do(c.line)
		assert.Equal(t, 2, code, "%s: %s%s", c.line, out, errs)
		assert.Contains(t, errs, c.says, "%s: %s", c.line, errs)
		assert.Equal(t, "coordinator", ta.holder(), "%s changed the seat", c.line)
	}
	assert.NotContains(t, ta.ok("log"), "seat", "a refused seat change wrote to the log")

	out := ta.ok("coordinator rowan --reason 'glenn moves the seat' --actor glenn --dry-run")
	assert.Equal(t, "COORDINATOR DRY-RUN holder=rowan from=coordinator by=glenn given; nothing was changed\n", out)
	assert.Equal(t, "coordinator", ta.holder(), "--dry-run moved the seat")
	out = ta.ok("coordinator rowan --reason 'glenn moves the seat' --actor glenn")
	assert.Contains(t, out, "COORDINATOR OK holder=rowan from=coordinator by=glenn given\n", out)
	assert.Contains(t, ta.ok("log"), "seat: coordinator -> rowan: glenn moves the seat, by glenn\n")
}

// The seat is taken by the new holder, with the owner's name: it works with the
// old holder doing nothing, the log says TAKEN loudly with the owner's name, the
// old holder has a note addressed to them in the inbox, and where says taken
// and when until the next handover is given.
func TestTheSeatIsTakenWithTheOwnersName(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	taken := ta.a.now().In(ta.a.zone()).Format("3:04 PM")
	out := ta.ok("coordinator rowan --take --approved-by glenn --reason 'stella is out of credits' --actor rowan")
	assert.Contains(t, out, "COORDINATOR OK holder=rowan from=coordinator by=rowan taken approved_by=glenn\n", out)
	assert.Equal(t, "rowan", ta.holder())
	assert.Contains(t, ta.ok("log"), "seat TAKEN: coordinator -> rowan, approved by glenn: stella is out of credits\n")

	var for_ *sprint.Group
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NSeatTaken {
			g := g
			for_ = &g
		}
	}
	require.NotNil(t, for_, "no seat TAKEN note in the inbox")
	assert.Equal(t, "coordinator", for_.To, "the note is addressed to the old holder")
	assert.Contains(t, ta.ok("inbox"), "for=coordinator", "the inbox shows who it is for")

	where := ta.ok("where")
	assert.Contains(t, where, "SPRINT TABLE  coordinator rowan (taken "+taken+")\n", where)
	var v struct {
		Seat struct {
			Holder     string `json:"holder"`
			Taken      bool   `json:"taken"`
			ApprovedBy string `json:"approved_by"`
		} `json:"seat"`
	}
	ta.json("where", &v)
	assert.True(t, v.Seat.Taken)
	assert.Equal(t, "glenn", v.Seat.ApprovedBy)

	// the next handover, given, clears the mark
	ta.ok("coordinator stella --reason 'stella woke' --actor rowan")
	where = ta.ok("where")
	assert.Contains(t, where, "SPRINT TABLE  coordinator stella\n", where)
	assert.NotContains(t, where, "taken", where)
}

// A take is refused without the owner's name, with another name than the
// owner's, by anyone but the name taking it, and on a sprint that names no
// owner; nothing changes and nothing is logged.
func TestATakeWithoutTheOwnersNameIsRefused(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	for _, c := range []struct{ line, says string }{
		{"coordinator rowan --take --reason r --actor rowan", "--take wants --approved-by <owner>"},
		{"coordinator rowan --take --approved-by rowan --reason r --actor rowan", "the sprint's owner is glenn, not rowan"},
		{"coordinator rowan --take --approved-by glenn --reason r --actor stella", "a seat is taken by the one taking it: rowan, not stella"},
		{"coordinator rowan --take --approved-by glenn --actor rowan", "--reason"},
	} {
		code, out, errs := ta.do(c.line)
		assert.Equal(t, 2, code, "%s: %s%s", c.line, out, errs)
		assert.Contains(t, errs, c.says, "%s: %s", c.line, errs)
		assert.Equal(t, "coordinator", ta.holder(), "%s changed the seat", c.line)
	}
	assert.NotContains(t, ta.ok("log"), "seat", "a refused take wrote to the log")

	// a sprint with no owner takes no take; NOVA_SPRINT_OWNER names one
	ta2 := newTestApp(t)
	ta2.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta2.do("coordinator rowan --take --approved-by glenn --reason r --actor rowan")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "the sprint names no owner", errs)
	env := ta2.a.getenv
	ta2.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_OWNER" {
			return "glenn"
		}
		return env(k)
	}
	ta2.ok("coordinator rowan --take --approved-by glenn --reason r --actor rowan")
	assert.Equal(t, "rowan", ta2.holder())
}

// A take's whole purpose is an away holder whose pushes are necessarily stale
// (down or out of credits): while pushes are armed, the take still moves the
// seat. Only the next holder's judgment nonce proof gates it, never the away
// holder's complete set (the reader finding on attempt 7).
func TestATakeStillWorksForAnAwayHolderWhosePushesAreStale(t *testing.T) {
	t.Parallel()
	const away = "take-away"
	ta, _ := pushProofSprint(t, away)
	ctx := context.Background()
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	st, err := ta.a.store(common{redis: "mem:0", actor: away})
	require.NoError(t, err)

	// the away holder's pong is two hours old and its observers never beat: a
	// normal move would be refused with a PUSH DOWN naming it
	old := ta.a.now().Add(-2 * time.Hour)
	require.NoError(t, writePush(ctx, st, sprint.PushRecord{Name: away, Harness: "opencode", Target: t.TempDir(), Nonce: "n1", Sent: old, Proven: old, PongOf: "n1"}))

	// the next holder's judgment nonce is proven
	next := "take-next"
	pushTests.Store(next, pushArmedOnly{})
	t.Cleanup(func() { pushTests.Delete(next) })
	require.NoError(t, writePush(ctx, st, sprint.PushRecord{Name: next, Harness: "opencode", Target: t.TempDir(), Nonce: "n1", Sent: ta.a.now(), Proven: ta.a.now(), PongOf: "n1"}))

	out := ta.ok("coordinator " + next + " --take --approved-by glenn --reason 'the holder is away' --actor " + next)
	assert.Contains(t, out, "COORDINATOR OK holder="+next+" from="+away+" by="+next+" taken approved_by=glenn", out)
	assert.Equal(t, next, ta.holder())
}

// handover prints what the next seat needs, from the store, in one screen.
func TestHandoverPrintsWhatTheNextSeatNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s1 --sentinel s1-stop")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 --count 1 --one")
	// b waits on the live s2-1: a need naming a dropped card is refused at
	// add, and drop refuses a card a waiting card still needs without
	// --cascade (docs/SPEC-SPRINT.md section 11), so no blocked judgment opens.
	ta.ok("add --stream s3 b --one --needs s2-1")
	ta.ok("fleet down m2")

	out := ta.ok("handover")
	for _, want := range []string{
		"HANDOVER seat=coordinator since=init\n",
		"STREAM s1 waiting=3 ready=1 working=0 review=0 merging=0 landed=0\n",
		"SENTINEL s1-stop stream=s1 held: 2 wait behind it (s1-",
		"MEMBER m2 held by coordinator\n",
		"DECISION ",
		" hold member m2 by coordinator: --return: its work begun is handed back now\n",
		"RULE Cards are admitted and released in waves of at least the fleet's width: add takes a directory, release names a wave, rework and drop answer a group; a single-card verb outside a judgment is the sign of doing it wrong.\n",
		"FIRST nova-sprint where\n",
		"FIRST nova-sprint inbox --wait --push seat\n",
		"FIRST read docs/SPEC-SPRINT.md, \"Handing over the seat\"\n",
		"JUDGMENT machine:stopped",
		"HANDOVER OK judgments=1 sentinels=1 members=1 decisions=1\n",
	} {
		assert.Contains(t, out, want, out)
	}
	assert.Contains(t, out, "ROUTES ", "the routes line is there, disabled or none:\n%s", out)
	assert.Less(t, strings.Count(out, "\n"), 60, "one screen:\n%s", out)

	var h struct {
		Seat      struct{ Holder string } `json:"seat"`
		Judgments []inboxJudgment         `json:"judgments"`
		Sentinels []struct {
			ID     string   `json:"id"`
			Behind []string `json:"behind"`
		} `json:"sentinels"`
		Decisions []struct {
			Verb   string `json:"verb"`
			Reason string `json:"reason"`
		} `json:"decisions"`
		First []string `json:"first"`
	}
	out = ta.ok("handover --json")
	require.NoError(t, json.Unmarshal([]byte(out), &h), out)
	assert.Equal(t, "coordinator", h.Seat.Holder)
	require.Len(t, h.Judgments, 1, "the stopped machine's")
	require.Len(t, h.Sentinels, 1)
	assert.Equal(t, "s1-stop", h.Sentinels[0].ID)
	assert.Len(t, h.Sentinels[0].Behind, 2)
	require.Len(t, h.Decisions, 1)
	assert.Equal(t, "hold", h.Decisions[0].Verb)
	assert.Len(t, h.First, 3)
}

// inbox --wait --push seat writes to the holder's inbox, ~/<holder>-working/
// inbox/sprint-judgments/, when ~/<holder>-working/inbox is there; a seat change
// moves the push to the new holder's inbox (every open judgment is pushed there
// once), and the note of a taken seat goes to the old holder's.
func TestThePushFollowsTheSeat(t *testing.T) {
	t.Parallel()
	ta, held := heldAndWaiting(t)
	home := t.TempDir()
	ta.a.home = func() (string, error) { return home, nil }
	for _, who := range []string{"coordinator", "rowan"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, who+"-working", "inbox"), 0o755))
	}
	ta.ok("init --owner glenn") // the owner set once, on a sprint that named none
	in := ta.interruptible()
	ta.atSleep(func(n int) {
		switch n {
		case 3:
			ta.ok("coordinator rowan --take --approved-by glenn --reason 'coordinator is asleep' --actor rowan")
			ta.ok("tick")
		case 7:
			in.now(t)
		}
	})
	out := ta.ok("inbox --wait --push seat --timeout 200ms")
	old := filepath.Join(home, "coordinator-working", "inbox", "sprint-judgments")
	next := filepath.Join(home, "rowan-working", "inbox", "sprint-judgments")
	assert.FileExists(t, filepath.Join(old, held.Notes[0]+".md"), out)
	assert.FileExists(t, filepath.Join(next, held.Notes[0]+".md"), "the open judgment follows the seat:\n%s", out)
	assert.Contains(t, out, "NOTE the seat is rowan's: pushing to "+next+"\n", out)
	var takenNote string
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NSeatTaken {
			takenNote = g.Notes[0]
		}
	}
	require.NotEmpty(t, takenNote)
	assert.FileExists(t, filepath.Join(old, takenNote+".md"), "the taken note is the old holder's:\n%s", out)
	assert.NoFileExists(t, filepath.Join(next, takenNote+".md"), out)

	// a holder whose ~/<holder>-working/inbox is not there: refused, naming it
	ta.ok("coordinator nobody --reason r --actor rowan")
	code, _, errs := ta.do("inbox --wait --push seat --timeout 200ms")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, filepath.Join(home, "nobody-working", "inbox"), errs)
}

// nova-sprint seat names each friend's daemon stamp and last beat age, and one
// ALARM for a stamp that is not the newest any friend beats and one for a beat
// older than the down bound, on the twin store (docs/SPEC-SPRINT.md,
// daemon-supervised-r-b.w8). The newest stamp is the one on the most recent
// beat, never the greatest on the roster: the friend that beat last is the
// updated one however its stamp sorts, and an empty stamp is unknown, never a
// drift (the reader's finding, daemon-supervised-r-b.w7). The stamp is kept on
// the beat's report and carried on her row beside load (FriendRow.DaemonVersion),
// so --json's friends read it there too (daemon-supervised-r-b.w8).
func TestSeatSaysEachDaemonsVersionAndAlarmsOnDriftAndADeadDaemon(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy", "bob", "cat", "dan")
	ta.ok("friend sync")
	pongAt := ta.now.UTC().Format(time.RFC3339)
	ta.ok("friend beat amy --daemon-version v9.0 --check chk1 --run r1")
	ta.ok("friend beat amy --daemon-version v9.0 --pong chk1 --run r1 --load 12.5")
	// bob beats later with the string-smaller stamp: the newest stamp is the one
	// on the latest beat, v10.0, so amy (the greatest string) is the one that
	// drifts and bob, who beat last, does not (the reader's finding,
	// daemon-supervised-r-b.w7).
	ta.now = ta.now.Add(10 * time.Second)
	ta.ok("friend beat bob --daemon-version v10.0")
	// dan's beat carries no stamp: an empty stamp is unknown, not a drift.
	ta.ok("friend beat dan")
	raw, found, err := ta.m.GetKey(context.Background(), "friend-beat:amy")
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, raw, `"daemon_version":"v9.0"`)
	assert.Contains(t, raw, `"pong":"`+pongAt+`"`)
	assert.Contains(t, raw, `"load":12.5`)
	assert.NotContains(t, raw, "no_proof", "the check this run asked was answered")
	amy := whereFriends(ta)["amy"]
	assert.Equal(t, 12.5, amy.Load)
	require.NotNil(t, amy.Report, "the stamp rides on the beat's report, beside load")
	assert.Equal(t, "v9.0", amy.DaemonVersion, "the daemon's stamp is on her row beside load")
	assert.False(t, amy.Proof.IsZero(), "the pong on the beat record is kept")

	out := ta.ok("seat")
	assert.Contains(t, out, "SEAT holder=coordinator epoch=0 generation=1\n")
	assert.Contains(t, out, "FRIEND friend=amy daemon_version=v9.0 last_beat_age=10s\n")
	assert.Contains(t, out, "FRIEND friend=bob daemon_version=v10.0 last_beat_age=0s\n")
	assert.Contains(t, out, "FRIEND friend=cat daemon_version=- last_beat_age=-\n")
	assert.Contains(t, out, "FRIEND friend=dan daemon_version=- last_beat_age=0s\n")
	assert.Contains(t, out, "ALARM friend=amy version drift: daemon_version=v9.0 newest=v10.0\n")
	assert.Contains(t, out, "ALARM friend=cat daemon down: no beat\n")
	assert.NotContains(t, out, "ALARM friend=bob", "the friend who beat last runs the newest stamp")
	assert.NotContains(t, out, "ALARM friend=dan", "an empty stamp is unknown, not a drift")
	assert.NotContains(t, out, "ALARM friend=cat version drift", "an empty stamp is unknown, not a drift")
	assert.Equal(t, 2, strings.Count(out, "\nALARM "), "amy drifts, cat is down")

	ta.now = ta.now.Add(sprint.MissedBeatsDown*sprint.BeatDeadline + time.Second)
	out = ta.ok("seat")
	assert.Contains(t, out, "ALARM friend=amy daemon down: last beat older than 45s\n")
	assert.Contains(t, out, "ALARM friend=bob daemon down: last beat older than 45s\n")
	assert.Contains(t, out, "ALARM friend=dan daemon down: last beat older than 45s\n")
	assert.Contains(t, out, "ALARM friend=amy version drift: daemon_version=v9.0 newest=v10.0\n")
	assert.NotContains(t, out, "ALARM friend=bob version drift")
	assert.NotContains(t, out, "ALARM friend=dan version drift")
	assert.Contains(t, out, "ALARM friend=cat daemon down: no beat\n")
	assert.Equal(t, 5, strings.Count(out, "\nALARM "), "amy drifts and is down, bob and dan are down, cat is down")

	ta.ok("friend beat amy --working 2")
	raw, found, err = ta.m.GetKey(context.Background(), "friend-beat:amy")
	require.NoError(t, err)
	require.True(t, found)
	assert.NotContains(t, raw, "daemon_version", "a beat without the flag replaces the record")
	f := whereFriends(ta)["amy"]
	require.NotNil(t, f.Report)
	assert.Equal(t, 2, *f.Report.Working)
	assert.Empty(t, f.Report.Window)
}
