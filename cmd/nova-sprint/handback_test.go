package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A dealt card handed back (docs/SPEC-SPRINT.md, a dealt card handed back).

func TestHandBackReturnsAnUnstartedCardToThePoolAndRetiresItsBrief(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 1, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	brief := filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md")
	_, err := os.Lstat(brief)
	require.NoError(t, err)
	ta.ok("friend up bob")
	ta.beatUp("bob")

	assert.Equal(t, "HANDBACK DRY-RUN from=amy cards=s1-1 all-unstarted=false; nothing was changed\n", ta.dry("handback s1-1 --from amy --reason 'eight ready cards are stranded' --dry-run"))
	out := ta.ok("handback s1-1 --from amy --reason 'eight ready cards are stranded' --root " + root)
	assert.Contains(t, out, "s1-1.w1 withdrawn gen=2")
	assert.Contains(t, out, "HANDBACK OK moved=1 refused=0")
	assert.Contains(t, out, "NOTE retired inbox/s1-1.w1 to inbox/retired/s1-1.w1")
	_, err = os.Lstat(brief)
	assert.True(t, os.IsNotExist(err), "the inbox brief is gone")
	retired, err := filepath.Glob(filepath.Join(root, "amy-working", "inbox", "retired", "s1-1.w1*"))
	require.NoError(t, err)
	require.NotEmpty(t, retired)

	ta.ok("tick")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1, "the same card, no second attempt")
	assert.Equal(t, sprint.FriendRow("bob"), c.Work[0].Row)
	assert.Equal(t, "1", c.Work[0].F("attempt"))
	assert.Equal(t, "3", c.Work[0].F("gen"))
	assert.Empty(t, c.Work[0].F(sprint.FieldTakeEnded))
	assert.Contains(t, c.Work[0].F(sprint.FieldFriendsLeft), "amy")
	ta.clean()
}

func TestAFriendHandsBackHerOwnCard(t *testing.T) {
	t.Parallel()
	ta, _ := takeApp(t, 1, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	ta.ok("friend up bob")
	ta.beatUp("bob")
	out := ta.ok("handback s1-1 --reason 'she has not started it' --actor friend.amy")
	assert.Contains(t, out, "HANDBACK OK moved=1 refused=0")
	ta.clean()
}

func TestHandBackRefusesAStartedLaneByName(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 2, nil, "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	ta.startFriend("amy", 1)
	code, _, errs := ta.do("handback s1-1 --from amy --reason 'too late'")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-1: s1-1.w1 has started: lane her start")
	assert.Contains(t, errs, "HANDBACK FAILED moved=0 refused=1")

	beginJob(t, root, "amy", "s1-2.w1")
	code, _, errs = ta.do("handback s1-2 --from amy --reason 'too late' --root " + root)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "lane jobs/s1-2.w1")
	ta.clean()
}

func TestHandBackAllUnstartedSkipsAStartedCard(t *testing.T) {
	t.Parallel()
	ta, _ := takeApp(t, 2, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	ta.ok("friend up bob")
	ta.beatUp("bob")
	out := ta.ok("handback --all-unstarted --from amy --reason 'the rest can wait'")
	assert.Contains(t, out, "NOTE friend amy keeps s1-1.w1: lane her start")
	assert.Contains(t, out, "HANDBACK OK moved=1 refused=0")
	ta.ok("tick")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-2", &c)
	require.NotEmpty(t, c.Work)
	assert.Equal(t, sprint.FriendRow("bob"), c.Work[0].Row)
	assert.Equal(t, "1", c.Primary.F("attempt"))
	ta.clean()
}

func TestHandBackUsage(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, _, errs := ta.do("handback s1-1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants --reason")
	code, _, errs = ta.do("handback s1-1 --all-unstarted --reason 'x'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "not both")

	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	code, _, errs = ta.do("handback s1-1 --reason 'x'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants --from friend.<name>")
	code, _, errs = ta.do("handback s1-1 --from amy --reason 'x' --actor intruder")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "handback is the friend's or the coordinator's")
	code, _, errs = ta.do("handback s1-1 --from amy --reason 'x'")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no friend amy on the friends table")
	ta.clean()
}

func TestRetireHandedBackRefusesWhatIsNotHerSprintBrief(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	job := filepath.Join(root, "amy-working", "inbox", "s1-1.w1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	brief := filepath.Join(job, "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1\n"), 0o644))
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	_, err := friend.RetireHandedBack("relative", "amy", "s1-1.w1", false, now)
	assert.Error(t, err)
	_, err = friend.RetireHandedBack(root, "amy", "s1-1.w1", true, now)
	assert.Error(t, err)
	_, err = friend.RetireHandedBack(root, "amy", "..", false, now)
	assert.Error(t, err)

	where, err := friend.RetireHandedBack(root, "amy", "s1-1.w1", false, now)
	require.NoError(t, err)
	assert.Equal(t, "inbox/retired/s1-1.w1", where)
	_, err = os.Lstat(brief)
	assert.True(t, os.IsNotExist(err))
	where, err = friend.RetireHandedBack(root, "amy", "s1-1.w1", false, now)
	require.NoError(t, err)
	assert.Empty(t, where, "a missing brief is not an error")

	other := filepath.Join(root, "amy-working", "inbox", "note")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "BRIEF.md"), []byte("# not a sprint card\n"), 0o644))
	_, err = friend.RetireHandedBack(root, "amy", "note", false, now)
	assert.Error(t, err)
	_, err = os.Lstat(filepath.Join(other, "BRIEF.md"))
	assert.NoError(t, err, "a brief that is not the sprint's stays")

	linkJob := filepath.Join(root, "amy-working", "inbox", "linked")
	require.NoError(t, os.Symlink(other, linkJob))
	_, err = friend.RetireHandedBack(root, "amy", "linked", false, now)
	assert.Error(t, err)
}
