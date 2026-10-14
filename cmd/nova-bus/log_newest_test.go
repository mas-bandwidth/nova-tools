package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogDefaultsToTheNewestAndNamesPeekTakeMax pins the readers' three
// complaints (SPEC-BUS.md, the log; docs/STANDARD.md, output is bounded with a
// total and the flag that widens it): log defaults to the newest window, printed
// oldest-first within it, with a MORE line naming the older entries and
// --oldest selecting the oldest window the store reads from "-"; and names and
// peek take --max <n> with a MORE line like every listing verb.
func TestLogDefaultsToTheNewestAndNamesPeekTakeMax(t *testing.T) {
	t.Parallel()

	r := newRig("ada", "bob")
	cli := r.cli()
	// a fake log of thirty messages: m0 is the oldest, m29 the newest
	for i := 0; i < 30; i++ {
		cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", fmt.Sprintf("m%d", i), "--body", "x")
	}
	require.Equal(t, 30, r.store.Len(bus.LogKey), "the fake log holds thirty messages")

	// the default is the newest twenty, oldest-first within the window: m10..m29
	newest := cli.Do(t, "log", "--as", "ada").Exit(0)
	assert.Contains(t, newest.Stdout, "LOG OK total=30")
	assert.NotContains(t, newest.Stdout, `subject="m0"`)
	assert.NotContains(t, newest.Stdout, `subject="m9"`)
	assert.Contains(t, newest.Stdout, `subject="m10"`)
	assert.Contains(t, newest.Stdout, `subject="m29"`)
	assert.Less(t, strings.Index(newest.Stdout, `subject="m10"`), strings.Index(newest.Stdout, `subject="m29"`),
		"the window prints oldest-first")
	assert.Contains(t, newest.Stdout, "LOG MORE kind=message shown=20 total=30")
	assert.Contains(t, newest.Stdout, "10 older", "the MORE line names the older entries")

	// --oldest is today's window: the first twenty, m0..m19
	oldest := cli.Do(t, "log", "--as", "ada", "--oldest").Exit(0)
	assert.Contains(t, oldest.Stdout, "LOG OK total=30")
	assert.Contains(t, oldest.Stdout, `subject="m0"`)
	assert.Contains(t, oldest.Stdout, `subject="m19"`)
	assert.NotContains(t, oldest.Stdout, `subject="m20"`)
	assert.Contains(t, oldest.Stdout, "LOG MORE kind=message shown=20 total=30")

	// names --max 1 prints one name and a MORE line
	named := cli.Do(t, "names", "--as", "ada", "--max", "1").Exit(0)
	assert.Equal(t, 1, strings.Count(named.Stdout, "NAMES NAME name="), "one name is printed")
	assert.Contains(t, named.Stdout, "NAMES MORE kind=name shown=1 total=2")

	// peek --max 1 prints one message and a MORE line
	peeked := cli.Do(t, "peek", "--as", "bob", "--max", "1").Exit(0)
	assert.Equal(t, 1, strings.Count(peeked.Stdout, "PEEK MESSAGE "), "one message is printed")
	assert.Contains(t, peeked.Stdout, "PEEK MORE kind=message shown=1 total=30")
}
