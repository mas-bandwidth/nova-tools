package swarm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// needsGitFake is the stage's git seam for TestStagingRefusesABaseWithoutItsLandedNeeds:
// it records each merge-base argv, answers baseSha for rev-parse, and answers whether a
// commit is in ancestors for merge-base --is-ancestor.
type needsGitFake struct {
	baseSha    string
	ancestors  map[string]bool
	mergeBases [][]string
}

func (f *needsGitFake) cmd(ctx context.Context, args ...string) *exec.Cmd {
	switch {
	case slices.Contains(args, "rev-parse"):
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s\\n' "+f.baseSha)
	case slices.Contains(args, "merge-base"):
		f.mergeBases = append(f.mergeBases, append([]string(nil), args...))
		ancestor := args[len(args)-2]
		if f.ancestors[ancestor] {
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		}
		return exec.CommandContext(ctx, "sh", "-c", "exit 1")
	default:
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
}

// TestStagingRefusesABaseWithoutItsLandedNeeds replays the sprint-21 spike-wide-joint-solve
// staging against a tip without its landed need fixed3d-perf-harness
// (08681b55602c7e01545d855432774c2576a47b97) and against one that holds it
// (nova-sprint tla/StageNeeds.tla).
func TestStagingRefusesABaseWithoutItsLandedNeeds(t *testing.T) {
	t.Parallel()

	const (
		needID   = "fixed3d-perf-harness"
		needHead = "08681b55602c7e01545d855432774c2576a47b97"
		staleTip = "a1b2c3d4e5f67e01545d855432774c2576a47b97"
		freshTip = "f6e5d4c3b2a17e01545d855432774c2576a47b97"
	)

	var pkt member.Packet
	require.NoError(t, json.Unmarshal([]byte(`{
		"card": "spike-wide-joint-solve.w1",
		"kind": "work",
		"as": "vision",
		"primary": "spike-wide-joint-solve",
		"stream": "fixed3d-box3d-ports",
		"attempt": 1,
		"gen": 1,
		"epoch": 21,
		"branch": "sprint/spike-wide-joint-solve.w1.g1.e21",
		"base": "sprint/fixed3d-box3d-ports",
		"need_heads": [
			{"id": "fixed3d-perf-harness", "state": "landed", "head": "08681b55602c7e01545d855432774c2576a47b97"},
			{"id": "box3d- pending", "state": "working"}
		]
	}`), &pkt))
	require.Equal(t, []member.NeedHead{
		{ID: needID, State: "landed", Head: needHead},
		{ID: "box3d- pending", State: "working"},
	}, pkt.NeedHeads)

	card := []byte("RESULT: spike-wide-joint-solve sha=000000000000 tier: frontier\n" +
		"REPO: mas-bandwidth/nova\n" +
		"BASE: sprint/fixed3d-box3d-ports\n\n" +
		"THE TASK.\n")

	makeMirror := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		mirror := filepath.Join(home, "nova-bench", "mirror", "nova.git")
		require.NoError(t, os.MkdirAll(mirror, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/sprint/fixed3d-box3d-ports\n"), 0o644))
		return home
	}

	// 1. Base does not contain fixed3d-perf-harness's landed head: refused with kind staging refused.
	home := makeMirror(t)
	staleFake := &needsGitFake{baseSha: staleTip, ancestors: map[string]bool{}}
	targetStale := filepath.Join(t.TempDir(), "repo")
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: targetStale,
		JobDir:    filepath.Dir(targetStale),
		BenchHome: home,
		BenchName: "vision",
		Branch:    pkt.Branch,
		NeedHeads: pkt.NeedHeads,
		git:       staleFake.cmd,
	})
	require.Error(t, err)
	assert.False(t, res.Staged)
	assert.Equal(t, "staging refused: need fixed3d-perf-harness landed 08681b55602c and base a1b2c3d4e5f6 does not contain it", err.Error())
	fin, why := member.Judge(member.Result{
		End:     member.EndStaging,
		Staging: strings.TrimPrefix(err.Error(), "staging refused: "),
	}, member.Push{})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "staging refused: need fixed3d-perf-harness landed 08681b55602c and base a1b2c3d4e5f6 does not contain it", why)

	// 2. Rework restaged at a tip that still lacks the landed need is also refused.
	reworkStaleFake := &needsGitFake{baseSha: staleTip, ancestors: map[string]bool{}}
	targetReworkStale := filepath.Join(t.TempDir(), "repo")
	res, err = StageCard(StageOptions{
		Card:      card,
		TargetDir: targetReworkStale,
		JobDir:    filepath.Dir(targetReworkStale),
		BenchHome: home,
		BenchName: "vision",
		Branch:    pkt.Branch,
		Rework:    &Rework{From: 1},
		NeedHeads: pkt.NeedHeads,
		git:       reworkStaleFake.cmd,
	})
	require.Error(t, err)
	assert.False(t, res.Staged)
	assert.Contains(t, err.Error(), "staging refused: need fixed3d-perf-harness landed 08681b55602c and base a1b2c3d4e5f6 does not contain it")

	// 3. Base contains fixed3d-perf-harness's landed head (08681b55602c): staged; the second need with empty head is not checked.
	freshFake := &needsGitFake{baseSha: freshTip, ancestors: map[string]bool{needHead: true}}
	targetFresh := filepath.Join(t.TempDir(), "repo")
	res, err = StageCard(StageOptions{
		Card:      card,
		TargetDir: targetFresh,
		JobDir:    filepath.Dir(targetFresh),
		BenchHome: home,
		BenchName: "vision",
		Branch:    pkt.Branch,
		NeedHeads: pkt.NeedHeads,
		git:       freshFake.cmd,
	})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Equal(t, freshTip, res.BaseSha)
	require.Len(t, freshFake.mergeBases, 1, "only the need with a non-empty head is checked")
	assert.Equal(t, []string{"-C", targetFresh, "merge-base", "--is-ancestor", "--end-of-options", needHead, freshTip}, freshFake.mergeBases[0])

	// 4. A need with an empty head is not checked.
	emptyHeadFake := &needsGitFake{baseSha: staleTip, ancestors: map[string]bool{}}
	targetEmptyHead := filepath.Join(t.TempDir(), "repo")
	res, err = StageCard(StageOptions{
		Card:      card,
		TargetDir: targetEmptyHead,
		JobDir:    filepath.Dir(targetEmptyHead),
		BenchHome: home,
		BenchName: "vision",
		Branch:    pkt.Branch,
		NeedHeads: []member.NeedHead{{ID: needID, State: "working", Head: ""}},
		git:       emptyHeadFake.cmd,
	})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Empty(t, emptyHeadFake.mergeBases)

	// 5. A packet with no need_heads stages as today.
	noNeedsFake := &needsGitFake{baseSha: staleTip, ancestors: map[string]bool{}}
	targetNoNeeds := filepath.Join(t.TempDir(), "repo")
	res, err = StageCard(StageOptions{
		Card:      card,
		TargetDir: targetNoNeeds,
		JobDir:    filepath.Dir(targetNoNeeds),
		BenchHome: home,
		BenchName: "vision",
		Branch:    pkt.Branch,
		git:       noNeedsFake.cmd,
	})
	require.NoError(t, err)
	assert.True(t, res.Staged)
	assert.Empty(t, noNeedsFake.mergeBases)
}
