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
	assert.Equal(t, []string{"address", "public", "user", "os", "arch", "cores", "roles", "runners", "note"}, Columns[Machine]())
	assert.Equal(t, []string{"harness", "model", "width", "tiers", "pronouns", "note", "enabled"}, Columns[Friend]())
	assert.Equal(t, []string{"machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log"}, Columns[Service]())
}

// TestListSQLNamesItsColumns: no *, and none of the old columns, so a store
// that still has them (an older friends table's slots, mode, session) is read as is.
func TestListSQLNamesItsColumns(t *testing.T) {
	t.Parallel()
	q := listSQL("friends", Columns[Friend]())
	assert.Equal(t, `SELECT name, coalesce("harness"::text, ''), coalesce("model"::text, ''), coalesce("width"::text, ''), coalesce("tiers"::text, ''), coalesce("pronouns"::text, ''), coalesce("note"::text, ''), coalesce("enabled"::text, ''), to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM config.friends ORDER BY name`, q)
	for _, cols := range [][]string{Columns[Machine](), Columns[Service](), Columns[Friend]()} {
		q := listSQL("t", cols)
		assert.NotContains(t, q, "*")
		for _, old := range []string{"slots", "mode", "session", "token_cap", "config_dir", "streams", "kinds", "gates_only", "coordinator"} {
			assert.NotContains(t, q, `"`+old+`"`)
		}
	}
}

func TestFillTypesEachField(t *testing.T) {
	t.Parallel()
	var f Friend
	r := Row{Name: "alice", Created: "c", Updated: "u", F: map[string]string{"harness": "claude", "width": "16", "tiers": "flash,pro", "pronouns": "she/her", "enabled": "true"}}
	require.NoError(t, fill(reflect.ValueOf(&f).Elem(), r))
	assert.Equal(t, Friend{Name: "alice", Harness: "claude", Width: 16, Tiers: "flash,pro", Pronouns: "she/her", Enabled: true, Created: "c", Updated: "u"}, f)
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
	var f Friend
	assert.EqualError(t, fill(reflect.ValueOf(&f).Elem(), Row{F: map[string]string{"width": "wide"}}), `width "wide": not an integer`)
	var s Service
	assert.EqualError(t, fill(reflect.ValueOf(&s).Elem(), Row{F: map[string]string{"argv": "prog"}}), "argv: not JSON of its type")
}

// A row type marshals to the fields nova-config <kind> show --json prints.
func TestJSONIsTheCLIFields(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Friend{Name: "alex", Harness: "codex", Width: 2, Tiers: "heavy,pro", Enabled: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"alex","harness":"codex","model":"","width":2,"tiers":"heavy,pro","pronouns":"","note":"","enabled":true,"created":"","updated":""}`, string(b))
}
