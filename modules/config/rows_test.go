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
	assert.Equal(t, []string{"address", "public", "user", "os", "arch", "cores", "roles", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "cpu_units", "memory_gb", "tags", "resources", "note"}, Columns[Machine]())
	assert.Equal(t, []string{"harness", "model", "width", "tiers", "pronouns", "note", "enabled"}, Columns[Friend]())
	assert.Equal(t, []string{"machine", "argv", "seat", "keys", "every", "keepalive", "enabled", "env", "log"}, Columns[Service]())
}

func TestMachineEffective(t *testing.T) {
	t.Parallel()
	m1 := Machine{OS: "darwin", Arch: "arm64", Tags: "gpu,metal"}
	assert.Equal(t, "mac", m1.EffectiveOS())
	assert.Equal(t, []string{"arm64", "gpu", "mac", "metal"}, m1.EffectiveTags())

	m2 := Machine{OS: "linux", Arch: "amd64", Tags: "linux,gpu"}
	assert.Equal(t, "linux", m2.EffectiveOS())
	assert.Equal(t, []string{"amd64", "gpu", "linux"}, m2.EffectiveTags())

	cpu, mem := 16, 64
	m3 := Machine{Resources: map[string]int{"gpu": 1}, CPUUnits: &cpu, MemoryGB: &mem}
	effRes := m3.EffectiveResources()
	assert.Equal(t, map[string]int{"cpu": 16, "gpu": 1, "memory": 64}, effRes)
	// Mutating the returned map should not affect the struct
	effRes["gpu"] = 2
	assert.Equal(t, 1, m3.Resources["gpu"])

	m4 := Machine{}
	assert.Equal(t, "", m4.EffectiveOS())
	assert.Equal(t, []string(nil), m4.EffectiveTags())
	assert.Equal(t, map[string]int{}, m4.EffectiveResources())
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
	require.NoError(t, fill(reflect.ValueOf(&m).Elem(), Row{Name: "m1", F: map[string]string{
		"cores": "12", "runners": "0", "cpu_units": "16", "memory_gb": "64", "tags": "gpu,metal", "resources": `{"gpu":1}`,
	}}))
	require.NotNil(t, m.Cores)
	assert.Equal(t, 12, *m.Cores)
	require.NotNil(t, m.CPUUnits)
	assert.Equal(t, 16, *m.CPUUnits)
	require.NotNil(t, m.MemoryGB)
	assert.Equal(t, 64, *m.MemoryGB)
	assert.Equal(t, "gpu,metal", m.Tags)
	assert.Equal(t, map[string]int{"gpu": 1}, m.Resources)
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
