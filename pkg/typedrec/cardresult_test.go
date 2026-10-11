package typedrec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// A card result is read in the card contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3): the six keys, a known verdict, a commit or `-` for the head.
func TestParseCardResultHoldsTheShape(t *testing.T) {
	t.Parallel()
	whole := "head: 0123456789abcdef0123456789abcdef01234567\nbranch: b\nverdict: ok\ngate: go test ./x\noutput: -\nreport: done it\ntitle: T\n\n## Body\n\nline one\nhead: not a key here\n"
	r := typedrec.ParseCardResult([]byte(whole))
	assert.True(t, r.Shaped)
	assert.Equal(t, "line one\nhead: not a key here", r.Body)
	assert.Equal(t, "T", r.Title)
	for name, text := range map[string]string{
		"no report":     strings.Replace(whole, "report: done it\n", "", 1),
		"empty report":  strings.Replace(whole, "report: done it", "report:", 1),
		"no gate":       strings.Replace(whole, "gate: go test ./x\n", "", 1),
		"a bad head":    strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "HEAD", 1),
		"a bad verdict": strings.Replace(whole, "verdict: ok", "verdict: done", 1),
		"the old shape": "rev: 0123456789abcdef0123456789abcdef01234567\nverdict: ok\n\n## One line\n\nx\n",
		"nothing":       "",
	} {
		assert.False(t, typedrec.ParseCardResult([]byte(text)).Shaped, name)
	}
	assert.True(t, typedrec.ParseCardResult([]byte(strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "-", 1))).Shaped, "a not-done result may name no head")
	assert.True(t, typedrec.ParseCardResult([]byte(strings.Replace(whole, "verdict: ok", "verdict: nothing", 1))).Shaped, "nothing is a verdict")
}

func TestTheShaPredicates(t *testing.T) {
	t.Parallel()
	full := "0123456789abcdef0123456789abcdef01234567"
	assert.True(t, typedrec.IsSha("0123456"))
	assert.True(t, typedrec.IsSha(full))
	assert.False(t, typedrec.IsSha("012345"), "under seven digits")
	assert.False(t, typedrec.IsSha("ABCDEF0"), "upper case")
	assert.False(t, typedrec.IsSha("a-1.w1"), "a card id")
	assert.True(t, typedrec.IsFullSha(full))
	assert.False(t, typedrec.IsFullSha("0123456"))
}
