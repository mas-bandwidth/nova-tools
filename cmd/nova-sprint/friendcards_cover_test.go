package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// TestFriendcardsCoverBranchTipRefusesATipItCannotRead covers branchTip
// (cmd/nova-sprint/friendcards.go:148, the finding's 0.0%): the refusal a
// caller of the tip sees when the read cannot run at all. branchTip is the
// real tipFn, and its body is one bounded git ls-remote, so origin's tip
// itself cannot be read here without a git child and the network beyond it;
// what the unit tier reaches is the refusal both callers see when the read is
// ended before git starts: a caller's bound already past (reported as a
// *subproc.TimeoutError naming the ls-remote and its ref, what friendFinish
// wraps as "cannot be read") and a caller's own cancellation (passed through,
// not blamed on the budget). No git runs, no network, no clock wait: a child
// whose context is already done is never started. The read of the tip itself
// -- the ls-remote, its ref-line parse and the empty tip for a branch origin
// does not hold -- needs a git child and is named in the report as not-done.
func TestFriendcardsCoverBranchTipRefusesATipItCannotRead(t *testing.T) {
	t.Parallel()
	a := &app{} // branchTip reads only a.gitEnv, and no git runs here to read it
	for _, c := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		is   error
		kill bool
	}{
		{
			name: "the tip's budget is already spent: the kill the caller sees",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(t.Context(), time.Now().Add(-friendTipBudget))
			},
			is:   context.DeadlineExceeded,
			kill: true,
		},
		{
			name: "the caller cancelled the read before it started",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx, cancel
			},
			is: context.Canceled,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := c.ctx()
			defer cancel()
			head, err := a.branchTip(ctx, friendRepo, "sprint/s1-1.w1.g1.e0")
			require.Error(t, err, "a read that cannot start returns no tip")
			assert.Empty(t, head, "a refused read names no head")
			require.ErrorIs(t, err, c.is, err.Error())
			ref := "refs/heads/sprint/s1-1.w1.g1.e0"
			if c.kill {
				var killed *subproc.TimeoutError
				require.ErrorAs(t, err, &killed, "the budget's end is reported as the kill: "+err.Error())
				assert.Equal(t, "git ls-remote -- "+friendRepo+" "+ref, killed.What,
					"the kill names the one ls-remote and the ref it asked for")
				assert.Equal(t, killed.What+" did not finish before its deadline and was killed", killed.Error())
			} else {
				assert.NotErrorIs(t, err, context.DeadlineExceeded,
					"the caller's own cancellation is not blamed on the budget")
				assert.NotContains(t, err.Error(), "was killed")
			}
		})
	}
}
