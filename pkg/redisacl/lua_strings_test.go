package redisacl

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/redisfn"
)

// A file is read as Lua's own lexer reads it: a double-quoted string is a
// value like a single-quoted one, a "--" inside a quoted value is text of
// that value and not the start of a comment, and a comment (line or long)
// grants nothing whatever command words it holds. A quote ended by an escape
// does not close the string, so a fake command cannot cross a boundary.
// TestCommandsOfReadsCallsDataAndSubcommands holds the rest of the reading to
// it (data commands, reply words, a container's subcommand).
func TestLuaStringSyntaxKeepsCommandGrants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "double quoted call",
			src:  `redis.call("TIME")`,
			want: []string{"time"},
		},
		{
			name: "dashes in a quoted value keep the rest of the line",
			src:  `local marker='--'; redis.call('TIME')`,
			want: []string{"time"},
		},
		{
			name: "a line comment grants nothing",
			src:  "-- redis.call('KEYS', '*')",
			want: nil,
		},
		{
			name: "a long comment grants nothing and ends at its close",
			src:  "--[[\nredis.call('KEYS', '*')\n]]\nredis.call('TIME')",
			want: []string{"time"},
		},
		{
			name: "an escaped quote does not close the string",
			src:  `redis.call('SET', 'k', 'a\'-- b'); redis.call('TIME')`,
			want: []string{"set", "time"},
		},
		{
			name: "an escaped backslash ends the string at its own quote",
			src:  `local s='a\\'; redis.call("DEL", 'k')`,
			want: []string{"del"},
		},
		{
			name: "a fake command inside a quoted value grants nothing",
			src:  `local note="don't redis.call('KEYS') here"`,
			want: nil,
		},
		{
			name: "a call before a same-line comment is read",
			src:  "redis.call('TIME') -- redis.call('KEYS', '*')",
			want: []string{"time"},
		},
		{
			name: "data and container subcommands keep their names",
			src:  "T.stage(d, 'HSET', key)\n{'XADD', stream}\nredis.call('XINFO', 'STREAM', key)",
			want: []string{"hset", "xadd", "xinfo|stream"},
		},
		{
			name: "the same command twice is granted once, sorted",
			src:  `redis.call("TIME") redis.call('TIME') {'HSET'}`,
			want: []string{"hset", "time"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lib := redisfn.Library{
				Name:  "test",
				Files: fstest.MapFS{"lua/f.lua": &fstest.MapFile{Data: []byte(tc.src)}},
				Glob:  "lua/*.lua",
			}
			cmds, err := LuaCommands(lib)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cmds["lua/f.lua"])
		})
	}
}
