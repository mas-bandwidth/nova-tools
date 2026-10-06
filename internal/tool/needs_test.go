package tool

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHelpReadsTheDeclaredNeeds pins the banner and `help <verb>` to Needs: a
// declared tool's banner carries its row, a verb with a row of its own carries
// that row, and a tool with no row prints no needs line.
func TestHelpReadsTheDeclaredNeeds(t *testing.T) {
	t.Parallel()
	verbs := []Verb{
		{Name: "run", Usage: "run", Example: "run", Effect: Inspection, Run: func(c *Call) *Out { return Done() }},
		{Name: "install", Usage: "install", Example: "install", Effect: LocalWrite, Run: func(c *Call) *Out { return Done() }},
	}
	declared := &Tool{Name: "nova-friend", What: "a test", ExitTable: "0 done", Verbs: verbs}
	assert.Contains(t, declared.Banner(), "\nneeds: Redis\nexit codes: 0 done\n")

	var out, errs bytes.Buffer
	assert.Equal(t, 0, declared.Run([]string{"help", "install"}, nil, &out, &errs), errs.String())
	assert.Contains(t, out.String(), "\neffect: local write: writes files on this machine; needs: Redis; runs: launchctl; platforms: darwin\n")
	out.Reset()
	assert.Equal(t, 0, declared.Run([]string{"help", "run"}, nil, &out, &errs), errs.String())
	assert.Contains(t, out.String(), "\neffect: inspection: reads, writes nothing; needs: Redis\n", "a verb with no row of its own carries the tool's")

	undeclared := &Tool{Name: "nova-demo", What: "a test", ExitTable: "0 done", Verbs: verbs}
	assert.NotContains(t, undeclared.Banner(), "needs:")
}
