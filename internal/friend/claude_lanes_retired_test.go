package friend

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeReadModelsAreTheBudRunnersTable pins the tier-to-model table the
// buds' hand runner and reader carried (frontier claude-fable-5-1, heavy
// claude-opus-5-5, pro claude-sonnet-5-5, flash claude-haiku-4-5-20251001):
// a claude account's reads run on the model of the read's tier, so retiring
// the scripts may not drop a row of the table.
func TestClaudeReadModelsAreTheBudRunnersTable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, map[string]string{
		"frontier": "claude-fable-5-1",
		"heavy":    "claude-opus-5-5",
		"pro":      "claude-sonnet-5-5",
		"flash":    "claude-haiku-4-5-20251001",
	}, ReadModels)
}

// TestALaneDaemonRecordsABrokenReadWithItsFinding: a read whose RESULT.md says
// `verdict: broken` is recorded with `read --broken` and the finding (the
// report line and the body) and is never returned; the bud's reader.zsh wrote
// the same verdict from the same RESULT.md shape.
func TestALaneDaemonRecordsABrokenReadWithItsFinding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": strings.Replace(okResult, "verdict: ok", "verdict: broken", 1)}}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 2)
		r.run(t, 30)

		broken := sp.verbs("--broken")
		require.Len(t, broken, 1, "a broken verdict is recorded once with read --broken")
		assert.Equal(t, "a.w1", broken[0][4])
		assert.Equal(t, "15", flagValue(broken[0], "--epoch"))
		finding := flagValue(broken[0], "--finding")
		assert.Contains(t, finding, "fine")
		assert.Contains(t, finding, "no findings")
		assert.Contains(t, flagValue(broken[0], "--usage"), "model=m-pro")
		assert.Empty(t, sp.verbs("--ok"), "a broken verdict is not recorded ok")
	})
}
