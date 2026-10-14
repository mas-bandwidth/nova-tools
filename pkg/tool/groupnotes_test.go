package tool

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A group's help lists its verbs' examples under the exit codes, then the group's own
// note: a reader of only `<tool> <group> -h` sees how to run its verbs and, say, where
// an older set of them went. A group with no examples and no note prints neither.
func TestAGroupsHelpCarriesItsExamplesAndNote(t *testing.T) {
	t.Parallel()
	d := demo()
	d.Verbs = append(d.Verbs,
		Verb{Name: "grp a", Usage: "grp a --n <n>", Example: "grp a --n 1", Effect: Inspection, Run: func(*Call) *Out { return Done() }},
		Verb{Name: "grp b", Usage: "grp b", Effect: Inspection, Run: func(*Call) *Out { return Done() }},
	)
	d.GroupNotes = map[string]string{"grp": "  the old grp verbs are under fn now  "}

	var stdout, stderr bytes.Buffer
	assert.Equal(t, 0, d.Run([]string{"grp", "-h"}, strings.NewReader(""), &stdout, &stderr))
	assert.Empty(t, stderr.String())
	out := stdout.String()
	assert.Contains(t, out, "exit codes: ")
	assert.True(t, strings.HasSuffix(out, "\nexample:\n  nova-demo grp a --n 1\n\nthe old grp verbs are under fn now\n"), out)

	stdout.Reset()
	assert.Equal(t, 0, d.Run([]string{"fn", "-h"}, strings.NewReader(""), &stdout, &stderr))
	assert.NotContains(t, stdout.String(), "example:", "a group whose verbs name no example prints no example block")
	assert.NotContains(t, stdout.String(), "the old grp verbs", "a note is its own group's")
}
