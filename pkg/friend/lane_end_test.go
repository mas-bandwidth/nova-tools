package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane's end is a finish (the owner, 2026-10-05: "Now let's look at friends. Are they
// actually doing work?"; six one-shot runs ended overnight with no REPORT.md and their
// cards stayed working for up to 14 hours).

// fakeOpenCode is a harness on the friend's PATH, as opencode answers a lane: `session
// list` shows ses_1 once a seed has run, a seed run answers ready, and a card turn prints
// one line and then does what FAKE_MODE says: exit 3 with nothing written, or write the
// friend's REPORT.md and RESULT.md into FAKE_OUT and exit 0.
const fakeOpenCode = `#!/bin/sh
if [ "$1" = session ]; then
  if [ -f "$FAKE_STATE/opened" ]; then printf '[{"id":"ses_1","directory":"%s","updated":1}]\n' "$FAKE_DIR"; else echo '[]'; fi
  exit 0
fi
case " $* " in
*" --session "*) ;;
*) touch "$FAKE_STATE/opened"; echo ready; exit 0 ;;
esac
echo "working on the card"
case "$FAKE_MODE" in
exit) exit 3 ;;
report)
  mkdir -p "$FAKE_OUT"
  printf 'Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n\nthe friend did it\n' > "$FAKE_OUT/REPORT.md"
  echo done > "$FAKE_OUT/RESULT.md"
  exit 0 ;;
esac
`

// laneEndFixture is a friend's working directory holding cards each delivered with a brief
// whose STATUS line names its branch, as friend sync writes it.
func laneEndFixture(t *testing.T, jobs ...string) string {
	t.Helper()
	dir := t.TempDir()
	q := Queue{}
	for _, job := range jobs {
		id, _, gen, ok := ParseJob(job)
		require.True(t, ok, job)
		q.Tasks = append(q.Tasks, Task{ID: id, Gen: gen, State: "queued"})
		in := filepath.Join(dir, "inbox", job)
		require.NoError(t, os.MkdirAll(in, 0o755))
		brief := "STATUS: nova-sprint card " + id + ", epoch 15, attempt 1; push your work to the branch sprint/" + id + ".g1.e15; when done, write outbox/" + job + "/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n"
		require.NoError(t, os.WriteFile(filepath.Join(in, "BRIEF.md"), []byte(brief), 0o644))
	}
	require.NoError(t, write(filepath.Join(dir, filepath.FromSlash(QueueFile)), q))
	return dir
}

// finishes is the finish verbs a lane sent to the sprint server.
type finishes struct {
	mu   sync.Mutex
	sent [][]string
}

func (f *finishes) finish(_ context.Context, argv []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, argv)
	return nil
}

func (f *finishes) got() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.sent...)
}

// pathRig is the lane rig over the fake opencode in its own bin directory, the turn's mode
// and the card's outbox handed to it in its environment.
func pathRig(t *testing.T, dir, mode, outbox string) (*rig, *LaneState, *finishes) {
	t.Helper()
	bin, state := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "opencode"), []byte(fakeOpenCode), 0o755))
	r, st := laneRig(t, &lanesHarness{dir: dir, active: map[string]int{}}, 1)
	// the child's PATH leads with the fake; the program is named by its path there, for
	// os/exec looks a bare name up on this process's PATH, never the child's
	r.d.Deliver = &OpenCode{Dir: dir, Program: filepath.Join(bin, "opencode"),
		Run: envExec("PATH="+bin+":"+os.Getenv("PATH"), "FAKE_STATE="+state, "FAKE_DIR="+dir, "FAKE_MODE="+mode, "FAKE_OUT="+outbox)}
	f := &finishes{}
	r.d.Finish = f.finish
	return r, st, f
}

