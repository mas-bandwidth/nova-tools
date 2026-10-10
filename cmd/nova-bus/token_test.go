package main

import (
	"io"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A send whose answer was lost after the store took it, retried with the
// same --token and the same arguments, prints the first send's line (its id
// and at) and adds nothing; the same token with another body is refused.
func TestARetriedSendWithTheSameTokenPrintsTheFirstLine(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	args := []string{"send", "--as", "ada", "--to", "bob", "--subject", "once", "--body", "the body\n", "--token", "t-cli"}

	r.store.Lose = io.ErrUnexpectedEOF
	cli.Do(t, args...).Exit(2).Err("SEND REFUSED", "unexpected EOF")
	require.Equal(t, 1, r.store.Len(bus.LogKey), "the write committed; its answer was lost")
	log, err := (&bus.Bus{Store: r.store}).Log(t.Context(), "-")
	require.NoError(t, err)
	first := log[0].Message()

	r.advance(30 * time.Second)
	got := cli.OK(t, args...)
	assert.Equal(t, first.ID, id(t, got.Stdout))
	assert.Contains(t, got.Stdout, "at="+first.At.Format("2006-01-02T15:04:05Z07:00"), "the first send's at, not the retry's")
	again := cli.OK(t, args...)
	assert.Equal(t, got.Stdout, again.Stdout, "every retry prints the same line")
	assert.Equal(t, 1, r.store.Len(bus.StreamOf("bob")))
	assert.Equal(t, 1, r.store.Len(bus.LogKey))

	other := append(args[:len(args)-4:len(args)-4], "--body", "another\n", "--token", "t-cli")
	cli.Do(t, other...).Exit(2).Err("SEND REFUSED", `the token "t-cli" already sent `+first.ID)
	assert.Equal(t, 1, r.store.Len(bus.LogKey))

	cli.Do(t, append(args, "--token-life", "0s")...).Exit(2).Err("--token-life and --token-cleanup want a duration above zero")
}
