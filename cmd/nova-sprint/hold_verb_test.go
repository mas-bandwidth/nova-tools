package main

import (
	"encoding/json"
	"testing"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hold and unhold on the twin (docs/SPEC-SPRINT.md section 11): one verb for members,
// readers and streams here (friends need a config: internal/sprint's twin test holds
// them), the reason required of a hold and shown with the holds, the log's lines; the
// old words' help names the pair.
func TestTheHoldVerbsHoldAndReleaseWithTheReason(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 2")

	code, _, errs := ta.do("hold m1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--reason <text> is required")
	code, _, errs = ta.do("hold m1 nobody --reason r")
	assert.Equal(t, 1, code, errs)
	assert.NotContains(t, ta.ok("log"), "member m1 held", "a name of nothing refuses the whole call: nothing written")
	code, _, _ = ta.do("unhold")
	assert.Equal(t, 2, code)

	out := ta.ok("hold m1 reader-a s1 --reason 'the cache trim'")
	assert.Contains(t, out, "HOLD OK")
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --cards")), &v))
	var held []string
	for _, h := range v.Holds {
		held = append(held, h.Kind+" "+h.Name+": "+h.Reason)
	}
	assert.Equal(t, []string{"member m1: the cache trim", "reader reader-a: the cache trim", "stream s1: the cache trim"}, held)
	assert.Equal(t, sprint.Held, v.Tables["fleet"]["m1"]["status"])
	assert.Equal(t, sprint.Held, v.Tables["merge"]["s1"]["state"])
	assert.Equal(t, "held", ta.readerState("reader-a"))
	log := ta.ok("log")
	for _, want := range []string{"member m1 held: the cache trim", "reader reader-a held: the cache trim", "stream s1 held: the cache trim"} {
		assert.Contains(t, log, want)
	}

	assert.Contains(t, ta.ok("unhold m1 reader-a s1 --reason trimmed"), "UNHOLD OK")
	var after whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --cards")), &after))
	assert.Empty(t, after.Holds)
	assert.Contains(t, ta.ok("log"), "stream s1 released from its hold: trimmed")

	for verb, want := range map[string]string{"fleet down": "hold <member> --return", "reader away": "hold <reader>... --return", "reader up": "unhold", "friend down": "hold <friend>", "friend up": "unhold <friend>"} {
		assert.Contains(t, ta.ok("help "+verb), want, verb)
	}
}
