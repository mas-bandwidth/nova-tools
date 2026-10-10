package friend

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A walled lane's opencode writes its session under the wall's HOME, not the daemon's: on
// 2026-10-10 every walled opencode lane read all-zero tokens from the daemon's database (the
// sums of no row), and its runs were judged empty. The tokens come from the first database
// that holds the session; one that none holds is unread (ErrSessionInNoDB), never zero.
func TestALanesTokensComeFromTheDatabaseThatHoldsItsSession(t *testing.T) {
	t.Parallel()
	const wall, daemon = "/w/.local/share/opencode/opencode.db", "/home/.local/share/opencode/opencode.db"
	sqlite := func(rows map[string]string, missing ...string) Exec {
		return func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			require.Equal(t, "sqlite3", name)
			db := args[1]
			if slices.Contains(missing, db) {
				return "", 1, nil
			}
			if out, ok := rows[db]; ok {
				return out, 0, nil
			}
			return "0|0|0|0|0|0|0\n", 0, nil // no row: the sums are zero, the count is 0
		}
	}
	ctx := context.Background()

	got, err := TokensFromOpenCodeIn(ctx, sqlite(map[string]string{wall: "12677|0|0|1433|0|0.01|1\n"}), []string{wall, daemon}, "ses_a")
	require.NoError(t, err)
	assert.Equal(t, int64(12677), got.Input, "the wall's database holds the lane's session")
	assert.Equal(t, int64(1433), got.Output)

	got, err = TokensFromOpenCodeIn(ctx, sqlite(map[string]string{daemon: "5|0|0|7|0|0|1\n"}), []string{wall, daemon}, "ses_b")
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.Input, "a batch turn's session is in the daemon's")

	// reversed: a session in no database is unread, naming each, never the zero row
	_, err = TokensFromOpenCodeIn(ctx, sqlite(nil, wall), []string{wall, daemon, daemon, ""}, "ses_c")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSessionInNoDB))
	assert.Contains(t, err.Error(), wall+" (sqlite3 "+wall+" exited 1)")
	assert.Contains(t, err.Error(), daemon+" (no row)")

	// the old single read: the zero row of a missing session read as a spend of nothing
	got, err = TokensFromOpenCode(ctx, sqlite(nil), daemon, "ses_c")
	require.NoError(t, err)
	assert.Zero(t, got.Sessions, "TokensFromOpenCode alone cannot tell a missing session from zero")
}
