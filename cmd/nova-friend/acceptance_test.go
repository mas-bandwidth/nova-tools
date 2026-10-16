package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The acceptance run and the daemon's lane children run inside the identical
// wall: both call laneWall, so with the same flags and an empty friend row the
// two walls' Args() are equal (docs/SPEC-FRIEND.md, the acceptance run;
// buds-in-the-wall-r.w5). A wall of its own — sandbox.ProfileFriend or an
// invented deny path — fails this.
func TestAcceptanceAndRunAssembleTheSameWall(t *testing.T) {
	t.Parallel()
	flags := []string{
		"--profile", "friend",
		"--config-dir", "/accounts/heavy-a",
		"--deny-self", "/seat/self,/vol/self",
		"--wall-jobs", "/jobs,/jobs2",
		"--wall-reads", "/reads",
		"--coordinator", "ada",
	}

	// acceptance: the wall it hands the wall seam, from the same flags.
	accRig := newRig(t, "ada", "bob")
	accRig.onPath = map[string]string{"claude": "/usr/bin/claude"}
	accWorld := accRig.world()
	var accWall friend.Wall
	accWorld.wall = func(wl friend.Wall, inner friend.Exec) friend.Exec { accWall = wl; return inner }
	dir := t.TempDir()
	var acout, acerr strings.Builder
	acode := run(append([]string{"acceptance", "--as", "bob", "--harness", "claude", "--dir", dir}, flags...),
		strings.NewReader(""), &acout, &acerr, accWorld)
	require.Equal(t, 0, acode, acerr.String())
	require.NotEmpty(t, accWall.Args(), "acceptance built no wall")

	// run: the wall a one-shot claude lane hands the wall seam, same flags, same
	// dir, no row config_dir, so the flag's value stands for both. A bubble at a
	// time: the lane's daemon parks on a clock, which the bubble drives.
	t.Run("run's wall", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runWall, out := runOneShotLane(t, dir, "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1", flags...)
			assert.Equal(t, accWall.Args(), runWall.Args(), "acceptance and run built different walls from the same flags and row\n%s", out)
		})
	})
}

// runOneShotLane runs one `run` of a one-shot claude lane over dir inside the
// current synctest bubble and returns the wall the lane ran under and the run's
// output. The daemon parks on the injected clock, which the bubble drives; the
// lane ends after the twelfth beat. beatReply is what the beat answers, so the
// caller can vary the row's profile, and extra are the flags the caller adds.
func runOneShotLane(t *testing.T, dir, beatReply string, extra ...string) (friend.Wall, string) {
	t.Helper()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
	var mu sync.Mutex
	var runWall friend.Wall
	w.wall = func(wl friend.Wall, _ friend.Exec) friend.Exec {
		return func(ctx context.Context, _, _ string, args []string, _ string) (string, int, error) {
			if friend.InLane(ctx) && !proofCall(args) {
				mu.Lock()
				runWall = wl
				mu.Unlock()
				out := filepath.Join(dir, "outbox", "c1~15")
				if err := os.MkdirAll(out, 0o755); err != nil {
					return "", 0, err
				}
				for _, f := range []string{"REPORT.md", "RESULT.md"} {
					if err := os.WriteFile(filepath.Join(out, f), []byte("done\n"), 0o644); err != nil {
						return "", 0, err
					}
				}
			}
			return `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.0125,"session_id":"s1","result":"ok"}` + "\n", 0, nil
		}
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 12 {
			cancel()
		}
		return beatReply, nil
	}
	var out, errb strings.Builder
	code := run(append([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir}, extra...),
		strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, runWall.Args(), "the lane ran in no wall\n%s", out.String())
	return runWall, out.String()
}

