package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A quack call is one step: a generated card id that the store will refuse
// must keep every other stream in that call unchanged.
func TestQuackRereadRefusesOverlongGeneratedIDAtomically(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	stream := strings.Repeat("b", 128)
	code, out, errs := ta.do("quack --streams a," + stream + " --count 1 --repo https://example.com/quack.git")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, before, ta.applies(), "a refused pass must add no cards: %s%s", out, errs)
}

// Teardown removes the operation receipt and the epoch, so init starts again
// at epoch zero. Reusing an op after that lifecycle must still name new files
// because the test repository keeps files from the earlier pass.
func TestQuackRereadTeardownInitSameOpUsesNewFileNames(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op reused-after-teardown"
	first, _ := ta.quackCards(line)
	require.Len(t, first, 2)
	ta.ok("teardown --confirm sprint")
	ta.ok("init --readers reader-a,reader-b --members m1")
	second, _ := ta.quackCards(line)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s reuses a file path still present in the test repository", id)
	}
}

// Each store begins at epoch zero. The same caller operation may legitimately
// be used in two independent sprints that target the same test repository.
func TestQuackRereadIndependentStoresSameOpUseNewFileNames(t *testing.T) {
	t.Parallel()
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op shared-operation-name"
	firstStore := newTestApp(t)
	firstStore.ok("init --readers reader-a,reader-b --members m1")
	first, _ := firstStore.quackCards(line)
	require.Len(t, first, 2)
	secondStore := newTestApp(t)
	secondStore.ok("init --readers reader-a,reader-b --members m1")
	second, _ := secondStore.quackCards(line)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s repeats across independent sprint stores that target the same repository", id)
	}
}

// The recorded stamp must come from the moved quack card ids, regardless of
// how the caller spells its operation id. Result.Op precedes Result.Moved in
// the recorded JSON and may itself contain text shaped like a quack stamp.
func TestQuackRereadExactRetryIgnoresStampShapedOperationID(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op quack-deadbeefcafe-caller"
	first, _ := ta.quackCards(line)
	require.Len(t, first, 2)
	writes := ta.applies()
	code, out, errs := ta.do(line)
	assert.Equal(t, 0, code, out+errs)
	assert.Equal(t, writes, ta.applies(), "an exact retry writes nothing")
	for _, id := range first {
		assert.Contains(t, out, id, "the retry returns the recorded card")
	}
}

// quackDoneBarrier lets two public commands both complete their first Done
// read before either returns from it. Both therefore observe the same absent
// operation record, as overlapping retries can before the first commit.
type quackDoneBarrier struct {
	store.Backend
	target string
	mu     sync.Mutex
	reads  int
	ready  chan struct{}
}

func (b *quackDoneBarrier) Done(ctx context.Context, op string) (string, bool, error) {
	raw, ok, err := b.Backend.Done(ctx, op)
	if op != b.target {
		return raw, ok, err
	}
	b.mu.Lock()
	b.reads++
	if b.reads == 2 {
		close(b.ready)
	}
	ready := b.ready
	b.mu.Unlock()
	select {
	case <-ready:
		return raw, ok, err
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

// Two overlapping invocations with one caller operation id are the same
// operation. Both must return its one recorded result; an internal random
// stamp must not make their otherwise identical arguments conflict.
func TestQuackRereadOverlappingExactRetriesShareOneOperation(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	const op = "overlapping-quack"
	gate := &quackDoneBarrier{Backend: ta.m, target: op, ready: make(chan struct{})}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return gate, nil }
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op " + op
	type reply struct {
		code      int
		out, errs string
	}
	start, replies := make(chan struct{}), make(chan reply, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			code, out, errs := ta.do(line)
			replies <- reply{code: code, out: out, errs: errs}
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		r := <-replies
		assert.Equal(t, 0, r.code, r.out+r.errs)
		assert.NotEmpty(t, quackID.FindAllString(r.out, -1), r.out+r.errs)
	}
}
