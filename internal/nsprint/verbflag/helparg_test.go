package verbflag

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const demoHelp = `nova-demo: a demo

usage:
  nova-demo put --dir <dir>
  nova-demo fn load  --addr <host:port>
  nova-demo fn check --addr <host:port>
  nova-demo seat set|show <name>
  nova-demo version print the build identity
  nova-demo help print this text
  nova-demo <kind> list
  not-nova-demo get
`

func TestVerbsAreTheWordsTheHelpTextNames(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"put", "fn", "fn load", "fn check", "seat", "seat set", "seat show", "version"}, Verbs(demoHelp, "nova-demo"))
}

func TestHelpArgsPutsHelpStraightAfterTheVerb(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args, want []string
	}{
		{[]string{"put"}, []string{"put", "--help"}},
		{[]string{"put", "extra"}, []string{"put", "--help", "extra"}},
		{[]string{"put", "--", "x"}, []string{"put", "--help", "--", "x"}},
		{[]string{"put", "--json"}, []string{"put", "--help", "--json"}},
		{[]string{"fn", "load"}, []string{"fn", "load", "--help"}},
		{[]string{"fn", "load", "extra"}, []string{"fn", "load", "--help", "extra"}},
		{[]string{"fn", "extra"}, []string{"fn", "--help", "extra"}},
		{[]string{"fn", "--", "load"}, []string{"fn", "--help", "--", "load"}},
		{[]string{"seat", "show", "--json"}, []string{"seat", "show", "--help", "--json"}},
		{[]string{"machine", "show", "x"}, []string{"machine", "show", "--help", "x"}},
		{[]string{"bogus", "extra"}, []string{"bogus", "--help", "extra"}},
	} {
		assert.Equal(t, tc.want, HelpArgs(tc.args, demoHelp, "nova-demo", "machine show"), "help %v", tc.args)
	}
}
