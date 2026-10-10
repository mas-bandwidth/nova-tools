package main

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerReviewWaitingInboxCannotWriteTheClientStore(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	before, err := ta.m.Cursor(context.Background())
	require.NoError(t, err)
	prior := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == ServerEnv {
			return "127.0.0.1:6390"
		}
		return prior(k)
	}
	ta.a.forward = func(context.Context, string, ...[]string) ([]sprintwire.Result, error) {
		return []sprintwire.Result{{Code: 2, Stderr: "server refuses the cursor write"}}, nil
	}
	code, out, errs := ta.do("inbox --wait --read --timeout 2s")
	after, err := ta.m.Cursor(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before, after, "only the server may move the cursor; exit=%d output=%s%s", code, out, errs)
}
