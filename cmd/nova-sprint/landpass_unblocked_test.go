package main

import (
	"context"
	"os"
	"path/filepath"
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
	deadline := make(chan time.Time, 1)
	parentAfter := r.a.after
	r.a.after = func(d time.Duration) <-chan time.Time {
		if d == LandDeadline {
			return deadline
		}
		return parentAfter(d)
	}
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
		deadline <- time.Now() // the higher-priority gate reached its bound
	case <-t.Context().Done():
		t.Error("second base could not gate while first base gate waited")
	}
	select {
	case <-pushed:
		// This hook runs immediately before the push. The first base gate
		// was abandoned, so only the second stream can reach it.
	case <-t.Context().Done():
		t.Error("green second base did not push while first base gate waited")
	}
	close(release)
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("land did not finish after releasing first gate")
	}
	assert.Equal(t, 1, code, "the abandoned batch is refused for this pass: %s%s", out, errs)
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s2-1": "landed/merged"}, r.places("s1-1", "s2-1"))
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
