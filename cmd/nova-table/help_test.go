package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestEveryCommandHasEquivalentDiscoverableHelp(t *testing.T) {
	t.Parallel()
	code, banner, errout := runTable("help")
	require.EqualValues(t, 0, code, "top help: %d %q", code, errout)
	require.Empty(t, errout, "top help: %d %q", code, errout)
	for _, c := range commands {
		assert.Contains(t, banner, "nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n", "not discoverable: %s", c.name)
		words := strings.Fields(c.name)
		code, want, errout := runTable(append([]string{"help"}, words...)...)
		require.EqualValues(t, 0, code, "help %s: %d %q %q", c.name, code, want, errout)
		require.Empty(t, errout, "help %s: %d %q %q", c.name, code, want, errout)
		require.Contains(t, want, "usage: nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n", "help %s: %d %q %q", c.name, code, want, errout)
		require.Contains(t, want, "example:\n  nova-table "+c.example, "help %s: %d %q %q", c.name, code, want, errout)
		for _, flag := range []string{"--help", "-h"} {
			code, got, errout := runTable(append(append([]string{}, words...), flag)...)
			assert.EqualValues(t, 0, code, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
			assert.Equal(t, want, got, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
			assert.Empty(t, errout, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
		}
	}
	for _, group := range []string{"row", "col", "cell", "member", "view"} {
		_, want, _ := runTable("help", group)
		for _, alias := range []string{"--help", "-h", "help"} {
			code, got, errout := runTable(group, alias)
			assert.EqualValues(t, 0, code, "%s %s: %d %q %q", group, alias, code, got, errout)
			assert.Equal(t, want, got, "%s %s: %d %q %q", group, alias, code, got, errout)
			assert.Empty(t, errout, "%s %s: %d %q %q", group, alias, code, got, errout)
		}
	}
	_, create, _ := runTable("help", "create")
	require.LessOrEqual(t, strings.Index(create, "--columns <string>"), strings.Index(create, "--actor <string>"), "%v", "receipt metadata precedes product flags")
	_, show, _ := runTable("help", "view", "show")
	require.NotContains(t, show, "--summary", "%v", "view show advertises view set flags")
	require.NotContains(t, show, "--title", "%v", "view show advertises view set flags")
}
