package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCodexCardRunnerStartsARealModelCommand pins the one-shot launch boundary:
// the existing lane loop gives it a card, it runs the Codex CLI with that
// card's brief on stdin in the job directory and accepts only real outbox files.
func TestCodexCardRunnerStartsARealModelCommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		write   bool
		direct  bool
		wantErr string
	}{
		{"friend staging reported", true, false, ""},
		{"sprint staging reported", true, true, ""},
		{"no report", false, false, "REPORT.md and no RESULT.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			job := filepath.Join(root, "jobs", "c1~15")
			repo := filepath.Join(job, "repo")
			if tc.direct {
				repo = job
			}
			outbox := filepath.Join(root, "outbox", "c1~15")
			require.NoError(t, os.MkdirAll(repo, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere\n"), 0o644))
			require.NoError(t, os.MkdirAll(outbox, 0o755))
			brief := filepath.Join(root, "BRIEF.md")
			require.NoError(t, os.WriteFile(brief, []byte("work c1, epoch 15 generation 1"), 0o644))
			config := filepath.Join(root, "account")
			require.NoError(t, os.MkdirAll(config, 0o700))
			var gotDir, gotName, gotStdin string
			var gotArgs []string
			run := func(_ context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				gotDir, gotName, gotArgs, gotStdin = dir, name, slices.Clone(args), stdin
				if tc.write {
					require.NoError(t, os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
					require.NoError(t, os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("RESULT: c1\n"), 0o644))
				}
				return `{"type":"turn.completed"}`, 0, nil
			}
			cx := NewCodex("bob", root, run, nil)
			cx.ConfigDir = func() string { return config }
			turn, err := cx.RunCard(WithLaneDir(LaneContext(t.Context()), job), Card{ID: "c1", Brief: brief, Outbox: outbox, Tier: "pro"})
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			assert.Zero(t, turn.Exit)
			assert.Equal(t, repo, gotDir)
			assert.Equal(t, "env", gotName)
			assert.Equal(t, "work c1, epoch 15 generation 1", gotStdin)
			assert.Equal(t, []string{"CODEX_HOME=" + config, "codex", "--dangerously-bypass-approvals-and-sandbox", "exec", "--ignore-user-config", "--json", "--cd", repo, "--add-dir", outbox, "--model", "gpt-5.6-sol", "-"}, gotArgs)
		})
	}
}

// TestCodexReadAndRefusal keeps a reader on the named tier model and refuses a
// card before launching when the account's Codex home is not absolute.
func TestCodexReadAndRefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var calls int
	var gotArgs []string
	run := func(_ context.Context, _, _ string, args []string, stdin string) (string, int, error) {
		calls++
		gotArgs = slices.Clone(args)
		assert.Equal(t, "read c1", stdin)
		return `{"type":"turn.completed"}`, 0, nil
	}
	cx := NewCodex("bob", root, run, nil)
	cx.ConfigDir = func() string { return "relative" }
	_, err := cx.RunRead(LaneContext(t.Context()), CodexReadModels["flash"], "read c1")
	require.ErrorContains(t, err, "without an absolute config_dir")
	assert.Zero(t, calls)
	cx.ConfigDir = func() string { return filepath.Join(root, "account") }
	_, err = cx.RunRead(LaneContext(t.Context()), "", "read c1")
	require.ErrorContains(t, err, "no supported Codex tier")
	assert.Zero(t, calls)
	_, err = cx.RunRead(LaneContext(t.Context()), CodexReadModels["flash"], "read c1")
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.True(t, slices.Contains(gotArgs, "gpt-5.6-luna"), gotArgs)
	assert.True(t, slices.Contains(gotArgs, "--model"), gotArgs)
	assert.True(t, slices.Contains(gotArgs, "--skip-git-repo-check"), gotArgs)
	assert.Equal(t, "gpt-6-astra", CodexReadModels["frontier"])
	assert.True(t, RunsCards("codex"))
}

func TestCodexCardRunnerReturnsTheProcessFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere\n"), 0o644))
	brief := filepath.Join(root, "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("work"), 0o644))
	cx := NewCodex("bob", root, func(context.Context, string, string, []string, string) (string, int, error) {
		return "turn failed", 1, errors.New("child stopped")
	}, nil)
	cx.ConfigDir = func() string { return root }
	turn, err := cx.RunCard(LaneContext(t.Context()), Card{ID: "c1", Brief: brief, Outbox: root, Tier: "pro"})
	require.ErrorContains(t, err, "child stopped")
	assert.Equal(t, 1, turn.Exit)
	assert.True(t, strings.Contains(cx.Refusal(), "config_dir") == false)
}

