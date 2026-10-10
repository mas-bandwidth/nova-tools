package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stage contract: a lane starts only on a job the stage wrote whole (a readable
// regular brief, the JOB.md and a validated checkout). A stage that wrote no brief
// starts no lane and raises one judgment; three stage failures finish the card FAIL
// with the stage reason; a prompt whose brief path is not there is refused before any
// harness runs; a directory brief and a path that is not a checkout are refused; and
// a card whose gate names a go command, including on a continuation line, on a host
// with no go is refused at stage (docs/SPEC-FRIEND.md, the stage contract).

// writeCheckout is a git worktree checkout: a directory whose .git names an absolute gitdir.
func writeCheckout(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
	gitdir := filepath.Join(path, ".gitdir")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
}

// stageRecordText is a job's JOB.md: the checkout the stage wrote, and the brief path
// the record carries when brief is not empty.
func stageRecordText(checkout, brief string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# JOB: work c1, attempt 1\n\n")
	if checkout != "" {
		fmt.Fprintf(&b, "The staged checkout: %s (a git worktree of o/r at dev, abcdef, on branch sprint/c1).\n", checkout)
	}
	if brief != "" {
		fmt.Fprintf(&b, "The brief the stage wrote: %s\n", brief)
	}
	return b.String()
}

// stageCardDir is a working directory whose stage record names briefPath, which is not
// inbox/<job>/BRIEF.md. body empty leaves that file unwritten. The checkout is a git
// worktree, not an empty directory.
func stageCardDir(t *testing.T, job, body string) (dir, briefPath string) {
	t.Helper()
	dir = t.TempDir()
	checkout := filepath.Join(dir, JobsDir, job, "repo")
	writeCheckout(t, checkout)
	briefPath = filepath.Join(dir, "staged-brief", job, "BRIEF.md")
	require.NoError(t, os.WriteFile(filepath.Join(dir, JobsDir, job, JobFile), []byte(stageRecordText(checkout, briefPath)), 0o644))
	if body != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(briefPath), 0o755))
		require.NoError(t, os.WriteFile(briefPath, []byte(body), 0o644))
	}
	return dir, briefPath
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
	dir, _ := stageCardDir(t, job, "") // the record names a brief the stage did not write
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

// A prompt whose brief path is not there is refused before any harness runs. The path is
// the one the stage record carries, not one the lane composes. A directory at that path
// is not a brief, and a directory or a file in place of the checkout is not a checkout.
func TestAPromptNamingABriefThatIsNotThereIsRefused(t *testing.T) {
	t.Parallel()
	job := "c1~15"
	dir, briefPath := stageCardDir(t, job, "")
	composed := filepath.Join(dir, "inbox", job, "BRIEF.md")
	c := Card{ID: "c1", Brief: composed, Outbox: filepath.Join(dir, "outbox", job)}
	rec, ok := stageRecordOf(dir, job)
	require.True(t, ok)
	assert.Equal(t, briefPath, rec.Brief, "the prompt path is the stage record's")
	assert.NotEqual(t, composed, rec.Brief, "the lane does not compose the brief path")

	f, refused := StageGate(dir, c, job, "", func() bool { return true })
	require.True(t, refused)
	assert.Contains(t, f.Error(), "stage failed for c1: ")
	assert.Contains(t, f.Error(), briefPath, "the refusal names the path the record carries")
	assert.NotContains(t, f.Error(), composed, "a composed inbox path is not the refusal")

	require.NoError(t, os.MkdirAll(filepath.Dir(briefPath), 0o755))
	require.NoError(t, os.WriteFile(briefPath, []byte("STATUS: nova-sprint card c1\n"), 0o644))
	_, refused = StageGate(dir, c, job, "STATUS: nova-sprint card c1\n", func() bool { return true })
	assert.False(t, refused, "a job staged whole is handed over")

	carried := carryStagedBrief(dir, c)
	assert.Equal(t, briefPath, carried.Brief)
	text := CardText(LaneJob{Dir: filepath.Join(dir, JobsDir, job), Card: carried, Brief: "STATUS: nova-sprint card c1\n"}, 1, 1, "nova-bus send", "", "", "", nil)
	assert.Contains(t, text, "Its brief is "+briefPath)
	assert.NotContains(t, text, composed, "the prompt does not name a path the lane composed")

	require.NoError(t, os.Remove(briefPath))
	require.NoError(t, os.MkdirAll(briefPath, 0o755))
	f, refused = StageGate(dir, c, job, "", func() bool { return true })
	require.True(t, refused, "a directory is not a brief")
	assert.Contains(t, f.Error(), "not a readable regular file")

	require.NoError(t, os.RemoveAll(briefPath))
	require.NoError(t, os.WriteFile(briefPath, []byte("STATUS: nova-sprint card c1\n"), 0o644))
	checkout := filepath.Join(dir, JobsDir, job, "repo")
	require.NoError(t, os.RemoveAll(checkout))
	require.NoError(t, os.MkdirAll(checkout, 0o755))
	f, refused = StageGate(dir, c, job, "STATUS: nova-sprint card c1\n", func() bool { return true })
	require.True(t, refused, "an empty directory is not a checkout")
	assert.Contains(t, f.Error(), "the stage wrote no checkout")

	require.NoError(t, os.RemoveAll(checkout))
	require.NoError(t, os.WriteFile(checkout, []byte("not a checkout\n"), 0o644))
	_, refused = StageGate(dir, c, job, "STATUS: nova-sprint card c1\n", func() bool { return true })
	assert.True(t, refused, "a file is not a checkout")
}

