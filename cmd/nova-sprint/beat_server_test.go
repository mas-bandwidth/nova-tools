package main

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The member's beat is `fleet beat <member> --stop-returns <n>` and, once this
// machine has named a load, `--load` last. The server used to refuse that, so
// no adopted member could beat. The count is what start waits on.
func TestTheMemberBeatPassesTheServer(t *testing.T) {
	t.Parallel()

	var sent [][]string
	w := &sprintwire.Worker{Send: func(_ context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
		require.Len(t, verbs, 1)
		sent = append(sent, verbs[0])
		as, words, why := workerVerb(verbs[0])
		require.Empty(t, why, "the member's beat must be a verb the server runs: %s", why)
		assert.Equal(t, "m", as)
		assert.Equal(t, 2, words)
		return []sprintwire.Result{{Code: 0, Stdout: "ok\n"}}, nil
	}}

	// Member.Beat with a sample: stop-returns, then the load (pkg/member).
	code, out := w.Run("fleet", "beat", "m", "--stop-returns", "0", "--load", "70.0")
	require.Equal(t, 0, code, "%s", out)

	// The next beat has no new sample. The wire repeats the load, and the count
	// still goes, including a count above zero.
	code, out = w.Run("fleet", "beat", "m", "--stop-returns", "1")
	require.Equal(t, 0, code, "%s", out)

	assert.Equal(t, [][]string{
		{"fleet", "beat", "m", "--stop-returns", "0", "--load", "70.0"},
		{"fleet", "beat", "m", "--stop-returns", "1", "--load", "70.0"},
	}, sent)

	as, words, why := workerVerb([]string{"fleet", "beat", "m", "--load", "1"})
	require.Empty(t, why, "a beat that names only a load is still a beat")
	assert.Equal(t, "m", as)
	assert.Equal(t, 2, words)

	for _, argv := range [][]string{
		{"fleet", "beat", "m", "--stop-returns", "-1", "--load", "1"},
		{"fleet", "beat", "m", "--stop-returns", "x", "--load", "1"},
		{"fleet", "beat", "m", "--stop-returns", "1", "--load", "1", "--cores", "4"},
		{"fleet", "beat", "m", "--load", "1", "--stop-returns", "1"},
	} {
		_, _, why = workerVerb(argv)
		assert.NotEmpty(t, why, "refused: %v", argv)
	}
}
