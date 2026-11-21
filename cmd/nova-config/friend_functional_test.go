//go:build functional

package main

import (
	"context"
	"testing"

	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalLibrary: every kind the library reads, on a fresh store.
func TestFunctionalLibrary(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t)}
	for _, args := range [][]string{
		{"machine", "add", "m1", "--user", "nova", "--cores", "8", "--roles", "runner,bench", "--routes", "quest,gpu", "--actor", "a"},
		{"service", "add", "s1", "--machine", "m1", "--argv", `["/p","x y"]`, "--env", `{"A":"1"}`, "--every", "60", "--actor", "a"},
		{"friend", "add", "f1", "--harness", "claude", "--actor", "a"},
		{"provider", "add", "p1", "--url", "https://api.example.com", "--api", "openai", "--key", "P1_API_KEY", "--concurrency", "2", "--actor", "a"},
		{"model", "add", "p1/org/m-1.5:q4", "--price_in", "0", "--machine", "m1", "--actor", "a"},
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
	assert.Equal(t, []any{"m1", "nova", 8, "bench,runner", "gpu,quest"}, []any{ms[0].Name, ms[0].User, *ms[0].Cores, ms[0].Roles, ms[0].Routes})
	assert.Regexp(t, `^20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ$`, ms[0].Created)
	ss, err := st.Services(ctx)
	require.NoError(t, err)
	require.Len(t, ss, 1)
	assert.Equal(t, config.Service{Name: "s1", Machine: "m1", Argv: []string{"/p", "x y"}, Every: 60, Enabled: true, Env: map[string]string{"A": "1"}, Created: ss[0].Created, Updated: ss[0].Updated}, ss[0])
	fr, err := st.Friends(ctx)
	require.NoError(t, err)
	assert.Equal(t, []any{"f1", "claude", true}, []any{fr[0].Name, fr[0].Harness, fr[0].Enabled})
	ps, err := st.Providers(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.Provider{Name: "p1", URL: "https://api.example.com", API: "openai", Key: "P1_API_KEY", Concurrency: 2, Enabled: true, Created: ps[0].Created, Updated: ps[0].Updated}, ps[0])
	mds, err := st.Models(ctx)
	require.NoError(t, err)
	require.Len(t, mds, 1)
	require.NotNil(t, mds[0].PriceIn)
	assert.Equal(t, []any{"p1/org/m-1.5:q4", "opencode", 0, "m1"}, []any{mds[0].Name, mds[0].Harness, *mds[0].PriceIn, mds[0].Machine})
	assert.Nil(t, mds[0].PriceOut, "an unset price is NULL in the store and nil in the row: unpriced, never free")
	snap, err := st.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, config.Snapshot{Machines: ms, Friends: fr, Services: ss, Providers: ps, Models: mds}, snap, "one transaction reads what the five reads did")
	for _, stmt := range []string{
		`INSERT INTO config.models (name) VALUES ('p9/m')`,                     // no such provider, whoever writes
		`INSERT INTO config.models (name, machine) VALUES ('p1/m-2', 'nope')`,  // no such machine
		`INSERT INTO config.models (name, harness) VALUES ('p1/m-3', 'aider')`, // no such harness
		`INSERT INTO config.models (name, price_in) VALUES ('p1/m-4', -1)`,     // a negative price
		`INSERT INTO config.providers (name, url, api) VALUES ('p2', 'ftp://h', 'openai')`,
		`INSERT INTO config.providers (name, url, api, key) VALUES ('p2', 'https://h', 'openai', 'sk-1')`,
		// a URL with a user, a password, a query or a fragment, in any of the three columns, whoever writes
		`INSERT INTO config.providers (name, url, api) VALUES ('p2', 'https://u@h', 'openai')`,
		`INSERT INTO config.providers (name, url, api) VALUES ('p2', 'https://h/v1?key=k', 'openai')`,
		`INSERT INTO config.providers (name, url, api, models_url) VALUES ('p2', 'https://h', 'openai', 'https://u:pw@h/models')`,
		`INSERT INTO config.providers (name, url, api, models_url) VALUES ('p2', 'https://h', 'openai', 'https://h/models#x')`,
		`INSERT INTO config.providers (name, url, api, balance_url) VALUES ('p2', 'https://h', 'openai', 'https://u:pw@h/balance')`,
		`INSERT INTO config.providers (name, url, api, balance_url) VALUES ('p2', 'https://h', 'openai', 'https://h/balance?k=1')`,
		`UPDATE config.machines SET routes = 'route:quest' WHERE name = 'm1'`,
		`DELETE FROM config.providers WHERE name = 'p1'`, // a model still names it
	} {
		_, err := st.Conn().Exec(ctx, stmt)
		assert.Error(t, err, stmt)
	}
	_, err = st.Conn().Exec(ctx, `INSERT INTO config.providers (name, url, api, models_url, balance_url) VALUES ('p3', 'http://bench-1.tail:8080/v1', 'openai', 'https://h/v1/models', 'https://h/user/balance')`)
	assert.NoError(t, err, "a host with a port and a path is a URL the store takes")
	code, _, errs := realRun(t, env, "provider", "remove", "p1", "--actor", "a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "provider p1 is still named by models p1/org/m-1.5:q4; remove them first")
	_, err = st.Conn().Exec(ctx, "SELECT 1")
	require.NoError(t, err, "the snapshot's transaction is over: the connection is idle again")
}
