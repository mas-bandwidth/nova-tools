package redisacl

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

var reLuaCall = regexp.MustCompile(`redis\.p?call\(\s*'([A-Za-z_]+)'`)

// directCalls is every command name a Lua text passes to redis.call or
// redis.pcall as a literal, upper-cased. Only the tests below read it, so it
// lives here and the shipped package carries no function the commands never
// reach (the deadcode class test).
func directCalls(src string) []string {
	src = reLuaComment.ReplaceAllString(src, "")
	var out []string
	for _, m := range reLuaCall.FindAllStringSubmatch(src, -1) {
		out = append(out, strings.ToUpper(m[1]))
	}
	return out
}

// Every command an embedded Lua file runs is allowed, by the rendered rules
// alone, to every role that may FCALL a function of that file: a function
// runs its commands under the caller's ACL, so a role granted the function
// and not its commands gets "The user executing the script can't run this
// command" (TIME in capacity.lua, under the coordinator, was the first). The
// rules are expanded with an empty catalog, so a command counts only when a
// rule names it: a category's membership is the store's, not this test's.
func TestEveryLuaCommandIsGrantedToItsCallers(t *testing.T) {
	t.Parallel()
	lib := fn.Spec()
	users, err := Render(lib)
	require.NoError(t, err)
	byRole := map[string]User{}
	for _, u := range users {
		byRole[u.Role] = u
	}
	cmds, err := LuaCommands(lib)
	require.NoError(t, err)
	require.Contains(t, cmds["lua/capacity.lua"], "time", "the reader of the Lua misses TIME")
	for _, r := range Roles() {
		allowed := Catalog{}.Commands(byRole[r.Name].Rules)
		for file, list := range cmds {
			calls := r.All
			for _, f := range r.Files {
				calls = calls || f == file
			}
			if !calls {
				continue
			}
			for _, c := range list {
				assert.True(t, allowed[c], "role %s may FCALL %s, which runs %s, and its rules do not allow it", r.Name, file, c)
			}
		}
	}
}

// A command a file passes to redis.call or redis.pcall by name is one the
// reader knows, so the filter that tells commands from reply words cannot
// drop a new one.
func TestEveryDirectLuaCallIsAKnownCommand(t *testing.T) {
	t.Parallel()
	lib := fn.Spec()
	files, err := fs.Glob(lib.Files, lib.Glob)
	require.NoError(t, err)
	n := 0
	for _, f := range files {
		b, err := fs.ReadFile(lib.Files, f)
		require.NoError(t, err)
		for _, c := range directCalls(string(b)) {
			n++
			assert.True(t, redisCommands[c], "%s calls %s, which redisacl's redisCommands does not list", f, c)
		}
	}
	assert.NotZero(t, n)
}

// The reader takes direct calls, commands built as data, and a container's
// subcommand, and leaves reply words, comments and a container named alone.
func TestCommandsOfReadsCallsDataAndSubcommands(t *testing.T) {
	t.Parallel()
	src := `local t = redis.call('TIME')
-- redis.call('KEYS', '*') is never run
T.stage(d, 'HSET', key, 'f', 'v')
local cmd = {'XADD', stream, '*'}
local info = redis.call('XINFO', 'STREAM', key)
return {'OK', 'REFUSED', T.refuse('CONFIG')}
`
	assert.Equal(t, []string{"hset", "time", "xadd", "xinfo|stream"}, commandsOf(src))
	assert.Equal(t, []string{"TIME", "XINFO"}, directCalls(src))
}
