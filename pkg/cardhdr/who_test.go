package cardhdr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The one parser of a brief's WHO line: `friend` is any friend, `friend <name>` one, no
// line is a machine's, anything else is refused naming the two forms; a WHO under the
// header's blank line is prose.
func TestReadWhoReadsAFriendOrNone(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		brief string
		want  Who
		why   string
	}{
		{"c1: do it (s1)\nREPO: o/r\n\nThe task.", Who{}, ""},
		{"c1: do it\nREPO: o/r\nWHO: friend\n\nThe task.", Who{Friend: true}, ""},
		{"c1: do it\nwho: Friend amy\n", Who{Friend: true, Name: "amy"}, ""},
		{"c1: do it\nWHO: only friend amy\n", Who{Friend: true, Only: true, Name: "amy"}, ""},
		{"c1: do it\n\nWHO: friend amy is prose here", Who{}, ""},
		{"c1: do it\nWHO: machine\n", Who{}, "WHO: machine is not `friend` or `friend <name>`"},
		{"c1: do it\nWHO: friend a b\n", Who{}, "is not `friend` or `friend <name>`"},
		{"c1: do it\nWHO: friend st.ella\n", Who{}, "is not `friend` or `friend <name>`"},
	} {
		got, why := ReadWho(c.brief)
		assert.Equal(t, c.want, got, c.brief)
		assert.Equal(t, c.why == "", why == "", "%q: %q", c.brief, why)
		assert.Contains(t, why, c.why, c.brief)
	}
}
