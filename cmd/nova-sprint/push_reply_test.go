package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushProofWithoutAReplyRouteDeliversNothing(t *testing.T) {
	t.Parallel()
	ta, session := pushProofSprint(t, "push-no-reply-route")
	ta.ok("init")
	ta.do("seat push --harness opencode --target /tmp/canary")
	st, err := ta.a.store(common{redis: "mem:0", actor: "push-no-reply-route"})
	require.NoError(t, err)
	var said bytes.Buffer
	ta.a.prove(context.Background(), &storeSource{st: st}, "push-no-reply-route", false, &said)
	assert.Contains(t, said.String(), "no direct store address")
	assert.Empty(t, session.last())
	rec, ok, err := readPush(context.Background(), st, "push-no-reply-route")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Empty(t, rec.Nonce)
}

func TestPushProofReplyRejectsCredentialBearingAddresses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  pushProver
	}{
		{"direct", &storeSource{redis: "redis://user:credential-value@host:6379"}},
		{"server", &serverSource{addr: "user:credential-value@host:6390"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reply, err := tc.src.pushReply("emma", "n1")
			require.Error(t, err)
			assert.Empty(t, reply)
			assert.NotContains(t, err.Error(), "credential-value")
		})
	}
}