// A card whose gate names a go command on a host with no go is refused at stage, and the
// card is handed back, never started; a host with go hands the same card over. The gate
// is the whole DONE WHEN, so a go command on a continuation line counts and one in a
// later section does not.
func TestAGoGateOnAHostWithNoGoIsRefusedAtStage(t *testing.T) {
	t.Parallel()
	job := "c1~15"
	brief := "STATUS: nova-sprint card c1, epoch 15\n\nDONE WHEN: the gate is green when\ngo test ./internal/friend/\n"
	dir, _ := stageCardDir(t, job, brief)
	c := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
	require.True(t, NeedsGo(brief), "a go command on a continuation line of the gate counts")
	require.True(t, NeedsGo("DONE WHEN: go build ./... && go vet ./...\n"), "a go command on the DONE WHEN line counts")
	require.False(t, NeedsGo("STATUS: nova-sprint card c1\n\nDONE WHEN: the prose is enough\n\nRULES: go test ./... is not this gate\n"), "a go command in a later section is not the gate")
	f, refused := StageGate(dir, c, job, brief, func() bool { return false })
	require.True(t, refused)
	assert.Equal(t, "stage failed for c1: go is not on this host; the card's gates run on a bench", f.Error())
	_, refused = StageGate(dir, c, job, brief, func() bool { return true })
	assert.False(t, refused, "a host with go hands the card over")
}

// A read is its own stage case (ReadStageGate): its staged files are its inbox BRIEF.md,
// the line friend sync writes for a held card. The read's lane clones the work under review
// itself, so a read needs no work JOB.md and no checkout, and requiring either would refuse
// every read. A held read whose BRIEF.md the stage did not write starts no lane, is counted
// as a stage failure, and raises one judgment; the read's own BRIEF.md clears it, and three
// such failures finish the read FAIL with the stage reason, never "wrote no report".
func TestAReadMissingItsBriefIsAStageFailure(t *testing.T) {
	t.Parallel()
	job := "frontier.r1.bob"
	dir := t.TempDir()
	read := HeldCard{Card: "frontier.r1", Job: job, Col: "working", Kind: "read", Brief: "WHO: friend bob\n\nread this\n"}
	r, l := stageLoop(t)
	r.d.Dir = dir
	r.d.Stage = func(context.Context, Packet) (string, error) { return "", nil }
	r.d.heldCards = []HeldCard{read}
	handed := Card{ID: read.Card, Outbox: filepath.Join(dir, "outbox", job), Brief: filepath.Join(dir, "inbox", job, "BRIEF.md")}

	_, refused := ReadStageGate(dir, handed, job)
	assert.True(t, refused, "the read's inbox BRIEF.md is not there")
	assert.False(t, l.stageHandsOver(handed), "a read with no staged BRIEF.md is not handed over")

	l.stageLaneStep(t0)
	assert.Equal(t, 1, l.lanes.stageFails[job], "the read's missing brief is a stage failure")
	assert.False(t, l.lanes.given[job], "the card is back in the queue, not finished")
	got := r.adaGot(t)
	require.Len(t, got, 1, "one judgment, not one a step: %v", got)
	assert.Contains(t, got[0], "stage failed for frontier.r1: ")
	assert.Contains(t, got[0], "BRIEF.md")

	// the read's own BRIEF.md is its staged file: a work JOB.md and a checkout are not
	// required of it, and a staged read clears the failure count
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", job, "BRIEF.md"), []byte(read.Brief), 0o644))
	_, refused = ReadStageGate(dir, handed, job)
	assert.False(t, refused)
	assert.True(t, l.stageHandsOver(handed), "a staged read is handed over")
	assert.NoFileExists(t, filepath.Join(dir, "jobs", job, JobFile), "a read needs no work JOB.md")
	l.stageLaneStep(t0.Add(StageRetryEvery))
	assert.Empty(t, l.lanes.stageFails, "a staged read clears its failure count")
	assert.False(t, l.lanes.given[job])

	// three stage failures of a read finish it FAIL with the stage reason, never "wrote no report"
	require.NoError(t, os.Remove(filepath.Join(dir, "inbox", job, "BRIEF.md")))
	now := t0.Add(2 * StageRetryEvery)
	for range StageFailLimit {
		l.stageLaneStep(now)
		now = now.Add(StageRetryEvery)
	}
	require.True(t, l.lanes.given[job], "the read is set aside after three stage failures")
	raw, err := os.ReadFile(filepath.Join(dir, "outbox", job, "REPORT.md"))
	require.NoError(t, err, "the read's REPORT.md is written")
	assert.True(t, strings.HasPrefix(string(raw), "Verdict: FAIL\n"), "the report: %s", raw)
	assert.Contains(t, string(raw), "stage failed for frontier.r1: ")
	assert.NotContains(t, string(raw), "wrote no report")
}

