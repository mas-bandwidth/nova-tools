package cardhdr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// `WHO: -` is no WHO line: the card is the fleet's, read with no error (an add was refused
// for it on 2026-10-04); `friend` and `friend <name>` read as before, and any other value
// is still refused.
func TestWhoDashIsTheFleet(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		brief string
		want  Who
		why   string
	}{
		{"c1: do it\nREPO: o/r\nWHO: -\n\nThe task.", Who{}, ""},
		{"c1: do it\nwho:   -  \n", Who{}, ""},
		{"c1: do it\nWHO: friend x\n", Who{Friend: true, Name: "x"}, ""},
		{"c1: do it\nWHO: friend\n", Who{Friend: true}, ""},
		{"c1: do it\nWHO: junk\n", Who{}, "WHO: junk is not `friend` or `friend <name>`"},
		{"c1: do it\nWHO: - -\n", Who{}, "is not `friend` or `friend <name>`"},
		{"c1: do it\nWHO: friend -\n", Who{}, "is not `friend` or `friend <name>`"},
	} {
		got, why := ReadWho(c.brief)
		assert.Equal(t, c.want, got, c.brief)
		assert.Equal(t, c.why == "", why == "", "%q: %q", c.brief, why)
		assert.Contains(t, why, c.why, c.brief)
	}
}