// A lane program that exits 126 inside the wall makes acceptance exit 126 and
// its note name the friend and the exit code (docs/SPEC-FRIEND.md, the
// acceptance run); a runner that cannot be found is the same exit 126.
func TestAcceptanceExitsTheWallsRealCode(t *testing.T) {
	t.Parallel()
	const wantNote = "friend=bob exit=126"
	ok := newRig(t, "ada", "bob")
	ok.onPath = map[string]string{"claude": "/usr/bin/claude"}
	w := ok.world()
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) {
			return "cannot execute\n", 126, nil
		}
	}
	var out, errb strings.Builder
	code := run([]string{"acceptance", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()},
		strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 126, code)
	assert.Contains(t, out.String()+errb.String(), wantNote)

	// the runner is not on PATH: refused as 126 before any wall is built.
	miss := newRig(t, "ada", "bob").world()
	miss.wall = func(_ friend.Wall, inner friend.Exec) friend.Exec { return inner }
	var mout, merr strings.Builder
	mcode := run([]string{"acceptance", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()},
		strings.NewReader(""), &mout, &merr, miss)
	assert.Equal(t, 126, mcode)
	assert.Contains(t, mout.String()+merr.String(), wantNote)
}

// --dry-run plans the acceptance run from the same path and writes nothing: the
// wall is assembled and the runner found, the plan names the command, and no
// .acceptance directory or RESULT.md is made and no lane program runs.
func TestAcceptanceDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	rig := newRig(t, "ada", "bob")
	rig.onPath = map[string]string{"claude": "/usr/bin/claude"}
	w := rig.world()
	ran := false
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) {
			ran = true
			return "claude 1.0.0\n", 0, nil
		}
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"acceptance", "--as", "bob", "--harness", "claude", "--dir", dir, "--dry-run"},
		strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, out.String()+errb.String())
	assert.False(t, ran, "a dry run ran the lane program")
	assert.Contains(t, out.String(), "claude --version", "the plan names the command the run would start")
	assert.Contains(t, out.String(), "dry_run=true")
	_, err := os.Stat(filepath.Join(dir, ".acceptance"))
	assert.True(t, os.IsNotExist(err), "a dry run made the probe directory: %v", err)
}

// The antigravity lane starts the harness's own agentapi under the friend's
// home, the path the adapter itself runs (friend.AntigravityRunner,
// pkg/friend/adapter_antigravity.go), never a bare name on PATH: a healthy host
// is not reported exit=126 (docs/SPEC-FRIEND.md, the acceptance run: the lane's
// own program).
func TestAcceptanceRunsTheAntigravityAdapterPath(t *testing.T) {
	t.Parallel()
	rig := newRig(t, "ada", "bob")
	runner := filepath.Join(rig.home, friend.AntigravityData, "bin", "agentapi")
	rig.onPath = map[string]string{runner: runner}
	w := rig.world()
	var got string
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(_ context.Context, _, prog string, _ []string, _ string) (string, int, error) {
			got = prog
			return "agentapi version 1\n", 0, nil
		}
	}
	var out, errb strings.Builder
	code := run([]string{"acceptance", "--as", "bob", "--harness", "antigravity", "--dir", t.TempDir()},
		strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, out.String()+errb.String())
	assert.Equal(t, friend.AntigravityRunner(rig.home), got, "acceptance ran no agentapi under the friend's home")
	assert.Equal(t, runner, got, "acceptance ran agentapi by name instead of the adapter's path under the home")
}

// A beat that answers an empty row_profile= makes the row's profile the wall's,
// the way run read it before run and acceptance shared the assembly: --profile
// does not stand in for the row's empty profile (friend.ParseProfile,
// pkg/friend/lanes.go; docs/SPEC-FRIEND.md, the acceptance run). run's behaviour
// is unchanged by the shared laneWall.
func TestRunEmptyRowProfileOverridesTheProfileFlag(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runWall, _ := runOneShotLane(t, t.TempDir(), "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1 row_profile=", "--profile", "strict")
		assert.Equal(t, "", runWall.Profile, "an empty row_profile= must override --profile, the behaviour run had before the shared assembly")
	})
}
