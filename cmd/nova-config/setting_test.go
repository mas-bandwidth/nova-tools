package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setting set sprint.<name> <value> is the sprint row's set of that one field: the value
// shows on the row, a bad value is refused naming the setting and its range and writes
// nothing, and a name that is no sprint field is refused naming the fields.
func TestSettingSetIsTheSprintRowsField(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	code, out, errs := h.run(t, words(toolExamples["migrate"])[1:]...)
	require.Equal(t, 0, code, out+errs)

	code, out, errs = h.run(t, words(toolExamples["setting set"])[1:]...)
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, errs)
	_, show, _ := h.run(t, "sprint", "show", "--file", "try.json")
	assert.Contains(t, show, "deal_ahead=3", show)

	code, _, errs = h.run(t, "setting", "set", "sprint.deal_ahead", "0", "--as", "a1", "--file", "try.json")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "deal_ahead wants a whole number from 1 to 10")
	_, show, _ = h.run(t, "sprint", "show", "--file", "try.json")
	assert.Contains(t, show, "deal_ahead=3", "a refused value writes nothing")

	code, _, errs = h.run(t, "setting", "set", "sprint.no_such", "3", "--as", "a1", "--file", "try.json")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `no setting "sprint.no_such"; want sprint.<name>`)
	code, _, errs = h.run(t, "setting", "set", "route.deal_ahead", "3", "--as", "a1", "--file", "try.json")
	assert.Equal(t, 2, code, errs)
	code, _, _ = h.run(t, "setting", "set", "sprint.deal_ahead")
	assert.Equal(t, 2, code, "no value")
}
