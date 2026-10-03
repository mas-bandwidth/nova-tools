package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A reader is named for its machine: reader-<m> is the one reader on machine m,
// and it runs at m's width, the fleet row's (the owner, 2026-10-02: "why not
// just have as many readers as workers per-machine"). A name of another shape
// names no machine.
func TestAReaderIsNamedForItsMachine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		reader, machine string
		ok              bool
	}{
		{"reader-superman", "superman", true},
		{"reader-bench-a", "bench-a", true},
		{"reader-", "", false},
		{"reader", "", false},
		{"r", "", false},
		{"reader-a/b", "", false},
	} {
		m, ok := ReaderMachine(c.reader)
		assert.Equal(t, c.ok, ok, c.reader)
		assert.Equal(t, c.machine, m, c.reader)
	}
}
