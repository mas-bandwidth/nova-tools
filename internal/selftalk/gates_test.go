package selftalk

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedPattern is a pattern the scan skips by a literal gate, its needs, and sentences it matches.
type gatedPattern struct {
	re    *regexp.Regexp
	needs []string
	seeds []string
}

// gated is every pattern the scan skips by a literal gate.
func gated(t *testing.T) map[string]gatedPattern {
	t.Helper()
	out := map[string]gatedPattern{
		"dated": {dated, datedNeeds, []string{"On 2026-09-30 I am bad at estimating time.", "Measured that day: I cannot.", "once, at first"}},
		"instrument": {instrumentMarker, instrumentNeeds, []string{"RULE: my central pathology gets a second reader.",
			"the bar is high", "THE CHECK runs", "the tell : this"}},
		"aspiration": {aspiration, aspirationNeeds, []string{"I want my central pathology to stay in view.", "we would like it", "I hope to plan"}},
		"willNever":  {willNever, willNeverNeeds, []string{"I will never be a good planner.", "I'll never get it right"}},
		"rankIdiom":  {rankIdiom, rankIdiomNeeds, []string{"I try my best", "at least this", "the gift I most wanted", "most of it"}},
		"traitLead":  {traitLead, []string{"i "}, []string{"I hoard refusals and manufacture limits.", "so: I tend to rush"}},
	}
	for _, r := range installationRules {
		if r.needs == nil || r.Pattern == "" || strings.HasPrefix(r.Pattern, "a clause") {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		require.NoError(t, err, "rule %s: its Pattern is the expression it runs", r.Name)
		out[r.Name+" "+r.Says] = gatedPattern{re, r.needs, []string{r.Finds}}
	}
	require.Len(t, out, 6+14, "every gated pattern is under test")
	return out
}

// A gate is exact: no text a pattern matches is ever skipped by the pattern's gate. Each pattern
// is run on texts built from its own literal words and from sentences it matches, in random case
// and with the two runes that fold onto an ASCII letter from outside it (U+212A KELVIN SIGN,
// U+017F LONG S), so its matches are dense; every match must find one of its needs in the folded
// text.
func TestEveryLiteralGatePassesEveryMatch(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(3, 4))
	filler := []string{"work", "it", "a", "an", "the", "is", "are", "my", "I", "we", "and", "to", ":", "-", ",",
		";", ".", "2026-08-01", "\"", "—", "own", "habit", "practice", "instrument", "do", "have", "I'm"}
	word := regexp.MustCompile(`[A-Za-z']+`)
	for name, g := range gated(t) {
		vocab := append(append([]string{}, filler...), word.FindAllString(g.re.String(), -1)...)
		matched := 0
		// One buffer for every generated text, and a string only for a text the pattern
		// matches: this package's TestScanAllocatesAFewTimesTheInputNotOneIntPerByte reads the
		// process-wide allocation counter while this test runs beside it.
		var b []byte
		for i := 0; i < 3000; i++ {
			b = b[:0]
			seed := rng.IntN(5)
			for n := 2 + rng.IntN(10); n > 0; n-- {
				piece := vocab[rng.IntN(len(vocab))]
				if n == seed {
					piece = g.seeds[rng.IntN(len(g.seeds))]
				}
				for _, r := range piece {
					switch {
					case r == 'k' && rng.IntN(4) == 0:
						r = 'K'
					case r == 's' && rng.IntN(4) == 0:
						r = 'ſ'
					case rng.IntN(6) == 0:
						r = unicode.ToUpper(r)
					}
					b = utf8.AppendRune(b, r)
				}
				b = append(b, ' ')
			}
			b = b[:len(b)-1]
			if !g.re.Match(b) {
				continue
			}
			matched++
			if text := string(b); !mayMatch(fold(text), g.needs) {
				assert.Fail(t, "a gate skips a text its pattern matches", "%s: the gate %q skips %q", name, g.needs, text)
			}
		}
		assert.Positive(t, matched, "%s: no generated text matched, so the gate went untested", name)
	}
}
