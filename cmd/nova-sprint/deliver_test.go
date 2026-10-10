package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// An installed sync pass must leave a stageable work brief to the daemon. The
// runner can see BRIEF.md as soon as it appears, so the daemon's job comes first.
func TestFriendSyncLeavesAStageableWorkBriefToTheDaemon(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	job := "s1-1.w1"
	dir := filepath.Join(root, "amy-working")
	brief := filepath.Join(dir, "inbox", job, "BRIEF.md")
	ta.ok("friend sync --root " + root)
	assert.NoFileExists(t, brief, "sync cannot publish work before the job is staged")

	held := friendCardsAnswer(t, ta, "amy")
	require.Len(t, held, 1)
	delivery := friend.Delivery{Dir: dir, Stage: func(_ context.Context, _ friend.Packet) (string, error) {
		assert.NoFileExists(t, brief)
		jobDir := friend.JobDir(dir, job)
		if err := os.MkdirAll(filepath.Join(jobDir, "repo"), 0o755); err != nil {
			return "", err
		}
		return "", os.WriteFile(filepath.Join(jobDir, friend.JobFile), []byte("# JOB: staged\n"), 0o644)
	}}
	o := delivery.One(context.Background(), held[0])
	require.NoError(t, o.Err)
	assert.Equal(t, friend.DeliverStaged, o.What)
	assert.FileExists(t, brief)
	assert.FileExists(t, filepath.Join(friend.JobDir(dir, job), friend.JobFile))
}

// A card whose WHO line prefers one friend, dealt by the tick to another (she is down), is
// delivered to the friend whose row holds it: the pin is a preference and the deal is the
// decision. deliver is the coordinator's hand version of her daemon's delivery, one DELIVER
// line per card, and a second pass finds it delivered.
func TestDeliverDeliversAWhoPinnedCardDealtToAnotherFriend(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "bob", "amy")
	ta.ok("friend down amy")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	require.Equal(t, sprint.FriendRow("bob"), c.Work[0].Row, "the deal placed amy's preferred card on bob's row")
	job := c.Work[0].ID
	brief := filepath.Join(root, "bob-working", "inbox", job, "BRIEF.md")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bob-working"), 0o755), "her working directory, as her daemon keeps it")

	dry := ta.ok("deliver bob --once --dry-run --stages runner --root " + root)
	assert.Contains(t, dry, "DELIVER "+job+" brief dry run: nothing written")
	assert.NoFileExists(t, brief, "a dry run writes nothing")

	out := ta.ok("deliver bob --once --stages runner --root " + root)
	assert.Equal(t, "DELIVER "+job+" brief\n", out)
	raw, err := os.ReadFile(brief)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(raw), "STATUS: nova-sprint card "+job+", "), "the brief friend cards answers her daemon: %q", raw)
	assert.Contains(t, string(raw), "WHO: friend amy", "delivered whatever its WHO line prefers")
	assert.NoDirExists(t, filepath.Join(root, "bob-working", "jobs", job), "a runner that stages its own jobs is handed the brief alone")
	assert.NoDirExists(t, filepath.Join(root, "amy-working", "inbox", job), "never to the friend it preferred")

	again := ta.ok("deliver bob --once --stages runner --root " + root)
	assert.Contains(t, again, "DELIVER "+job+" skipped delivered already")
}

// deliver refuses what it cannot do with the way forward, and a friend not on the table.
func TestDeliverRefusals(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend", "bob")
	for line, want := range map[string]string{
		"deliver": "wants one friend",
		"deliver bob --once --stages hands --root x":      "--stages wants daemon or runner",
		"deliver bob --every 0s --root " + root:           "--every wants a duration above zero",
		"deliver bob --once --mirrors rel --root " + root: "--mirrors wants an absolute path",
	} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, line)
		assert.Contains(t, errs, want, line)
	}
	code, _, errs := ta.do("deliver bob --once --root " + filepath.Join(root, "none"))
	assert.Equal(t, 1, code, "no working directory: a no, never a usage refusal")
	assert.Contains(t, errs, "is not a directory, and nothing is delivered outside it")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "zed-working"), 0o755))
	code, _, errs = ta.do("deliver zed --once --root " + root)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no friend zed on the friends table")
}
