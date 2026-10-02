package typedrec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResultFormatIsTheTwoLineContract: the brief is line 1 and line 2 (and,
// for a kind that needs one, the fields only the model can know); it never asks
// for a field the wrapper writes.
func TestResultFormatIsTheTwoLineContract(t *testing.T) {
	t.Parallel()

	for _, kind := range Kinds {
		f := ResultFormat(kind)
		require.Contains(t, f, "line 1: this card's line 1, verbatim", "KIND %s: no two-line contract:\n%s", kind, f)
		require.Contains(t, f, "`DONE`, `ABSTAIN <why>` or `BLOCKED <why>`", "KIND %s: no two-line contract:\n%s", kind, f)
		for _, owned := range WrapperOwned {
			assert.NotContains(t, f, "`"+owned+":", "KIND %s: the brief asks the model for %s, a field the wrapper writes:\n%s", kind, owned, f)
		}
		fields, sections := JudgementFields(kind)
		for _, x := range fields {
			assert.Contains(t, f, "`"+x+": <value>`", "KIND %s: the brief does not name %s, which only the model knows", kind, x)
		}
		for _, s := range sections {
			assert.Contains(t, f, "`## "+s+"`", "KIND %s: the brief does not name ## %s", kind, s)
		}
	}
	f, s := JudgementFields(KindFix)
	assert.Empty(t, f, "a fix card needs nothing from the model beyond two lines")
	assert.Empty(t, s, "a fix card needs nothing from the model beyond two lines")
}
