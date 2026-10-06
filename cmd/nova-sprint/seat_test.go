package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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

// handover prints what the next seat needs, from the store, in one screen.
func TestHandoverPrintsWhatTheNextSeatNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s1 --sentinel s1-stop")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 --count 1 --one")
	ta.ok("drop s2-1 --reason 'obsolete: the tool went away'")
	ta.ok("add --stream s3 b --one --needs s2-1")
	ta.ok("fleet down m2")
	blocked := ta.group(sprint.NBlocked, "s3")

	out := ta.ok("handover")
	for _, want := range []string{
		"HANDOVER seat=coordinator since=init\n",
		"STREAM s1 waiting=3 ready=1 working=0 review=0 merging=0 landed=0\n",
		"SENTINEL s1-stop stream=s1 held: 2 wait behind it (s1-",
		"JUDGMENT " + blocked.ID,
		"    nova-sprint ack " + blocked.Notes[0],
		"MEMBER m2 held by coordinator\n",
		"DECISION ",
		" drop s2-1 by coordinator: obsolete: the tool went away\n",
		" hold member m2 by coordinator: --return: its work begun is handed back now\n",
		"RULE Cards are admitted and released in waves of at least the fleet's width: add takes a directory, release names a wave, rework and drop answer a group; a single-card verb outside a judgment is the sign of doing it wrong.\n",
		"FIRST nova-sprint where\n",
		"FIRST nova-sprint inbox --wait --push seat\n",
		"FIRST read docs/SPEC-SPRINT.md, \"Handing over the seat\"\n",
		"JUDGMENT machine:stopped",
		"HANDOVER OK judgments=2 sentinels=1 members=1 decisions=2\n",
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
	require.Len(t, h.Judgments, 2, "the stopped machine's and the blocked primary's")
	require.Len(t, h.Sentinels, 1)
	assert.Equal(t, "s1-stop", h.Sentinels[0].ID)
	assert.Len(t, h.Sentinels[0].Behind, 2)
	require.Len(t, h.Decisions, 2)
	assert.Equal(t, "drop", h.Decisions[0].Verb)
	assert.Equal(t, "obsolete: the tool went away", h.Decisions[0].Reason)
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

// The handover is a restart checkpoint (handover-is-a-restart-checkpoint.w4;
// Stella's nova-sprint review, item 5): a cold coordinator recovers the next
// safe actions from the generated handover alone, with no hand-written ledger
// beside it. It carries the source and config revisions, the epoch and the
// seat's generation, each open card's reads, gate, branch and head and the
// remedies already tried, the install receipts, and every place that names the
// holder, pending or reconciled. A configuration refresh that writes the old
// name into the key does not undo the accepted handover, and an old holder's
// move or a stale retry of the owner's is refused.
func TestHandoverCarriesRevisionsEvidenceAndLineage(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	home := t.TempDir()
	ta.a.home = func() (string, error) { return home, nil }
	ta.ok("init --readers reader-a,reader-b --members m1 --owner glenn")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "tests red: TestNilMap")
	ta.ok("rework s1-1 --fix 'handle the nil map'")
	ta.deal(2)
	ta.ok("take --as m1 --limit 10")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	for _, c := range q.Cards {
		ta.ok("finish --as m1 " + c.ID + "@" + strconv.Itoa(c.Gen) + " --head abc1234 --branch sprint/" + c.ID)
	}
	ta.ok("ask --limit 10")
	ta.ok("read --as reader-a --ok --limit 10")

	// the holder gives the seat: generation 2
	ta.ok("coordinator rowan --reason 'rowan takes the night'")
	// a configuration refresh writes the old name into the key
	require.NoError(t, ta.m.SetCoordinator(context.Background(), "coordinator"))
	code, _, errs := ta.do("coordinator stella --reason 'mine again'")
	assert.Equal(t, 2, code, "the old holder moved the seat: %s", errs)
	assert.Contains(t, errs, "the seat is the holder's to give: rowan, not coordinator", errs)
	code, _, errs = ta.do("coordinator stella --generation 1 --reason 'retry of the morning' --actor glenn")
	assert.Equal(t, 2, code, "a stale retry moved the seat: %s", errs)
	assert.Contains(t, errs, "the seat is at generation 2, not 1: it moved after this was decided", errs)
	assert.NotContains(t, ta.ok("log"), "stella", "a refused move wrote to the log")

	st, err := ta.a.store(common{redis: "mem:0", actor: "rowan"})
	require.NoError(t, err)
	src := handoverSources{
		Source: "20261006120000-abc123def456",
		Config: func(context.Context) (configSide, error) {
			return configSide{Coordinator: "coordinator", Revs: map[string]int64{"route": 12, "sprint": 7}}, nil
		},
		Applied: func(context.Context, *store.Store) (map[string]int64, error) {
			return map[string]int64{"route": 11, "sprint": 7}, nil
		},
		Units: func() (string, []sprint.UnitState, error) {
			states, err := sprint.CheckUnits(home, "linux", sprint.UnitKinds)
			return home, states, err
		},
	}
	h, out, err := ta.a.handoverFrom(context.Background(), st, src)
	require.NoError(t, err)
	for _, want := range []string{
		"SEAT holder=rowan generation=2 epoch=",
		"REVISION source=20261006120000-abc123def456 config=route:12,sprint:7 applied=route:11,sprint:7 pending=route\n",
		"OWNER record names=rowan reconciled: the accepted handover, generation 2\n",
		"OWNER key names=coordinator pending: the key names coordinator and the record rowan",
		"; run: nova-sprint seat --repair --reason <text>\n",
		"OWNER config names=coordinator pending: nova-config's sprint row names coordinator: an apply of it is held (APPLY HELD) and does not move the seat",
		"; run: nova-config sprint set --coordinator rowan\n",
		"OWNER wake names=- pending: rowan has no push target recorded",
		"CARD s1-1 stream=s1 state=",
		" attempt=2 branch=sprint/s1-1.w2 head=abc1234 reads=reader-a:ok",
		"TRIED s1-1 attempt=1 failed: tests red: TestNilMap\n",
		"TRIED s1-1 attempt=2 fix: handle the nil map; ok\n",
		"INSTALL server missing; run: nova-sprint install server\n",
		"INSTALLS installed=0 of 9 dir=" + home + "\n",
		"NEXT nova-sprint seat --repair --reason <text>\n",
		"NEXT nova-config sprint set --coordinator rowan\n",
		"NEXT nova-config apply --kind route (revision 12 is not applied; the store has 11)\n",
		"NEXT nova-sprint coordinator <name> --generation 2 --reason <text> (the seat moves only from generation 2)\n",
	} {
		assert.Contains(t, out, want, out)
	}
	assert.Equal(t, "rowan", h.Seat.Holder, "the record is the seat, not the key a refresh wrote")
	assert.Equal(t, uint64(2), h.Seat.Generation)
	assert.Equal(t, []string{"route"}, h.Revisions.Pending)
	var lineage []string
	for _, c := range h.Cards {
		if c.ID == "s1-1" {
			for _, a := range c.Lineage {
				lineage = append(lineage, fmt.Sprintf("%d %s %s", a.Attempt, a.Result, a.Fix))
			}
		}
	}
	assert.Equal(t, []string{"1 failed ", "2 ok handle the nil map"}, lineage, "the remedies already tried")

	// a cold coordinator with the store alone: the config side is unread and
	// says how to read it; the next lines are there
	var cold handoverView
	ta.json("handover --actor rowan", &cold)
	assert.Equal(t, "rowan", cold.Seat.Holder)
	assert.Contains(t, cold.Revisions.ConfigUnread, "--pg", "%+v", cold.Revisions)
	assert.Contains(t, cold.Next, "nova-sprint seat --repair --reason <text>")
	assert.Contains(t, cold.Next, "nova-sprint coordinator <name> --generation 2 --reason <text> (the seat moves only from generation 2)")

	// the line the handover printed moves the seat; read again after, it is stale
	ta.ok("seat --repair --reason 'the refresh wrote the old name' --actor rowan")
	ta.ok("coordinator stella --generation 2 --reason 'stella woke' --actor rowan")
	code, _, errs = ta.do("coordinator rowan --generation 2 --reason 'the same line again' --actor glenn")
	assert.Equal(t, 2, code, "the printed line moved the seat twice: %s", errs)
	assert.Contains(t, errs, "the seat is at generation 3, not 2", errs)
}