func TestCodexCardRunnerRefusesAnUnknownWorkTier(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	calls := 0
	cx := NewCodex("bob", root, func(context.Context, string, string, []string, string) (string, int, error) {
		calls++
		return "", 0, nil
	}, nil)
	cx.ConfigDir = func() string { return root }
	_, err := cx.RunCard(LaneContext(t.Context()), Card{ID: "c1", Tier: "unexpected"})
	require.ErrorContains(t, err, "no supported Codex tier")
	assert.Zero(t, calls)
}

func TestCodexCardRunnerRefusesOutsideLaneBeforeAnyModelOrOutbox(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var calls int
	cx := NewCodex("bob", root, func(context.Context, string, string, []string, string) (string, int, error) {
		calls++
		return "", 0, nil
	}, nil)
	cx.ConfigDir = func() string { return root }
	outbox := filepath.Join(root, "outbox", "c1")
	_, err := cx.RunCard(t.Context(), Card{ID: "c1", Outbox: outbox, Tier: "pro"})
	require.ErrorContains(t, err, "outside a nova-friend lane wall")
	_, err = cx.RunRead(t.Context(), CodexReadModels["pro"], "read c1")
	require.ErrorContains(t, err, "outside a nova-friend lane wall")
	assert.Zero(t, calls)
	_, err = os.Stat(outbox)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestCodexRefillsOneRealProcessAfterTheFirstCompletes uses the existing
// daemon's fake clock and queue. A second card arrives while width is full and
// a third after it frees; neither needs a prompt or coordinator command.
func TestCodexRefillsOneRealProcessAfterTheFirstCompletes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		for _, id := range []string{"c1", "c2", "c3"} {
			repo := filepath.Join(root, "jobs", id+"~15", "repo")
			require.NoError(t, os.MkdirAll(repo, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere\n"), 0o644))
		}
		var mu sync.Mutex
		started := []string{}
		active, maxActive := 0, 0
		release := map[string]chan struct{}{"c1": make(chan struct{}), "c2": make(chan struct{}), "c3": make(chan struct{})}
		run := func(ctx context.Context, _, name string, _ []string, stdin string) (string, int, error) {
			if name != "env" {
				return "", 2, errors.New("not a Codex model process")
			}
			id := strings.TrimSpace(strings.TrimPrefix(stdin, "RESULT: "))
			mu.Lock()
			started = append(started, id)
			active++
			maxActive = max(maxActive, active)
			mu.Unlock()
			select {
			case <-release[id]:
			case <-ctx.Done():
				return "", 1, ctx.Err()
			}
			outbox := filepath.Join(root, "outbox", id+"~15")
			if err := os.MkdirAll(outbox, 0o755); err != nil {
				return "", 1, err
			}
			for name, body := range map[string]string{"REPORT.md": "Verdict: LAND\n", "RESULT.md": "RESULT: " + id + "\n"} {
				if err := os.WriteFile(filepath.Join(outbox, name), []byte(body), 0o644); err != nil {
					return "", 1, err
				}
			}
			mu.Lock()
			active--
			mu.Unlock()
			return `{"type":"turn.completed"}`, 0, nil
		}
		cx := NewCodex("bob", root, run, nil)
		cx.ConfigDir = func() string { return root }
		r, _ := laneRig(t, &lanesHarness{dir: root, active: map[string]int{}}, 1)
		r.d.Harness, r.d.Deliver = "codex", cx
		r.d.heldCards = []HeldCard{{Job: "c1~15", Tier: "pro"}, {Job: "c2~15", Tier: "pro"}, {Job: "c3~15", Tier: "pro"}}
		r.at[10] = func() {
			q, err := json.Marshal(Queue{Tasks: []Task{{ID: "c1", State: "queued"}, {ID: "c2", State: "queued"}}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "inbox", "QUEUE.json"), q, 0o644))
			mu.Lock()
			assert.Equal(t, []string{"c1"}, started, "new ready work cannot overtake the busy lane")
			mu.Unlock()
		}
		r.at[15] = func() { close(release["c1"]) }
		r.at[25] = func() { close(release["c2"]) }
		r.at[28] = func() {
			q, err := json.Marshal(Queue{Tasks: []Task{{ID: "c1", State: "queued"}, {ID: "c2", State: "queued"}, {ID: "c3", State: "queued"}}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "inbox", "QUEUE.json"), q, 0o644))
		}
		r.at[36] = func() { close(release["c3"]) }
		r.run(t, 43)
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"c1", "c2", "c3"}, started, "a queued card and a later ready arrival both start automatically")
		assert.Equal(t, 1, maxActive, "width one is never overtaken")
	})
}
