package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseAndUnpauseAreStoreWritesWithDistinctStoppedRefusal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta.do("unpause")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "cannot start")
	ta.ok("start")
	assert.Contains(t, ta.ok("pause"), "PAUSE OK before=RUNNING after=PAUSED changed")
	assert.Contains(t, ta.ok("pause"), "unchanged")
	assert.Contains(t, ta.ok("queue --as m1 --json"), `"machine":"PAUSED"`)
	assert.Contains(t, ta.ok("start"), "after=PAUSED")
	assert.Contains(t, ta.ok("unpause"), "UNPAUSE OK before=PAUSED after=RUNNING changed")
	assert.Contains(t, ta.ok("unpause"), "unchanged")
	ta.ok("pause")
	ta.ok("stop --reason test --until 1h")
	code, _, errs = ta.do("unpause")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "STOPPED")
	var out machineOut
	code, body, _ := ta.do("unpause --json")
	assert.Equal(t, 1, code)
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	assert.Equal(t, "STOPPED", out.Before)
	assert.Equal(t, "STOPPED", out.After)
	assert.False(t, out.Changed)
	require.NotEmpty(t, out.Error)
}

func TestPauseDuringLandingLetsAllNativelyPreparedBatchesFinish(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.ok("add --stream s2 next --one")
	r.queued(map[string]string{
		"first": r.head("first", "main", "first.txt", "first\n"),
		"next":  r.head("next", "main", "next.txt", "next\n"),
	}, "first", "next")
	r.ok("start")
	pushes := 0
	r.a.beforePush = func(int) {
		pushes++
		if pushes == 1 {
			r.ok("pause")
		}
	}
	code, out, errs := r.do("land --land-parallel 1 --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Equal(t, 2, pushes, "both batches began native preparation before pause")

	assert.Equal(t, map[string]string{"first": "landed/merged", "next": "landed/merged"}, r.places("first", "next"))
	r.ok("unpause")
	r.a.beforePush = nil
	r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, map[string]string{"first": "landed/merged", "next": "landed/merged"}, r.places("first", "next"))
}

// A forge check has not begun native preparation; pause under that check holds clone.
type pauseDuringForge struct{ pause func() }

func (q pauseDuringForge) HoldsGroup(context.Context, string, string) (bool, error) {
	q.pause()
	return false, nil
}
func TestPauseUnderForgeAdmissionHoldsBeforeNativePreparation(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	r.ok("start")
	r.a.mergeQueue = pauseDuringForge{pause: func() { r.ok("pause") }}
	before := r.git(r.clone, "rev-parse", "HEAD")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "PAUSED")
	assert.Equal(t, before, r.git(r.clone, "rev-parse", "HEAD"))
	assert.Equal(t, []string{"base"}, r.mainLog())
	assert.Equal(t, "merging/queued", r.places("first")["first"])
	entries, err := os.ReadDir(filepath.Join(r.dir, "land"))
	assert.True(t, os.IsNotExist(err) || err == nil && len(entries) == 0, "no native worktree preparation began")
	r.a.mergeQueue = r.queue
	r.ok("unpause")
	r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, "landed/merged", r.places("first")["first"])
}
func TestPausedLandingHoldsANewPassAndKeepsItsQueue(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	r.ok("start")
	r.a.beforePush = func(int) { r.ok("pause") }
	r.ok("land --repo-dir " + r.clone + " --base main")
	r.a.beforePush = nil
	// Prepare the second claim before pausing in a later pass; no new native execution.
	r.ok("unpause")
	r.ok("add --stream s2 next --one")
	r.settle()
	r.queued(map[string]string{"next": r.head("next", "main", "next.txt", "next\n")}, "next")
	r.ok("pause")
	before := r.mainLog()
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "PAUSED")
	assert.Equal(t, before, r.mainLog())
	assert.Equal(t, "merging/queued", r.places("next")["next"])
	r.ok("unpause")
	r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, "landed/merged", r.places("next")["next"])
}

func TestMachineOnlyReaderQueueIsAvailableWithoutReaderRowDuringAllControlStates(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	for _, state := range []string{"STOPPED", "RUNNING", "PAUSED"} {
		if state == "RUNNING" {
			ta.ok("start")
		}
		if state == "PAUSED" {
			ta.ok("pause")
		}
		var q struct {
			Machine string `json:"machine"`
			Cards   []any  `json:"cards"`
		}
		ta.json("queue --as reader-alex --json --packets 0", &q)
		assert.Equal(t, state, q.Machine)
		assert.Empty(t, q.Cards)
	}
}

func TestStoppedLandingCannotBypassPauseOrBeginNewNativePreparation(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	r.ok("pause")
	r.ok("stop --reason control-test --until 1h")
	before := r.mainLog()
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "STOPPED")
	assert.Equal(t, before, r.mainLog())
	assert.Equal(t, "merging/queued", r.places("first")["first"])
	r.ok("start")
	r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, "landed/merged", r.places("first")["first"])
}
