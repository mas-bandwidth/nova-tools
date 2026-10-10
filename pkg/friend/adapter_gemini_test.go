package friend

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiResumesTheSessionWithThePromptAsOneArgument(t *testing.T) {
	t.Parallel()
	var turn strings.Builder
	fe := &fakeExec{exit: 0, out: "pong\n"}
	d, err := NewDeliverer("gemini", "/w/bob", "", fe.run, &turn)
	require.NoError(t, err)
	exit, err := d.Deliver(context.Background(), "--hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{"/w/bob", "gemini", "--skip-trust", "--resume", "latest", "--prompt=--hello"}, fe.calls[0],
		"no session named: the CLI's own latest of the directory; the text is one --prompt= argument, never a flag")
	assert.Equal(t, "pong\n", turn.String(), "the turn's output goes to the daemon's record")

	fe = &fakeExec{exit: 42}
	d, err = NewDeliverer("gemini", "/w/bob", "26f6a688-cbbf-4a00-8d36-5fb885bd76d4", fe.run, nil)
	require.NoError(t, err)
	exit, err = d.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, 42, exit, "the exit code is the harness's: 42 is no such session")
	assert.Equal(t, []string{"/w/bob", "gemini", "--skip-trust", "--resume", "26f6a688-cbbf-4a00-8d36-5fb885bd76d4", "--prompt=x"}, fe.calls[0])
}
