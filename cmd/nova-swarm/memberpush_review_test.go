package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// An operational ancestry-check failure cannot prove that the checkout has only
// one line of work (docs/SPEC-CARD-CONTRACT.md section 4).
func TestReviewAnAncestryErrorCannotChooseBetweenCheckoutTips(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	other := b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	g.git = func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
		if len(args) == 5 && args[0] == "merge-base" && args[1] == "--is-ancestor" && args[4] == other {
			return gitrun.Result{Stderr: []byte("fatal: injected object read failure\n")}, assert.AnError
		}
		return gitrun.Run(ctx, o, args...)
	}

	got := g.Push(b.p, member.Result{Head: wrongTail(head)})

	assert.Empty(t, got.Sha, "a failed ancestry check does not establish a unique tip")
	assert.NotEmpty(t, got.Refused, "the member refuses when uniqueness is unproved")
	assert.Empty(t, b.originHas(t, "sprint/c1"), "neither candidate is published")
}

// A fallback note reports a completed push, so a refused push cannot print it
// (docs/SPEC-CARD-CONTRACT.md section 4).
func TestReviewARefusedFallbackPushDoesNotClaimItWasPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	g.git = (&recordGit{refusePush: "fatal: injected credential failure"}).run
	var notes strings.Builder
	g.notes = &notes

	got := g.Push(b.p, member.Result{Head: wrongTail(head)})

	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "injected credential failure")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
	assert.NotContains(t, notes.String(), "was pushed", "failed pushes must not be reported as successful")
}
