//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoopThroughTheGrammarOnARealStore drives the loop kind's verbs against
// a throwaway Postgres and Redis: add, machine show naming it, apply --kind
// loop writing the hash the plays read, status at parity, remove.
func TestLoopThroughTheGrammarOnARealStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := newReal(t, true)
	r.run(t, 0, "migrate")
	r.run(t, 0, "machine", "add", "m1", "--user", "u", "--seat", "s-m1", "--slots", "4")
	out, _ := r.run(t, 0, "loop", "add", "member-m1", "--machine", "m1", "--argv", `["/bin/member","--width","2"]`, "--keepalive", "true", "--seat", "s-m1", "--keys", "B_KEY,A_KEY", "--width", "2")
	require.Equal(t, "CONFIG ADD kind=loop name=member-m1 rev=2\n", out)
	_, errs := r.run(t, 1, "loop", "set", "member-m1", "--every", "30")
	assert.Equal(t, "nova-config loop set: loop member-m1 has --every 30 and --keepalive true; a loop runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false; run: nova-config loop show member-m1\n", errs)
	out, _ = r.run(t, 0, "loop", "show", "member-m1")
	assert.True(t, strings.HasPrefix(out, `LOOP name=member-m1 machine=m1 argv=["/bin/member","--width","2"] seat=s-m1 keys=A_KEY,B_KEY every=0 keepalive=true width=2 enabled=true created=`), out)
	out, _ = r.run(t, 0, "machine", "show", "m1")
	assert.Contains(t, out, " loops=member-m1 ")

	out, _ = r.run(t, 0, "apply", "--kind", "machine")
	require.Contains(t, out, "CONFIG APPLY kind=machine add=1 ")
	out, _ = r.run(t, 0, "apply", "--kind", "loop")
	require.True(t, strings.HasPrefix(out, "APPLY ADD kind=loop name=member-m1\nCONFIG APPLY kind=loop add=1 set=0 remove=0 rev=2 ms="), out)
	h := r.client.HGetAll(ctx, config.LoopKey("member-m1")).Val()
	assert.Equal(t, "m1", h["machine"])
	assert.Equal(t, `["/bin/member","--width","2"]`, h["argv"])
	assert.Equal(t, "~/nova-bench/loops/member-m1.log", h["log"])
	assert.Equal(t, "2", h["rev"])
	out, errs = r.run(t, 0, "status")
	assert.Contains(t, out, " loop=1 loop_rev=2 ")
	assert.True(t, strings.HasSuffix(out, " loop_applied=2\n"), out)
	assert.Empty(t, errs)

	r.run(t, 0, "loop", "remove", "member-m1")
	_, errs = r.run(t, 1, "status")
	assert.Contains(t, errs, "Redis is not at Postgres's revision for 1 kind(s)")
	out, _ = r.run(t, 0, "apply", "--kind", "loop")
	assert.True(t, strings.HasPrefix(out, "APPLY REMOVE kind=loop name=member-m1\n"), out)
	assert.Zero(t, r.client.Exists(ctx, config.LoopKey("member-m1")).Val())
	r.run(t, 0, "status")
}
