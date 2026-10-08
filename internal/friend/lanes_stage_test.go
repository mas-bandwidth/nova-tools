package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stage contract: a lane starts only on a job the stage wrote whole (the brief the
// stage names, the JOB.md and the checkout). A stage that wrote no brief starts no lane
// and raises one judgment; three stage failures finish the card FAIL with the stage
// reason; a prompt whose brief path is not there is refused before any harness runs; and
// a card whose gate names a go command on a host with no go is refused at stage
// (docs/SPEC-FRIEND.md, the stage contract).

// stageCardDir is a working directory with a job's stage part written: its checkout and
// its JOB.md, and its brief when brief is not empty.
func stageCardDir(t *testing.T, job, brief string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, JobsDir, job, "repo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, JobsDir, job, JobFile), []byte("# JOB: work c1\n"), 0o644))
	if brief != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", job, "BRIEF.md"), []byte(brief), 0o644))
	}
	return dir
}

// stageCard is a lane's card for a job under dir: its brief at the path the stage writes,
// never one the lane composes.
func stageCard(dir, job string) Card {
	return Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
}

// stageLoop is a loop the stage gate is stepped on its own: the record, the tell and the
// lane state are the rig's, and no daemon loop or socket runs.
func stageLoop(t *testing.T) (*rig, *loop) {
	t.Helper()
	r := newRig(t)
	r.d.Coordinator = "ada"
	r.d.m = &Machine{}
	return r, &loop{d: r.d, ctx: context.Background(), b: r.bus, lanes: &laneSet{given: map[string]bool{}, state: LaneState{}}}
}

// A stage that wrote no BRIEF.md starts no lane: the card stays in the queue and the
// failure is one judgment to the coordinator.
func TestAStageMissingItsBriefStartsNoLaneAndRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, nil, nil) // the queue holds it; no brief is written
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		r.d.Stage = func(context.Context, Packet) (string, error) { return "", nil }
		r.d.heldCards = []HeldCard{stagedCard("c1", "queued", "o/r", "dev")}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, JobsDir, "c1~15", "repo"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, JobsDir, "c1~15", JobFile), []byte("# JOB: work c1\n"), 0o644))

		r.run(t, 3)

		turns, _, _ := h.got()
		assert.Empty(t, turns, "no lane is handed a card whose stage wrote no brief")
		got := r.adaGot(t)
		require.Len(t, got, 1, "one judgment, not one a step: %v", got)
		assert.Contains(t, got[0], "stage failed for c1: ")
		assert.NoFileExists(t, filepath.Join(dir, "outbox", "c1~15", "REPORT.md"), "the card waits in the queue")
	})
}

// Three stage failures of one card finish it FAIL with the stage reason, never "wrote no
// report", and the card is set aside.
func TestThreeStageFailuresFinishTheCardFailWithItsReason(t *testing.T) {
	t.Parallel()
	job := "c1~15"
	dir := stageCardDir(t, job, "") // no brief: the stage never finishes
	h := stagedCard("c1", "working", "o/r", "dev")
	r, l := stageLoop(t)
	r.d.Dir = dir
	r.d.Stage = func(context.Context, Packet) (string, error) { return "", nil }
	r.d.heldCards = []HeldCard{h}

	now := t0
	for range StageFailLimit {
		l.stageLaneStep(now)
		now = now.Add(StageRetryEvery)
	}

	assert.Equal(t, StageFailLimit, l.lanes.stageFails[h.Job])
	assert.True(t, l.lanes.given[h.Job], "the card is set aside")
	raw, err := os.ReadFile(filepath.Join(dir, "outbox", h.Job, "REPORT.md"))
	require.NoError(t, err, "the card's REPORT.md is written")
	assert.True(t, strings.HasPrefix(string(raw), "Verdict: FAIL\n"), "the report: %s", raw)
	assert.Contains(t, string(raw), "stage failed for c1: ")
	assert.NotContains(t, string(raw), "wrote no report")
	got := r.adaGot(t)
	require.Len(t, got, 1, "one judgment: %v", got)
	assert.Contains(t, got[0], "stage failed for c1: ")
}

// A prompt whose brief path is not there is refused before any harness runs: the gate
// names the exact path the stage would have written. The same job with its brief written
// is handed over.
func TestAPromptNamingABriefThatIsNotThereIsRefused(t *testing.T) {
	t.Parallel()
	job := "c1~15"
	dir := stageCardDir(t, job, "") // the checkout and the JOB.md, no brief
	c := stageCard(dir, job)
	f, refused := StageGate(dir, c, job, "", func() bool { return true })
	require.True(t, refused)
	assert.Contains(t, f.Error(), "stage failed for c1: ")
	assert.Contains(t, f.Error(), c.Brief, "the refusal names the exact path the prompt would carry")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
	require.NoError(t, os.WriteFile(c.Brief, []byte("STATUS: nova-sprint card c1\n"), 0o644))
	_, refused = StageGate(dir, c, job, "STATUS: nova-sprint card c1\n", func() bool { return true })
	assert.False(t, refused, "a job staged whole is handed over")
}

// A card whose gate names a go command on a host with no go is refused at stage, and the
// card is handed back, never started; a host with go hands the same card over.
func TestAGoGateOnAHostWithNoGoIsRefusedAtStage(t *testing.T) {
	t.Parallel()
	job := "c1~15"
	brief := "STATUS: nova-sprint card c1, epoch 15\n\nDONE WHEN: go build ./... && go test ./...\n"
	dir := stageCardDir(t, job, brief)
	c := stageCard(dir, job)
	require.True(t, NeedsGo(brief), "the gate names a go command")
	f, refused := StageGate(dir, c, job, brief, func() bool { return false })
	require.True(t, refused)
	assert.Equal(t, "stage failed for c1: go is not on this host; the card's gates run on a bench", f.Error())
	_, refused = StageGate(dir, c, job, brief, func() bool { return true })
	assert.False(t, refused, "a host with go hands the card over")
}