// A JOB.md that names neither a brief nor a checkout is the older record: its presence
// is the stage, so a later pass does not treat it as a missing file.
func TestAJobFileThatNamesNothingStaysStaged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := "a.w1~15"
	require.NoError(t, os.MkdirAll(JobDir(dir, job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(JobDir(dir, job), JobFile), []byte("# JOB: work a.w1, attempt 1\n"), 0o644))
	assert.True(t, Staged(dir, job))
}

// JOB.md alone must not suppress a retry after its brief or checkout disappeared.
func TestAPartialStagedJobIsRepairedAcrossPasses(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"brief", "checkout"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			g := testkit.Git(t, 1)
			g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
			env := stageEnv(t)
			gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/dev")
			r, l := stageLoop(t)
			h := stagedCard("c1", "working", "o/r", "dev")
			p, ok := PacketOf(h)
			require.True(t, ok)
			inbox := filepath.Join(r.d.Dir, "inbox", h.Job, "BRIEF.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(inbox), 0o755))
			require.NoError(t, os.WriteFile(inbox, []byte(h.Brief), 0o644))
			stager := &Stager{Dir: r.d.Dir, Env: env, URL: func(string) string { return g.Remote }}
			_, err := stager.Stage(context.Background(), p)
			require.NoError(t, err)
			assert.True(t, Staged(r.d.Dir, h.Job))
			checkout := filepath.Join(JobDir(r.d.Dir, h.Job), "repo")
			// A recorded brief outside the inbox reproduces a partially written stage.
			brief := filepath.Join(JobDir(r.d.Dir, h.Job), "BRIEF.md")
			require.NoError(t, os.WriteFile(brief, []byte(h.Brief), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(JobDir(r.d.Dir, h.Job), JobFile), []byte(stageRecordText(checkout, brief)), 0o644))
			if missing == "brief" {
				require.NoError(t, os.Remove(brief))
			} else {
				require.NoError(t, os.RemoveAll(checkout))
			}
			assert.False(t, Staged(r.d.Dir, h.Job))
			r.d.heldCards = []HeldCard{h}
			calls := 0
			r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
				calls++
				return stager.Stage(ctx, p)
			}
			l.stageLaneStep(t0) // no lane; one failure, one judgment, retry next pass
			assert.Equal(t, 1, l.lanes.stageFails[h.Job])
			assert.False(t, l.lanes.given[h.Job])
			l.stageStep([]HeldCard{h}, t0.Add(StageRetryEvery))
			r.d.stageWG.Wait()
			l.stageStep([]HeldCard{h}, t0.Add(2*StageRetryEvery))
			l.stageLaneStep(t0.Add(2 * StageRetryEvery))
			assert.Equal(t, 1, calls, "the incomplete record retries exactly once")
			assert.True(t, Staged(r.d.Dir, h.Job))
			assert.Empty(t, l.lanes.stageFails)
			assert.False(t, l.lanes.given[h.Job], "repaired job is not failed")
			assert.NoFileExists(t, filepath.Join(r.d.Dir, "outbox", h.Job, "REPORT.md"))
			assert.Len(t, r.adaGot(t), 1, "the missing file raises one judgment")
		})
	}
}
