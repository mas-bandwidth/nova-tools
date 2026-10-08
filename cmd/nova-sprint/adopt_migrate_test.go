package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migrateDryJSON is nova-config migrate --dry-run --json as the store of
// 2026-10-08 answered the seat's role: the ledger at from, the build at to,
// ready as said, and every table owned by owner when the role is not it.
func migrateDryJSON(role, owner string, from, to int) string {
	pending := to - from
	ready, status, items, why := "yes", "ok", "", ""
	if owner != "" && owner != role && pending > 0 {
		ready, status = "no", "failed"
		why = fmt.Sprintf(`"role %s cannot apply migration %d and applied none: role %s owns schema config and every table in it, and only the owner alters and fills them, so %s runs this once"`, role, to, owner, owner)
		for _, tb := range []string{"fleet", "friends"} {
			items += fmt.Sprintf(`{"kind":"not_owned","fields":{"table":"config.%s","owner":"%s","role":"%s"}},`, tb, owner, role)
		}
		items = strings.TrimSuffix(items, ",")
	}
	return fmt.Sprintf(`{"result":{"verb":"migrate","status":"%s","exit":0,"remedy":"NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD nova-config migrate --pg postgres://%s@db:5432/nova","why":[%s]},`+
		`"facts":{"pg":"%s@db:5432/nova","from":%d,"to":%d,"applied":0,"dry_run":true,"pending":%d,"missing":0,"role":"%s","ready":"%s"},"items":[%s]}`,
		status, owner, why, role, from, to, pending, role, ready, items)
}

// migrateRig is a store the fake runner plays: its ledger, its owner and the
// role each command runs as; every command is logged.
type migrateRig struct {
	schema, carries int
	owner           string
	calls           []string
	refuseAs        string // a role whose migrate is refused
}

func (m *migrateRig) run(_ context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	m.calls = append(m.calls, line)
	as := "nova_admin"
	if name == "env" {
		// NOVA_PG_PASSWORD_ENV=NOVA_PG_<ROLE>_PASSWORD names the role the run is as
		_, key, _ := strings.Cut(args[0], "=")
		as = "nova_" + strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(key, "_PASSWORD"), "NOVA_PG_"))
	}
	if strings.Contains(line, "--dry-run") {
		out := migrateDryJSON(as, m.owner, m.schema, m.carries)
		var err error
		if strings.Contains(out, `"ready":"no"`) {
			err = errors.New("exit status 1")
		}
		return out, err
	}
	if as == m.refuseAs {
		return "nova-config migrate REFUSED: role " + as + " cannot apply", errors.New("exit status 1")
	}
	if m.owner != "" && as != m.owner {
		return "nova-config migrate REFUSED: role " + as + " cannot apply", errors.New("exit status 1")
	}
	m.schema = m.carries
	return fmt.Sprintf("CONFIG MIGRATE pg=%s@db:5432/nova from=35 to=36 applied=1", as), nil
}

func TestAdoptMigrateRunsAsTheOwnerTheDryRunNamesAndRefusesAnUnappliedSchema(t *testing.T) {
	t.Parallel()
	const dsn = "postgres://nova_admin@db:5432/nova"
	env := func(k string) string {
		if k == "NOVA_PG_CONFIG_PASSWORD" {
			return "set-but-never-printed"
		}
		return ""
	}

	t.Run("the owner is read from the dry run, never assumed, and migrate runs as it", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{schema: 35, carries: 36, owner: "nova_config"}
		ev, err := adoptMigrate(context.Background(), m.run, "/out/darwin-arm64/nova-config", dsn, env)
		require.NoError(t, err)
		assert.Equal(t, "role=nova_admin as=nova_config from=35 to=36 applied=1", ev)
		require.Len(t, m.calls, 3, "%q", m.calls)
		assert.Equal(t, "/out/darwin-arm64/nova-config migrate --dry-run --json --pg "+dsn, m.calls[0])
		assert.Equal(t, "env NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD /out/darwin-arm64/nova-config migrate --pg postgres://nova_config@db:5432/nova", m.calls[1], "as the owner, the password by its variable's name")
		assert.Equal(t, 36, m.schema)
		for _, c := range m.calls {
			assert.NotContains(t, c, "set-but-never-printed", "no password is on a command line")
		}
	})

	t.Run("a role that owns the schema migrates as itself", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{schema: 35, carries: 36, owner: "nova_admin"}
		ev, err := adoptMigrate(context.Background(), m.run, "nova-config", dsn, env)
		require.NoError(t, err)
		assert.Equal(t, "role=nova_admin as=nova_admin from=35 to=36 applied=1", ev)
		assert.Equal(t, "nova-config migrate --pg "+dsn, m.calls[1])
	})

	t.Run("nothing pending runs no migrate", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{schema: 36, carries: 36, owner: "nova_config"}
		ev, err := adoptMigrate(context.Background(), m.run, "nova-config", dsn, env)
		require.NoError(t, err)
		assert.Equal(t, "role=nova_admin schema=36 applied=0: the store already carries the build's schema", ev)
		assert.Len(t, m.calls, 1)
	})

	t.Run("the owner's password not at hand refuses, naming the variable, and runs no migrate", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{schema: 35, carries: 36, owner: "nova_config"}
		_, err := adoptMigrate(context.Background(), m.run, "nova-config", dsn, func(string) string { return "" })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nova_config owns schema config")
		assert.Contains(t, err.Error(), "export NOVA_PG_CONFIG_PASSWORD")
		assert.Len(t, m.calls, 1, "the dry run only")
		assert.Equal(t, 35, m.schema)
	})

	t.Run("a migrate that leaves the schema behind refuses the switch", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{schema: 35, carries: 36, owner: "nova_config", refuseAs: "nova_config"}
		_, err := adoptMigrate(context.Background(), m.run, "nova-config", dsn, env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "migrate as nova_config")
		assert.Equal(t, 35, m.schema)
	})

	t.Run("no store named refuses before anything runs", func(t *testing.T) {
		t.Parallel()
		m := &migrateRig{}
		_, err := adoptMigrate(context.Background(), m.run, "nova-config", "", env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--pg")
		assert.Empty(t, m.calls)
	})
}

func TestReadMigrateDryReadsTheFactsAndTheOwners(t *testing.T) {
	t.Parallel()
	d, err := readMigrateDry("SECRETS EXEC OK\n" + migrateDryJSON("nova_admin", "nova_config", 35, 36))
	require.NoError(t, err)
	assert.Equal(t, "failed", d.status)
	assert.Equal(t, "nova_admin", d.role)
	assert.Equal(t, "no", d.ready)
	assert.Equal(t, 35, d.from)
	assert.Equal(t, 36, d.to)
	assert.Equal(t, 1, d.pending)
	assert.Equal(t, []string{"nova_config"}, d.owners)
	_, err = readMigrateDry("nova-config: command not found")
	assert.Error(t, err)
}
