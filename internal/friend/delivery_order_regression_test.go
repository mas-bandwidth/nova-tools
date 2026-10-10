package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The injected stage sees the files at the actual daemon boundary, without
// sockets, a real clock, or a git subprocess (SPEC-FRIEND, delivery order).
func TestStagingOrderIsPinnedByTheInjectedDaemonSeam(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	h := stagedCard("order.w1", "working", "o/r", "dev")
	row := &twinRow{}
	row.set(h)
	r.d.Held = row.held
	seen := make(chan bool, 1)
	r.d.Stage = func(context.Context, Packet) (string, error) {
		_, err := os.Stat(filepath.Join(r.d.Dir, "inbox", h.Job, "BRIEF.md"))
		seen <- err == nil
		job := JobDir(r.d.Dir, h.Job)
		if err := os.MkdirAll(filepath.Join(job, "repo"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(job, JobFile), []byte("# JOB: staged\n"), 0o644); err != nil {
			return "", err
		}
		return strings.Repeat("a", 40), nil
	}
	briefBeforeStage := false
	r.at[1] = func() { briefBeforeStage = <-seen }
	r.run(t, 1)
	assert.False(t, briefBeforeStage, "BRIEF.md is exposed before staging has completed")
	assert.DirExists(t, filepath.Join(JobDir(r.d.Dir, h.Job), "repo"))
}

func TestTheHeldRowOverridesBothWHOPreferenceForms(t *testing.T) {
	t.Parallel()
	for _, who := range []string{"friend other", "only friend other"} {
		t.Run(who, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			row := &twinRow{}
			r.d.Held = row.held
			h := workCard("pin.w1", "working")
			h.Brief = strings.Replace(h.Brief, "\n\n", "\nWHO: "+who+"\n\n", 1)
			row.set(h)
			r.run(t, 2)
			raw, err := os.ReadFile(filepath.Join(r.d.Dir, "inbox", h.Job, "BRIEF.md"))
			require.NoError(t, err, "the dealt row overrides %s", who)
			assert.Equal(t, h.Brief, string(raw))
		})
	}
}

func TestASelfStagingRunnerIsNotEnteredByAnotherStage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := stagedCard("runner.w1", "working", "o/r", "dev")
	p, ok := PacketOf(h)
	require.True(t, ok)
	owned := filepath.Join(JobDir(dir, h.Job), "repo", "runner-owned")
	require.NoError(t, os.MkdirAll(filepath.Dir(owned), 0o755))
	require.NoError(t, os.WriteFile(owned, []byte("unfinished runner checkout\n"), 0o644))
	entered := false
	stager := &Stager{Dir: dir, Env: stageEnv(t), URL: func(string) string {
		entered = true
		return filepath.Join(dir, "remote-that-must-not-be-needed")
	}}
	_, err := stager.Stage(context.Background(), p)
	assert.Error(t, err)
	assert.False(t, entered, "the second stage enters the mirror before checking the runner-owned job")
	assert.FileExists(t, owned)
	assert.NoFileExists(t, filepath.Join(JobDir(dir, h.Job), JobFile))
}

func TestDeliveryCarriesTheCurrentTierOnTheResultLine(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"RESULT: pin.w1 sha=\n", "RESULT: pin.w1 sha= tier: frontier\n", ""} {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			h := HeldCard{Card: "pin.w1", Job: "pin.w1~15", Tier: "heavy", Brief: line + "REPO: o/r\n\nWork here.\n"}
			dir := t.TempDir()
			if strings.HasPrefix(line, "RESULT:") {
				_, err := SyncInbox(dir, Row{Cards: []HeldCard{h}}, nil, time.Time{}, time.Now(), func(string) {})
				require.NoError(t, err)
			} else {
				o := Delivery{Dir: dir}.One(context.Background(), h)
				require.NoError(t, o.Err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "inbox", h.Job, "BRIEF.md"))
			require.NoError(t, err)
			assert.Contains(t, string(raw), "RESULT: pin.w1 sha= tier: heavy\n")
			assert.NotContains(t, string(raw), "tier: frontier")
		})
	}
}
