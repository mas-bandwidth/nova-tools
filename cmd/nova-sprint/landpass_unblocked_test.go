package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A base gate waiting on one stream must not prevent a different base's
// already green stream from reaching the serial push and report phase.
func TestLandAnotherBaseProgressesWhileFirstBaseGateWaits(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/other")
	r.git(r.worker, "fetch", "-q", "origin")
	r.moveBase("other", "other-base.txt")
	r.git(r.worker, "fetch", "-q", "origin")
	mainBase := r.git(r.remote, "rev-parse", "main")
	r.promotionStream("s1")
	r.promotionStream("s2")
	heads := map[string]string{}
	for _, row := range []struct{ stream, base string }{{"s1", "main"}, {"s2", "other"}} {
		id := row.stream + "-1"
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+row.base+"\n\nWrite "+id+".")), 0o600))
		r.ok("add --stream " + row.stream + " --one --brief-file " + brief)
		heads[id] = r.head(id, row.base, row.stream+".go", "package main\n\nfunc "+row.stream+"() {}\n")
	}
	r.queued(heads, "s1-1", "s2-1")
	blocked, green, release, pushed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.a.gateRan = func(dir string, tests bool) {
		if strings.HasSuffix(dir, "@s1") {
			once.Do(func() { close(blocked) })
		}
	}
	b := r.a.landState()
	b.mu.Lock()
	var greenOnce sync.Once
	b.gateBench = func(ctx context.Context, _, dir string, _ [][]string, _ bool) (string, int, error) {
		if strings.HasSuffix(dir, "@s1") {
			select {
			case <-ctx.Done():
				return "", 1, ctx.Err()
			case <-release:
				return "", 0, nil
			}
		}
		if strings.HasSuffix(dir, "@s2") {
			greenOnce.Do(func() { close(green) })
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	var pushOnce sync.Once
	r.a.beforePush = func(int) { pushOnce.Do(func() { close(pushed) }) }
	done := make(chan struct{})
	var code int
	var out, errs string
	go func() { code, out, errs = r.do("land --land-parallel 2"); close(done) }()
	select {
	case <-blocked:
	case <-t.Context().Done():
		t.Fatal("first base gate never began")
	}
	select {
	case <-green:
	case <-t.Context().Done():
		t.Error("second base could not gate while first base gate waited")
	}
	select {
	case <-pushed:
		// The green stream reaches its push while the first gate still waits.
	case <-t.Context().Done():
		t.Error("green second base did not push while first base gate waited")
	}
	close(release)
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("land did not finish after releasing first gate")
	}
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s2-1": "landed/merged"}, r.places("s1-1", "s2-1"))
	assert.Nil(t, r.a.baseGateFails[mainBase], "canceled base gate is not counted as red")
	r.clean()
}

// Once a red batch gate falls back to gating each head, cancellation of that
// second gate is still a pass-level timeout, never a finding against the card.
func TestLandFallbackGateDeadlineDoesNotBlameCard(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	mainBase := r.git(r.remote, "rev-parse", "main")
	r.promotionStream("s1")
	brief := filepath.Join(t.TempDir(), "s1-1.md")
	require.NoError(t, os.WriteFile(brief, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite s1-1.")), 0o600))
	r.ok("add --stream s1 --one --brief-file " + brief)
	head := r.head("s1-1", "main", "s1.go", "package main\n\nfunc s1() {}\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	deadline := make(chan time.Time, 1)
	parentAfter := r.a.after
	r.a.after = func(d time.Duration) <-chan time.Time {
		if d == LandDeadline {
			return deadline
		}
		return parentAfter(d)
	}
	var gates atomic.Int32
	fallback := make(chan struct{})
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, _, dir string, _ [][]string, _ bool) (string, int, error) {
		if !strings.HasSuffix(dir, "@s1") {
			return "", 0, nil
		}
		call := gates.Add(1)
		if call == 1 {
			return "", 0, nil // the base gate is green
		}
		if call == 2 {
			return "batch gate red", 1, nil
		}
		if call == 3 {
			close(fallback)
		}
		<-ctx.Done()
		return "", 1, ctx.Err()
	}
	b.mu.Unlock()
	done := make(chan struct{})
	var code int
	var out, errs string
	go func() { code, out, errs = r.do("land"); close(done) }()
	select {
	case <-fallback:
		deadline <- time.Now()
	case <-t.Context().Done():
		t.Fatal("fallback gate never began")
	}
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("land did not finish after fallback gate deadline")
	}
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, out+errs, "gate of this batch exceeded", "timeout is a batch refusal")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	assert.Nil(t, r.a.baseGateFails[mainBase], "canceled fallback gate is not counted as red")
	r.clean()
}

