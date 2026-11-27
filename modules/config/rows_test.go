package config

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColumns(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"address", "public", "user", "os", "arch", "cores", "roles", "routes", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "note"}, Columns[Machine]())
	assert.Equal(t, []string{"harness", "model", "pronouns", "note", "enabled"}, Columns[Friend]())
	assert.Equal(t, []string{"machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log"}, Columns[Service]())
	assert.Equal(t, []string{"url", "api", "key", "models_url", "balance_url", "concurrency", "note", "enabled"}, Columns[Provider]())
	assert.Equal(t, []string{"harness", "price_in", "price_out", "price_cache_read", "machine", "note", "enabled"}, Columns[Model]())
}

// TestListSQLNamesItsColumns: no *, and none of the old columns, so a store
// that still has them (an older friends table's slots, mode, session) is read as is.
func TestListSQLNamesItsColumns(t *testing.T) {
	t.Parallel()
	q := listSQL("friends", Columns[Friend]())
	assert.Equal(t, `SELECT name, coalesce("harness"::text, ''), coalesce("model"::text, ''), coalesce("pronouns"::text, ''), coalesce("note"::text, ''), coalesce("enabled"::text, ''), to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM config.friends ORDER BY name`, q)
	for _, cols := range [][]string{Columns[Machine](), Columns[Service](), Columns[Friend](), Columns[Provider](), Columns[Model]()} {
		q := listSQL("t", cols)
		assert.NotContains(t, q, "*")
		for _, old := range []string{"slots", "mode", "session", "token_cap", "config_dir", "streams", "kinds", "gates_only", "coordinator", "width", "tiers", "provider"} {
			assert.NotContains(t, q, `"`+old+`"`)
		}
	}
}

func TestFillTypesEachField(t *testing.T) {
	t.Parallel()
	var f Friend
	r := Row{Name: "alice", Created: "c", Updated: "u", F: map[string]string{"harness": "claude", "pronouns": "she/her", "enabled": "true"}}
	require.NoError(t, fill(reflect.ValueOf(&f).Elem(), r))
	assert.Equal(t, Friend{Name: "alice", Harness: "claude", Pronouns: "she/her", Enabled: true, Created: "c", Updated: "u"}, f)
	var md Model
	require.NoError(t, fill(reflect.ValueOf(&md).Elem(), Row{Name: "p1/m-1", F: map[string]string{"harness": "opencode", "price_in": "0", "price_out": ""}}))
	require.NotNil(t, md.PriceIn)
	assert.Equal(t, 0, *md.PriceIn, "a price of 0 is free")
	assert.Nil(t, md.PriceOut, "an unset price is unpriced, never 0")
	var m Machine
	require.NoError(t, fill(reflect.ValueOf(&m).Elem(), Row{Name: "m1", F: map[string]string{"cores": "12", "runners": "0"}}))
	require.NotNil(t, m.Cores)
	assert.Equal(t, 12, *m.Cores)
	assert.Empty(t, m.Public, "an unset public address is empty")
	require.NoError(t, fill(reflect.ValueOf(&m).Elem(), Row{Name: "m1", F: map[string]string{"public": "edge.example.com"}}))
	assert.Equal(t, "edge.example.com", m.Public)
	m = Machine{}
	require.NoError(t, fill(reflect.ValueOf(&m).Elem(), Row{Name: "m1", F: map[string]string{"cores": ""}}))
	assert.Nil(t, m.Cores, "an unset int is nil")
	var s Service
	require.NoError(t, fill(reflect.ValueOf(&s).Elem(), Row{Name: "s1", F: map[string]string{"argv": `["/p","-x"]`, "env": `{"A":"b"}`, "every": "5"}}))
	assert.Equal(t, []string{"/p", "-x"}, s.Argv)
	assert.Equal(t, map[string]string{"A": "b"}, s.Env)
	assert.Equal(t, 5, s.Every)
}

func TestFillRefusesABadValue(t *testing.T) {
	t.Parallel()
	var p Provider
	assert.EqualError(t, fill(reflect.ValueOf(&p).Elem(), Row{F: map[string]string{"concurrency": "wide"}}), `concurrency "wide": not an integer`)
	var s Service
	assert.EqualError(t, fill(reflect.ValueOf(&s).Elem(), Row{F: map[string]string{"argv": "prog"}}), "argv: not JSON of its type")
}

// A row type marshals to the fields nova-config <kind> show --json prints.
func TestJSONIsTheCLIFields(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Friend{Name: "alex", Harness: "codex", Enabled: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"alex","harness":"codex","model":"","pronouns":"","note":"","enabled":true,"created":"","updated":""}`, string(b))
	b, err = json.Marshal(Model{Name: "p1/m-1", Harness: "opencode", Enabled: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"p1/m-1","harness":"opencode","price_in":null,"price_out":null,"price_cache_read":null,"machine":"","note":"","enabled":true,"created":"","updated":""}`, string(b))
}
