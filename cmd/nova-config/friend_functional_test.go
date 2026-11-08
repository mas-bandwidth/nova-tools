//go:build functional

package main

import (
	"context"
	"testing"

	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalLibrary: Machines and Services on a fresh store.
func TestFunctionalLibrary(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t)}
	for _, args := range [][]string{
		{"machine", "add", "m1", "--user", "nova", "--cores", "8", "--roles", "runner,bench", "--actor", "a"},
		{"service", "add", "s1", "--machine", "m1", "--argv", `["/p","x y"]`, "--env", `{"A":"1"}`, "--every", "60", "--actor", "a"},
		{"friend", "add", "f1", "--harness", "claude", "--width", "4", "--actor", "a"},
	} {
		code, _, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
	}
	ctx := context.Background()
	st, err := config.Open(ctx, env["NOVA_PG_DSN"], "")
	require.NoError(t, err)
	defer st.Close()
	ms, err := st.Machines(ctx)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	require.NotNil(t, ms[0].Cores)
	assert.Equal(t, []any{"m1", "nova", 8, "bench,runner"}, []any{ms[0].Name, ms[0].User, *ms[0].Cores, ms[0].Roles})
	assert.Regexp(t, `^20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ$`, ms[0].Created)
	ss, err := st.Services(ctx)
	require.NoError(t, err)
	require.Len(t, ss, 1)
	assert.Equal(t, config.Service{Name: "s1", Machine: "m1", Argv: []string{"/p", "x y"}, Every: 60, Enabled: true, Env: map[string]string{"A": "1"}, Created: ss[0].Created, Updated: ss[0].Updated}, ss[0])
	fr, err := st.Friends(ctx)
	require.NoError(t, err)
	assert.Equal(t, []any{"f1", "claude", 4, true}, []any{fr[0].Name, fr[0].Harness, fr[0].Width, fr[0].Enabled})
	snap, err := st.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.Snapshot{Machines: ms, Friends: fr, Services: ss}, snap, "one transaction reads what the three reads did")
	_, err = st.Conn().Exec(ctx, "SELECT 1")
	require.NoError(t, err, "the snapshot's transaction is over: the connection is idle again")
}
