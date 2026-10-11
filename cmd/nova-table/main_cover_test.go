package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMainCoverJSONRefusalsWrite: jsonRefusals is the stderr a --json verb
// runs with, so every non-refusal line a verb prints lands in said, in order,
// and a subsequent write appends rather than replaces. A refusal does not come
// through Write at all: refuseWith records it in out.Why, leaving said for the
// notes.
func TestMainCoverJSONRefusalsWrite(t *testing.T) {
	t.Parallel()
	j := &jsonRefusals{out: tool.Out{Verb: "batch", Status: tool.Refused}}
	n, err := j.Write([]byte("first note\n"))
	require.NoError(t, err)
	require.Equal(t, len("first note\n"), n)
	n, err = j.Write([]byte("second note\n"))
	require.NoError(t, err)
	require.Equal(t, len("second note\n"), n)
	assert.Equal(t, "first note\nsecond note\n", j.said.String())

	code := refuseWith(j, "batch", "a bound is wrong", 1)
	assert.Equal(t, 1, code)
	require.Len(t, j.out.Why, 1)
	assert.Contains(t, j.out.Why[0], "a bound is wrong")
	assert.Equal(t, "first note\nsecond note\n", j.said.String(), "a refusal is not a note")
}

// TestMainCoverExitOf: exitOf is 0 when what a verb printed reached its reader
// (nil), and 1 when it did not (the writer's error), the refusal the run loop
// returns after writing jsonRefusals' held notes.
func TestMainCoverExitOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "main path: the print reached its reader", err: nil, want: 0},
		{name: "refusal: the print did not reach its reader", err: errors.New("write /dev/full: no space left on device"), want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, exitOf(tc.err))
		})
	}

	// The run loop's own shape: a failed write of the held notes is exit 1.
	j := &jsonRefusals{}
	j.Write([]byte("a note\n"))
	var full bytes.Buffer
	_, err := full.Write(j.said.Bytes())
	require.NoError(t, err)
	assert.Equal(t, 0, exitOf(err))
	assert.Equal(t, 1, exitOf(errors.New("short write")))
}
