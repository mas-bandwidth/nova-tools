package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init holds --coordinator to one word, as it holds --owner one block above
// and the seat move holds its name (seat.go): the holder's inbox is built
// under the holder's name, so a holder named a/../../escaped pushes
// judgments and makes directories outside the holder's tree.
func TestInitRefusesACoordinatorThatIsNotOneWord(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, line := range []string{
		"init --coordinator 'a/../../escaped'",
		"init --coordinator 'bad name'",
		"init --actor 'bad actor'", // the default from the actor is held to the same check
	} {
		before := ta.applies()
		code, _, errs := ta.do(line)
		require.Equal(t, 2, code, "%s: exit %d\n%s", line, code, errs)
		require.Contains(t, errs, "--coordinator wants letters, digits, _ and -", "%s: %s", line, errs)
		require.Equal(t, before, ta.applies(), "%s wrote", line)
	}
	ta.ok("init --coordinator good-name")
	assert.Contains(t, ta.ok("where"), "SPRINT TABLE  coordinator good-name\n", "a held name was refused too")
}
