package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The broadcast aliases and the two lists behind them are the part of address
// resolution a To line reaches at send time rather than as a hand-declared group, so
// nothing in ResolveList's own tests touches allNames, tableNames or ResolveBroadcast.
// These tests pin each one's main path and one refusal, against the package's roster
// helper and no store.

// TestAddressCoverAllNames pins allNames: every participant's name, in roster order,
// including the addressable participant with no lane.
func TestAddressCoverAllNames(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err)
	require.Equal(t, []string{"Ada", "Bo", "Dana"}, c.allNames(),
		`allNames() = %v, want every participant in roster order`, c.allNames())
}

// TestAddressCoverTableNames pins tableNames: every name but the sender's, in roster
// order, whoever the sender is.
func TestAddressCoverTableNames(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err)
	cases := []struct {
		name   string
		sender Participant
		want   []string
	}{
		{"first of the roster goes", Participant{Name: "Ada"}, []string{"Bo", "Dana"}},
		{"middle of the roster goes", Participant{Name: "Bo"}, []string{"Ada", "Dana"}},
		{"last of the roster goes", Participant{Name: "Dana"}, []string{"Ada", "Bo"}},
		{"a name the roster does not hold removes nobody", Participant{Name: "Zed"}, []string{"Ada", "Bo", "Dana"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, c.tableNames(tc.sender),
				`tableNames(%q) = %v, want %v`, tc.sender.Name, c.tableNames(tc.sender), tc.want)
		})
	}
}

// TestAddressCoverResolveBroadcast pins ResolveBroadcast's main path: the two aliases
// expanded against the roster, an alias beside a name keeping the name's place and
// collapsing the duplicate, and a line with no alias resolved word for word as
// ResolveList would.
func TestAddressCoverResolveBroadcast(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err)
	ada := mustParticipant(t, c, "Ada")
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"all is the whole roster", "all", []string{"Ada", "Bo", "Dana"}},
		{"the alias folds like every other token", "ALL", []string{"Ada", "Bo", "Dana"}},
		{"table is the roster but the sender", "table", []string{"Bo", "Dana"}},
		{"a name and an alias keep their order and collapse", "Bo; all", []string{"Bo", "Ada", "Dana"}},
		{"a group expands the same beside an alias", "Everybody on the bus; all", []string{"Ada", "Bo", "Dana"}},
		{"a line with no alias comes back word for word", "Ada a1b2c3d4 (active line)", []string{"Ada"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names, unknown := c.ResolveBroadcast(tc.line, ada)
			assert.Empty(t, unknown, "ResolveBroadcast(%q) left %v unresolved", tc.line, unknown)
			assert.Equal(t, tc.want, names, `ResolveBroadcast(%q) = %v, want %v`, tc.line, names, tc.want)
		})
	}
}

// TestAddressCoverResolveBroadcastRefuses pins the two refusals around the expansion: a
// token nobody holds stays unresolved so the send-time refusal can still name it, and a
// roster that really HAS an "all" or a "table" keeps that name instead of broadcasting.
func TestAddressCoverResolveBroadcastRefuses(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(writeBus(t, nil))
	require.NoError(t, err)
	ada := mustParticipant(t, c, "Ada")
	names, unknown := c.ResolveBroadcast("all; Boe", ada)
	assert.Equal(t, []string{"Ada", "Bo", "Dana"}, names, `ResolveBroadcast("all; Boe") = %v; the alias still expands`, names)
	assert.Equal(t, []string{"Boe"}, unknown, `ResolveBroadcast("all; Boe") left %v unresolved, want [Boe]: a misspelling survives the expansion`, unknown)

	holdRoot := writeBus(t, map[string]string{ConfigName: `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo"},
    {"name": "All"}
  ],
  "groups": [
    {"name": "Table", "members": ["Bo"]}
  ]
}`})
	hold, err := LoadConfig(holdRoot)
	require.NoError(t, err)
	holdAda := mustParticipant(t, hold, "Ada")
	// A participant named All is one reader, not everybody.
	names, unknown = hold.ResolveBroadcast("all", holdAda)
	assert.Empty(t, unknown)
	assert.Equal(t, []string{"All"}, names, `ResolveBroadcast("all") = %v; the roster holds "All", so the alias must not broadcast`, names)
	// A group named Table is that group, not the sender-less broadcast.
	names, unknown = hold.ResolveBroadcast("table", holdAda)
	assert.Empty(t, unknown)
	assert.Equal(t, []string{"Bo"}, names, `ResolveBroadcast("table") = %v; the roster holds the group "Table", so the alias must not broadcast`, names)
}