func TestALaneThatEndsWithNoReportFinishesItsCardFailed(t *testing.T) {
	t.Parallel()

	// a run that exits with no report: after its turns the lane writes the report naming
	// the exit and the wall, and sends the failed finish as the friend's row
	t.Run("exit", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := laneEndFixture(t, "c1~15")
			out := filepath.Join(dir, "outbox", "c1~15")
			r, state, f := pathRig(t, dir, "exit", out)
			r.run(t, 30)

			report, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
			require.NoError(t, err, strings.Join(r.records, "\n"))
			assert.True(t, strings.HasPrefix(string(report), "Verdict: FAIL\n\nnova-friend lane 1 of bob finished card c1: the run exited 3 with no REPORT.md, wall "), string(report))
			assert.Contains(t, string(report), ", after 2 turns; no pushed head found.")
			sent := f.got()
			require.Len(t, sent, 1, "one finish")
			assert.Equal(t, []string{"finish", "--as", "friend.bob", "c1@1", "--epoch", "15", "--failed", "--branch", "sprint/c1.g1.e15", "--report"}, sent[0][:10])
			assert.True(t, strings.HasPrefix(sent[0][10], "friend bob FAIL: nova-friend lane 1 of bob finished card c1: the run exited 3 with no REPORT.md"), sent[0][10])
			assert.Empty(t, state.Started, "the card is no longer started")
			assert.Equal(t, []string{"c1~15"}, state.GivenUp)
			assert.Contains(t, strings.Join(r.records, "\n"), "card=set_aside turn=2/2 reason=\"the turn exited 3 with no RESULT.md\" finish=failed sent=server")
		})
	})

	// a run the daemon stopped at the cap names the cap, and the head the friend pushed
	// (a HOLD at it, as friend sync keeps a HOLD's head)
	t.Run("cap", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := laneEndFixture(t, "c2~15.g2")
			head := "89abcdef0123456789abcdef0123456789abcdef"
			ref := filepath.Join(dir, "jobs", "c2~15.g2", "repo", ".git", "refs", "remotes", "origin", "sprint", "c2.g1.e15")
			require.NoError(t, os.MkdirAll(filepath.Dir(ref), 0o755))
			require.NoError(t, os.WriteFile(ref, []byte(head+"\n"), 0o644))
			h := &lanesHarness{dir: dir, active: map[string]int{}, block: make(chan struct{})} // silent until stopped
			r, state := laneRig(t, h, 1)
			f := &finishes{}
			r.d.Finish, r.d.SilentStop = f.finish, 30*time.Second
			r.run(t, 200)

			report, err := os.ReadFile(filepath.Join(dir, "outbox", "c2~15.g2", "REPORT.md"))
			require.NoError(t, err, strings.Join(r.records, "\n"))
			assert.True(t, strings.HasPrefix(string(report), "Verdict: HOLD\nHead: "+head+"\n\n"), string(report))
			assert.Contains(t, string(report), "the run was stopped at the cap (no output for 30s), exit 0, wall ")
			assert.Contains(t, string(report), "; pushed head "+head+" on sprint/c2.g1.e15.")
			sent := f.got()
			require.Len(t, sent, 1)
			assert.Equal(t, []string{"finish", "--as", "friend.bob", "c2@2", "--epoch", "15", "--failed", "--head", head, "--branch", "sprint/c2.g1.e15", "--report"}, sent[0][:12])
			assert.Contains(t, sent[0][12], "friend bob HOLD: ")
			assert.Empty(t, state.Started)
		})
	})

	// a daemon that starts up finishes each card its lanes began whose run is gone: failed
	// when the friend wrote no report, by her report when she did; neither is handed again
	t.Run("restart", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := laneEndFixture(t, "c3~15", "c4~15")
			c3 := Card{ID: "c3", Brief: filepath.Join(dir, "inbox", "c3~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c3~15")}
			c4 := Card{ID: "c4", Brief: filepath.Join(dir, "inbox", "c4~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c4~15")}
			require.NoError(t, os.MkdirAll(c4.Outbox, 0o755))
			require.NoError(t, os.WriteFile(c4.Report(), []byte("Verdict: FAIL\n\nmine\n"), 0o644))
			h := &lanesHarness{dir: dir, active: map[string]int{}}
			r, state := laneRig(t, h, 2)
			*state = LaneState{Sessions: map[int]string{1: "ses_1", 2: "ses_2"}, Started: map[string]Started{
				"c3~15": {Lane: 1, Card: c3, At: t0.Add(-time.Hour)},
				"c4~15": {Lane: 2, Card: c4, At: t0.Add(-time.Hour)},
			}}
			f := &finishes{}
			r.d.Finish = f.finish
			r.run(t, 10)

			report, err := os.ReadFile(c3.Report())
			require.NoError(t, err)
			assert.Contains(t, string(report), "Verdict: FAIL\n\nnova-friend lane 1 of bob finished card c3: the run is gone: the lane daemon started up at ")
			assert.Contains(t, string(report), " and found the card begun at "+t0.Add(-time.Hour).Format(time.RFC3339)+" with no REPORT.md")
			mine, err := os.ReadFile(c4.Report())
			require.NoError(t, err)
			assert.Equal(t, "Verdict: FAIL\n\nmine\n", string(mine), "her own report is never written over")
			sent := f.got()
			require.Len(t, sent, 1, "only the card with no report is finished by the lane")
			assert.Equal(t, "c3@1", sent[0][3])
			turns, _, _ := h.got()
			assert.Empty(t, turns, "neither card is handed again")
			assert.Empty(t, state.Started)
			assert.ElementsMatch(t, []string{"c3~15", "c4~15"}, state.GivenUp)
			records := strings.Join(r.records, "\n")
			assert.Contains(t, records, "lane 1: card c3 was begun at "+t0.Add(-time.Hour).Format(time.RFC3339)+" and its run is gone: finish=failed sent=server")
			assert.Contains(t, records, "lane 2: card c4 was begun at "+t0.Add(-time.Hour).Format(time.RFC3339)+" and its run is gone: finish=report")
		})
	})

	// a run that writes the friend's report finishes with it: the lane writes nothing and
	// sends nothing, friend sync reads hers
	t.Run("report", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := laneEndFixture(t, "c5~15")
			out := filepath.Join(dir, "outbox", "c5~15")
			r, state, f := pathRig(t, dir, "report", out)
			r.run(t, 20)

			report, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
			require.NoError(t, err, strings.Join(r.records, "\n"))
			assert.Equal(t, "Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n\nthe friend did it\n", string(report))
			assert.Empty(t, f.got(), "no finish from the lane")
			assert.Empty(t, state.Started)
			assert.Empty(t, state.GivenUp)
			assert.Contains(t, strings.Join(r.records, "\n"), "card=done finish=report")
		})
	})
}

