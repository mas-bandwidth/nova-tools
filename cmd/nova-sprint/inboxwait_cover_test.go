package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// exitErr is a source's failure already said on stderr, carrying its exit
// code; its Error is the one line "exit <code>" and nothing else, since the
// saying has been done by the source. The codes are the tool's exit table:
// 1 a verb that ran and said no, 2 a usage refusal or a server that did not
// answer. TestInboxwaitCoverExitErrError reaches it directly: the wait's
// paths that build one run through the server, which no unit test opens.
func TestInboxwaitCoverExitErrError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		err  *exitErr
		want string
	}{
		{name: "a verb that said no", err: &exitErr{code: 1}, want: "exit 1"},
		{name: "a refusal", err: &exitErr{code: 2}, want: "exit 2"},
		{name: "the server's own code", err: &exitErr{code: 3}, want: "exit 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.err.Error())
		})
	}
}
