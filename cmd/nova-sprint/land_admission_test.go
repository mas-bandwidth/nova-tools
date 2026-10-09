package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pauseAtLandAcquire lets a separate coordinator win the existing store fence,
// after this command has read RUNNING and planned the exact admission note.
type pauseAtLandAcquire struct {
	*store.Mem
	once  sync.Once
	pause func()
	seen  bool
}

func (b *pauseAtLandAcquire) Acquire(ctx context.Context, gen uint64, op store.OpRecord) (bool, error) {
	if op.Verb == "land admission" {
		b.once.Do(func() { b.seen = true; b.pause() })
	}
	return b.Mem.Acquire(ctx, gen, op)
}

func TestLandingAdmissionPauseRaceHoldsGitAndFreshRetry(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	st, err := r.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	b := &pauseAtLandAcquire{Mem: r.m, pause: func() {
		_, _, _, err := st.SetPaused(context.Background(), true)
		require.NoError(t, err)
	}}
	r.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return b, nil }
	before := r.git(r.clone, "rev-parse", "HEAD")
	code, out, errs := r.do("land --op same-request --repo-dir " + r.clone + " --base main")
	require.True(t, b.seen, "the exact batch reached the authoritative fenced admission")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "PAUSED")
	assert.Equal(t, before, r.git(r.clone, "rev-parse", "HEAD"))
	assert.Equal(t, []string{"base"}, r.mainLog())
	entries, err := os.ReadDir(filepath.Join(r.dir, "land"))
	assert.True(t, os.IsNotExist(err) || err == nil && len(entries) == 0, "no external worktree preparation began")
	assert.Equal(t, "merging/queued", r.places("first")["first"])
	r.ok("unpause")
	r.ok("land --op same-request --repo-dir " + r.clone + " --base main")
	assert.Equal(t, "landed/merged", r.places("first")["first"])
	notes, _, err := r.m.NotesSince(context.Background(), "", 10000)
	require.NoError(t, err)
	count := 0
	for _, n := range notes {
		if n.Type == "landing batch admitted" {
			count++
		}
	}
	assert.Equal(t, 1, count, "only the successful exact batch has a durable admission")
}

func TestLandingAdmissionDoesNotReuseReceiptForANewPreparation(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first --one")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	st, err := r.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(t, err)
	c := s.Work.Placed("first")
	require.NotNil(t, c)
	batch := []landCard{{id: c.ID, head: c.F("head"), attempt: c.F("attempt"), brief: c.F("brief"), base: "main"}}
	l := &lander{a: r.a, st: st, epoch: s.Epoch, c: common{op: "same-request"}}
	require.Empty(t, l.landAdmission(context.Background(), "s1", batch))
	_, _, _, err = st.SetPaused(context.Background(), true)
	require.NoError(t, err)
	assert.Contains(t, l.landAdmission(context.Background(), "s1", batch), "PAUSED",
		"a new preparation has fresh admission even if --op and all pinned inputs are unchanged")
}
