package redisacl

import (
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent/wire"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// lib is a small library: a ping file, a table file with one writer and one
// no-writes reader, and a capacity file.
func lib() redisfn.Library {
	return redisfn.Library{Name: "lib_one", Glob: "lua/*.lua", Files: fstest.MapFS{
		"lua/00_ping.lua":      {Data: []byte("redis.register_function('ns_ping', function() return 'PONG' end)\n")},
		"lua/table.lua":        {Data: []byte("redis.register_function('ns_table_set', function() end)\nredis.register_function{function_name = 'ns_table_read', flags = {'no-writes'}, callback = function() end}\n")},
		"lua/capacity.lua":     {Data: []byte("redis.register_function('ns_capacity_machine', function() end)\n")},
		"lua/friend_roles.lua": {Data: []byte("redis.register_function('ns_friend_roles', function() end)\n")},
	}}
}

func byRole(t *testing.T, users []User) map[string]User {
	t.Helper()
	out := map[string]User{}
	for _, u := range users {
		out[u.Role] = u
	}
	require.Len(t, out, 4)
	return out
}

// Each role's functions come from the library, by file: the coordinator
// every one, the member and the friend their files' (FCALL, and FCALL_RO
// for no-writes), the table reader only the no-writes ones through FCALL_RO;
// and no role but the coordinator may load the library.
func TestRenderGrantsEachRoleItsFilesFunctions(t *testing.T) {
	t.Parallel()
	users, err := Render(lib())
	require.NoError(t, err)
	r := byRole(t, users)
	cases := []struct {
		role       string
		has, lacks []string
	}{
		{Coordinator, []string{"~*", "+fcall|ns_capacity_machine", "+fcall|ns_friend_roles", "+fcall|ns_table_set", "+fcall_ro|ns_table_read", "+function|load"}, nil},
		{Member, []string{"~table:*", "~sprint:*", "~bench:*", "~tokens:ledger:*", "~ev:github", "%R~machine:*", "%R~loop:*", "%R~config:decl", "+function|list", "+fcall|ns_ping", "+fcall|ns_table_set", "+fcall|ns_table_read", "+fcall_ro|ns_table_read"},
			[]string{"~*", "+fcall|ns_capacity_machine", "+function|load"}},
		{Friend, []string{"~view:*", "~friend:*", "%R~bench:*", "+function|list", "+fcall|ns_table_set"}, []string{"+fcall|ns_friend_roles", "+function|load", "~bench:*"}},
		{Table, []string{"%R~table:*", "%R~sprint:*", "%R~bench:*", "+function|list", "+fcall_ro|ns_table_read"},
			[]string{"~table:*", "+fcall|ns_table_set", "+fcall|ns_table_read", "+@write", "+fcall|ns_ping"}},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()
			u := r[tc.role]
			assert.Equal(t, []string{"on", "clearselectors", "resetkeys", "resetchannels"}, u.Rules[:4], "a rendering starts from nothing and never names a password")
			for _, w := range tc.has {
				assert.Contains(t, u.Rules, w)
			}
			for _, w := range tc.lacks {
				assert.NotContains(t, u.Rules, w)
			}
			for _, rule := range u.Rules {
				assert.False(t, strings.HasPrefix(rule, ">") || strings.HasPrefix(rule, "#") || rule == "nopass", "password rule %q", rule)
			}
			assert.True(t, strings.HasPrefix(u.Line(), "ACL SETUSER "+u.Name+" on "))
		})
	}
	assert.Equal(t, 5, r[Coordinator].Functions)
	assert.Equal(t, 1, r[Table].Functions)
}

// A function added to a file reaches that file's roles with no change here.
func TestAFunctionAddedToAFileReachesItsRoles(t *testing.T) {
	t.Parallel()
	l := lib()
	files := l.Files.(fstest.MapFS)
	files["lua/table.lua"] = &fstest.MapFile{Data: append(append([]byte{}, files["lua/table.lua"].Data...), []byte("redis.register_function('ns_table_new', function() end)\n")...)}
	users, err := Render(l)
	require.NoError(t, err)
	r := byRole(t, users)
	assert.Contains(t, r[Member].Rules, "+fcall|ns_table_new")
	assert.Contains(t, r[Coordinator].Rules, "+fcall|ns_table_new")
	assert.NotContains(t, r[Table].Rules, "+fcall_ro|ns_table_new")
}

// A role naming a file the library lacks is refused, not rendered short.
func TestARoleNamingAMissingFileIsRefused(t *testing.T) {
	t.Parallel()
	l := lib()
	delete(l.Files.(fstest.MapFS), "lua/table.lua")
	_, err := Render(l)
	assert.ErrorContains(t, err, "lua/table.lua")
}

// Every file of the embedded library is named by a role or declared the
// coordinator's alone: a new file is a decision, not a silence.
func TestEveryLibraryFileHasItsRoles(t *testing.T) {
	t.Parallel()
	files, err := fs.Glob(fn.Spec().Files, fn.Spec().Glob)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	named := map[string]bool{}
	for _, f := range CoordinatorOnly {
		named[f] = true
	}
	for _, r := range Roles() {
		for _, f := range r.Files {
			named[f] = true
		}
	}
	for _, f := range files {
		assert.True(t, named[f], "%s is named by no role and not in CoordinatorOnly", f)
	}
	users, err := Render(fn.Spec())
	require.NoError(t, err)
	assert.Len(t, users, 4)
}

