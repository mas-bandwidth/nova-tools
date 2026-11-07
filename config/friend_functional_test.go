//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	config "github.com/mas-bandwidth/nova-tools/v2/config/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalV1Friends models the live store: the v1 friends table and a
// sprint row naming one of them, then schema.sql twice. The v1 rows read
// through the new columns' defaults, a write leaves v1's columns alone, and
// the library reads what the CLI prints.
func TestFunctionalV1Friends(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t, "testdata/v1-friends.sql", "schema.sql")}
	ok := func(args ...string) string {
		t.Helper()
		code, out, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
		return out
	}
	assert.Equal(t, "FRIEND name=alex harness=- model=- width=2 tiers=heavy,pro pronouns=he/him note=- enabled=true\n"+
		"FRIEND name=rowan harness=- model=- width=32 tiers=flash,frontier,heavy,pro pronouns=they/them note=- enabled=true\n"+
		"CONFIG LIST kind=friend rows=2\n", ok("friend", "list"))
	assert.Equal(t, "CONFIG ADD kind=friend name=zhi rev=1\n", ok("friend", "add", "zhi", "--harness", "muse", "--actor", "a1"), "unstated pronouns pass v1's check as NULL")
	ok("friend", "set", "alex", "--harness", "codex", "--width", "3", "--actor", "a1")
	code, _, errs := realRun(t, env, "friend", "remove", "rowan", "--actor", "a1")
	assert.Equal(t, 1, code)
	assert.Equal(t, "nova-config friend remove: friend rowan is still named by config.sprint; change that row first, then: nova-config friend remove rowan --actor <you>\n", errs)
	code, _, errs = realRun(t, env, "friend", "set", "alex", "--pronouns", "xe/xem", "--actor", "a1")
	assert.Equal(t, 1, code, "v1's pronouns check is the store's: a refusal")
	assert.Contains(t, errs, "nova-config friend set: friend alex: ")

	ctx := context.Background()
	c, err := pgx.Connect(ctx, env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	var slots int
	var mode string
	require.NoError(t, c.QueryRow(ctx, "SELECT slots, mode FROM config.friends WHERE name = 'alex'").Scan(&slots, &mode))
	assert.Equal(t, []any{8, "batch"}, []any{slots, mode}, "v1's columns keep their values")

	st, err := config.Open(ctx, env["NOVA_PG_DSN"], "")
	require.NoError(t, err)
	defer st.Close()
	fr, err := st.Friends(ctx)
	require.NoError(t, err)
	require.Len(t, fr, 3)
	assert.Equal(t, config.Friend{Name: "alex", Harness: "codex", Width: 3, Tiers: "heavy,pro", Pronouns: "he/him", Enabled: true, Created: fr[0].Created, Updated: fr[0].Updated}, fr[0])
	assert.Equal(t, "", fr[2].Pronouns)
	assert.Equal(t, "flash,frontier,heavy,pro", fr[2].Tiers)
}

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
