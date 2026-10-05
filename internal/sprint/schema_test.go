package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadCardIDTakeParsesTheFirstAskAndAReask(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "p.r1.reader-a", ReadCardIDTake("p", 1, "reader-a", 1))
	assert.Equal(t, "p.r1.reader-a.t2", ReadCardIDTake("p", 1, "reader-a", 2))
	assert.Equal(t, "p.r1.reader-a.t3", ReadCardIDTake("p", 1, "reader-a", 3))

	p, n, r, ok := ParseReadCard("p.r1.reader-a")
	require.True(t, ok)
	assert.Equal(t, "p", p)
	assert.Equal(t, 1, n)
	assert.Equal(t, "reader-a", r)

	p, n, r, ok = ParseReadCard("s1-1.r2.reader-b.t2")
	require.True(t, ok)
	assert.Equal(t, "s1-1", p)
	assert.Equal(t, 2, n)
	assert.Equal(t, "reader-b", r, "the suffix is not part of the reader")

	for _, id := range []string{"p.r1.reader.extra", "p.r1.reader.t1", "p.r1.reader.t02", "a.b.c.t2", "p.r1.reader.t2.t3"} {
		_, _, _, ok := ParseReadCard(id)
		assert.False(t, ok, "ParseReadCard(%q)", id)
		assert.False(t, ValidCardID(id), "ValidCardID(%q)", id)
	}
	assert.True(t, ValidCardID("p.r1.reader-a"))
	assert.True(t, ValidCardID("p.r1.reader-a.t2"))
	assert.False(t, ValidCardID("a.b.c.d"))
}