// The families are the owners' key shapes: a table's keys and registry, a
// view's, and the sprint's own.
func TestFamiliesAreTheOwnersKeys(t *testing.T) {
	t.Parallel()
	match := func(patterns []string, key string) bool {
		for _, p := range patterns {
			if ok, _ := path.Match(p, key); ok {
				return true
			}
		}
		return false
	}
	names := sprint.Names{}
	family := map[string][]string{}
	for _, f := range Families {
		family[f.Name] = f.Patterns
	}
	for name, ks := range map[string][]string{
		"tables":   {ntable.DefKey("work"), ntable.ChangesKey("work"), ntable.RowsKeyAt("work", 3), ntable.Registry},
		"views":    {"view:sprint", "views"},
		"sprint":   {names.EpochKey(), names.Key("beat:bench-a"), names.KeyAt("tick", 2)},
		"machines": {config.MachineKey("m"), config.MachineCeilingKey("m"), config.MachinesKey},
		"beats":    {config.BeatKey("m")},
		"friends":  {config.FriendBeatKey("f"), config.FriendsKey, "friend:f:roles", "friend:f:desired"},
		"fleet":    {config.FleetKey("store"), config.FleetKey("coordinator")},
		"loops":    {config.LoopsKey, config.LoopKey("member-a")},
		"config":   {config.DeclKey},
		"tokens":   {record.LedgerPrefix + "2026-09-30"},
		"events":   {wire.Stream},
	} {
		for _, k := range ks {
			assert.True(t, match(family[name], k), "%s: %s", name, k)
		}
	}
}

// The catalog expands rules in order, and a live user printed in other
// words is no drift; a missing key, an extra command, a selector and an off
// user are.
func TestCompareIsBySetsTheStoreExpands(t *testing.T) {
	t.Parallel()
	cat := Catalog{
		"read":      {"get", "hget", "keys"},
		"write":     {"set", "hset", "flushall", "function|load"},
		"dangerous": {"keys", "flushall"},
		"scripting": {"fcall", "function|load"},
	}
	want := User{Name: "u", Rules: []string{"on", "resetkeys", "~a:*", "%R~b:*", "-@all", "+@read", "+@write", "-@dangerous", "-@scripting", "+fcall|ns_x"}}
	same := Live{Exists: true, On: true, Keys: "~a:* %R~b:*", Commands: "-@all +get +hget +set +hset +fcall|ns_x"}
	assert.True(t, Compare(want, same, cat).None())
	otherWords := Live{Exists: true, On: true, Keys: "%RW~a:* %R~b:*", Commands: "+@all -keys -flushall -fcall -function +fcall|ns_x"}
	assert.True(t, Compare(want, otherWords, cat).None(), "%+v", Compare(want, otherWords, cat))

	cases := []struct {
		name string
		live Live
		want func(t *testing.T, d Drift)
	}{
		{"missing", Live{}, func(t *testing.T, d Drift) { assert.True(t, d.Missing) }},
		{"off", Live{Exists: true, Keys: same.Keys, Commands: same.Commands}, func(t *testing.T, d Drift) { assert.True(t, d.Off) }},
		{"selectors", Live{Exists: true, On: true, Keys: same.Keys, Commands: same.Commands, Selectors: 1}, func(t *testing.T, d Drift) { assert.True(t, d.Selectors) }},
		{"keys", Live{Exists: true, On: true, Keys: "~a:* ~c:*", Commands: same.Commands}, func(t *testing.T, d Drift) {
			assert.Equal(t, []string{"%R~b:*"}, d.KeysAdd)
			assert.Equal(t, []string{"~c:*"}, d.KeysDel)
		}},
		{"commands", Live{Exists: true, On: true, Keys: same.Keys, Commands: "-@all +@read +set +fcall|ns_y"}, func(t *testing.T, d Drift) {
			assert.Equal(t, []string{"fcall|ns_x", "hset"}, d.CommandsAdd)
			assert.Equal(t, []string{"fcall|ns_y", "keys"}, d.CommandsDel)
		}},
		{"channels", Live{Exists: true, On: true, Keys: same.Keys, Commands: same.Commands, Channels: "&*"}, func(t *testing.T, d Drift) {
			assert.Equal(t, []string{"&*"}, d.ChannelsDel)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := Compare(want, tc.live, cat)
			assert.False(t, d.None())
			tc.want(t, d)
		})
	}
}

// %RW~ is the store's ~; a drift line bounds each list.
func TestDriftFieldsAreBounded(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "~a:*", normKey("%RW~a:*"))
	var many []string
	for i := 0; i < MaxShown+3; i++ {
		many = append(many, string(rune('a'+i)))
	}
	f := Drift{Off: true, CommandsAdd: many}.Fields()
	assert.Contains(t, f, " off=true")
	assert.Contains(t, f, ",+3")
	assert.NotContains(t, f, many[MaxShown])
}
