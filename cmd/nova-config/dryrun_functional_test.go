//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalDryRunReal (short: macOS caps the socket path under TMPDIR at 103 bytes): a dry-run runs the real statement and
// rolls it back, so it refuses what the real write refuses and changes nothing.
func TestFunctionalDryRunReal(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_PG_DSN": startPG(t)}
	for _, args := range [][]string{
		{"machine", "add", "m1", "--user", "nova", "--actor", "a1"},
		{"service", "add", "s1", "--machine", "m1", "--argv", `["prog"]`, "--keepalive", "true", "--actor", "a1"},
	} {
		code, _, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"service", "add", "s2", "--machine", "ghost", "--argv", `["prog"]`, "--every", "5", "--actor", "a1", "--dry-run"},
			"nova-config service add: no machine ghost; the machines are m1; run: nova-config machine add ghost --user <user>\n"},
		{[]string{"service", "set", "s1", "--machine", "ghost", "--actor", "a1", "--dry-run"},
			"nova-config service set: no machine ghost; the machines are m1; run: nova-config machine add ghost --user <user>\n"},
		{[]string{"machine", "remove", "m1", "--actor", "a1", "--dry-run"},
			"nova-config machine remove: machine m1 still runs services s1; remove them first: nova-config service remove s1 --actor <you>\n"},
	} {
		code, out, errs := realRun(t, env, tc.args...)
		assert.Equal(t, 1, code, "%v: %s", tc.args, errs)
		assert.Empty(t, out, "%v", tc.args)
		assert.Equal(t, tc.want, errs, "%v", tc.args)
	}
	code, out, errs := realRun(t, env, "machine", "set", "m1", "--note", "x", "--actor", "a1", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=machine name=m1 actor=a1 wrote=nothing\n  note: \"\" -> \"x\"\n", out)
	code, out, errs = realRun(t, env, "service", "remove", "s1", "--actor", "a1", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG DRY-RUN op=remove kind=service name=s1 actor=a1 wrote=nothing\n")

	ctx := context.Background()
	c, err := pgx.Connect(ctx, env["NOVA_PG_DSN"])
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	var note string
	var n int
	require.NoError(t, c.QueryRow(ctx, "SELECT note FROM config.machines WHERE name = 'm1'").Scan(&note))
	assert.Empty(t, note, "a dry-run set is rolled back")
	require.NoError(t, c.QueryRow(ctx, "SELECT count(*) FROM config.services WHERE name = 's1'").Scan(&n))
	assert.Equal(t, 1, n, "a dry-run remove is rolled back")
	require.NoError(t, c.QueryRow(ctx, "SELECT count(*) FROM config.history").Scan(&n))
	assert.Equal(t, 2, n, "a dry-run records no history")
	code, out, errs = realRun(t, env, "machine", "set", "m1", "--note", "x", "--actor", "a1")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG SET kind=machine name=m1 rev=3 changed=note\n", out, "a dry-run takes no history id")
}