// stuckBehind is the shape of the hang the cold read of PR 5475 found (2026-10-09): two
// streams on different bases, the lower-priority one holding what the higher-priority one
// waits for while its own gate never answers. s1's goroutine is held at the beforeWait
// seam until s2's gate has begun, so s2 is the holder whatever order the goroutines start
// in, and the deadline fires only once s1 is at its wait. The pass cancels s1 first
// (priority order): before the fix the wait was plain, the cancellation changed nothing,
// s2 was never cancelled, and the pass hung for ever. With the fix s1 leaves its wait
// refused, the pass reaches s2, cancels it at its own bound and ends with both batches
// still queued and no base counted red. parallel is the pass's width; sameCommit puts the
// two bases at one commit (the per-commit gate is the wait, width 2), else the bases
// differ and the width slot is the wait (width 1).
func stuckBehind(t *testing.T, parallel int, sameCommit bool) {
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/other")
	r.git(r.worker, "fetch", "-q", "origin")
	if !sameCommit {
		r.moveBase("other", "other-base.txt")
		r.git(r.worker, "fetch", "-q", "origin")
	}
	mainBase, otherBase := r.git(r.remote, "rev-parse", "main"), r.git(r.remote, "rev-parse", "other")
	r.promotionStream("s1")
	r.promotionStream("s2")
	heads := map[string]string{}
	for _, row := range []struct{ stream, base string }{{"s1", "main"}, {"s2", "other"}} {
		id := row.stream + "-1"
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+row.base+"\n\nWrite "+id+".")), 0o600))
		r.ok("add --stream " + row.stream + " --one --brief-file " + brief)
		heads[id] = r.head(id, row.base, row.stream+".go", "package main\n\nfunc "+row.stream+"() {}\n")
	}
	r.queued(heads, "s1-1", "s2-1")
	holding, waiting := make(chan struct{}), make(chan struct{})
	deadline := make(chan time.Time, 1)
	parentAfter := r.a.after
	r.a.after = func(d time.Duration) <-chan time.Time {
		if d == LandDeadline {
			return deadline
		}
		return parentAfter(d)
	}
	wait := "slot"
	if sameCommit {
		wait = "gate"
	}
	var waitOnce sync.Once
	r.a.beforeWait = func(stream, what string) {
		if stream == "s1" && what == wait {
			<-holding // s2 holds the slot (or the commit's gate) and is stuck before s1 asks
			waitOnce.Do(func() { close(waiting) })
		}
	}
	b := r.a.landState()
	b.mu.Lock()
	var holdOnce sync.Once
	b.gateBench = func(ctx context.Context, _, dir string, _ [][]string, _ bool) (string, int, error) {
		if strings.HasSuffix(dir, "@s2") {
			holdOnce.Do(func() { close(holding) })
			<-ctx.Done() // the bench never answers: only the pass's own bound ends this gate
			return "", 1, ctx.Err()
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	r.a.beforePush = func(int) { t.Error("nothing may push: both batches are abandoned") }
	done := make(chan struct{})
	var code int
	var out, errs string
	go func() { code, out, errs = r.do("land --land-parallel " + strconv.Itoa(parallel)); close(done) }()
	select {
	case <-holding:
	case <-t.Context().Done():
		t.Fatal("the holder's gate never began")
	}
	select {
	case <-waiting:
	case <-t.Context().Done():
		t.Fatal("s1 never reached its wait behind s2")
	}
	// s1 is now at its wait behind s2: its bound fires, then s2's when the pass reaches it
	// (the second send waits in the channel until the pass asks for s2's clock)
	deadline <- time.Now()
	deadline <- time.Now()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("the pass did not end after both bounds fired: the cancelled waiter never left its wait")
	}
	assert.Equal(t, 1, code, "both batches refused for this pass: %s%s", out, errs)
	assert.Equal(t, 2, strings.Count(out+errs, "gate of this batch exceeded"), "each abandonment is the deadline refusal, no card blamed: %s%s", out, errs)
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s2-1": "merging/queued"}, r.places("s1-1", "s2-1"))
	assert.Nil(t, r.a.baseGateFails[mainBase], "an abandoned wait is not a red base")
	assert.Nil(t, r.a.baseGateFails[otherBase], "an abandoned gate is not a red base")
	r.clean()
}

// A higher-priority stream cancelled while it waits for the pass's only width slot leaves
// the wait; the lower-priority holder, stuck on its bench, is then bounded in its own turn.
func TestLandCancelledSlotWaitLeavesThePass(t *testing.T) {
	t.Parallel()
	stuckBehind(t, 1, false)
}

// Two bases at one commit share one gate: a stream cancelled while it waits for another
// stream's gate of that commit leaves the wait, and the holder is bounded in its own turn.
func TestLandCancelledGateWaitLeavesThePass(t *testing.T) {
	t.Parallel()
	stuckBehind(t, 2, true)
}