// The pushed head is read from a clone's remote-tracking ref, loose or packed, through a
// worktree's .git file too; a job with none has none.
func TestPushedHeadIsTheBranchsRemoteTrackingRef(t *testing.T) {
	t.Parallel()
	dir := laneEndFixture(t, "p1~15", "p2~15")
	card := func(job string) Card {
		return Card{ID: job[:2], Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}
	}
	head, branch := PushedHead(dir, card("p1~15"))
	assert.Equal(t, "", head)
	assert.Equal(t, "sprint/p1.g1.e15", branch)

	sha := "fedcba9876543210fedcba9876543210fedcba98"
	common := filepath.Join(dir, "jobs", "p1~15", "clone", ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(common, "worktrees", "wt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(common, "packed-refs"), []byte("# pack-refs with: peeled\n"+sha+" refs/remotes/origin/sprint/p1.g1.e15\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(common, "worktrees", "wt", "commondir"), []byte("../..\n"), 0o644))
	wt := filepath.Join(dir, "jobs", "p1~15", "wt")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(common, "worktrees", "wt")+"\n"), 0o644))
	head, _ = PushedHead(dir, card("p1~15"))
	assert.Equal(t, sha, head, "packed, found from the clone or its worktree")
	require.NoError(t, os.Rename(common, filepath.Join(dir, "jobs", "p1~15", "elsewhere.git")))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(dir, "jobs", "p1~15", "elsewhere.git", "worktrees", "wt")+"\n"), 0o644))
	head, _ = PushedHead(dir, card("p1~15"))
	assert.Equal(t, sha, head, "through the worktree's .git file alone")
	head, _ = PushedHead(dir, card("p2~15"))
	assert.Equal(t, "", head)
}

// A job's name is <id>~<epoch>, .g<gen> after it from the card's second generation; a
// lane hands the generation the queue names.
func TestParseJobReadsTheEpochAndTheGeneration(t *testing.T) {
	t.Parallel()
	for job, want := range map[string][3]any{"c~15": {"c", 15, 1}, "a.w1~15.g3": {"a.w1", 15, 3}, "c~0": {"c", 0, 1}} {
		id, epoch, gen, ok := ParseJob(job)
		require.True(t, ok, job)
		assert.Equal(t, want, [3]any{id, epoch, gen}, job)
	}
	for _, bad := range []string{"c", "~1", "c~x", "c~1.gx", "c~1.g0"} {
		_, _, _, ok := ParseJob(bad)
		assert.False(t, ok, bad)
	}
	dir := laneEndFixture(t, "c~15.g2")
	c, found, err := NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(dir, "outbox", "c~15.g2"), c.Outbox)
	assert.Equal(t, 2, c.Gen())
	assert.Equal(t, "15", c.Epoch())
}
