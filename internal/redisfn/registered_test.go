package redisfn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registered reads the no-writes flag of the table form, wherever it stands in
// the table, and only there: the string form has no flags, a flag in a nested
// table that is not flags is not the function's, and a function's name in a
// file is paired with that file.
func TestRegisteredReadsTheNoWritesFlagOfTheTableForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		lua  string
		want []Function
	}{
		{"string form has no flags", fn("plain"), []Function{{Name: "plain", File: "a.lua"}}},
		{"flags after the name", "redis.register_function{function_name = 'ro', callback = function() end, flags = {'no-writes'}}\n",
			[]Function{{Name: "ro", File: "a.lua", NoWrites: true}}},
		{"flags before the name", "redis.register_function{flags = { 'no-writes' }, function_name = 'ro', callback = function() return {} end}\n",
			[]Function{{Name: "ro", File: "a.lua", NoWrites: true}}},
		{"another flag only", "redis.register_function{function_name = 'rw', callback = function() end, flags = {'allow-stale'}}\n",
			[]Function{{Name: "rw", File: "a.lua"}}},
		{"no-writes outside flags", "redis.register_function{function_name = 'rw', callback = function() local t = {'no-writes'} return t end}\n",
			[]Function{{Name: "rw", File: "a.lua"}}},
		{"parenthesised table", "redis.register_function({function_name = 'ro', flags = {'no-writes'}, callback = function() end})\n",
			[]Function{{Name: "ro", File: "a.lua", NoWrites: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lib := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": tc.lua}), Glob: "*.lua"}
			got, err := lib.Registered()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Registered names the same functions Functions does, sorted by name, each
// with the file that registers it.
func TestRegisteredIsFunctionsWithTheirFiles(t *testing.T) {
	t.Parallel()
	lib := Library{Name: "lib_one", Files: tree(map[string]string{
		"b.lua": fn("zeta") + "redis.register_function{function_name = 'alpha', flags = {'no-writes'}, callback = function() end}\n",
		"a.lua": fn("beta"),
	}), Glob: "*.lua"}
	names, err := lib.Functions()
	require.NoError(t, err)
	got, err := lib.Registered()
	require.NoError(t, err)
	assert.Equal(t, []Function{
		{Name: "alpha", File: "b.lua", NoWrites: true},
		{Name: "beta", File: "a.lua"},
		{Name: "zeta", File: "b.lua"},
	}, got)
	var gotNames []string
	for _, f := range got {
		gotNames = append(gotNames, f.Name)
	}
	assert.Equal(t, names, gotNames)
}
