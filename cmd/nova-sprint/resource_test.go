package main

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resource verbs on the twin (docs/SPEC-SPRINT.md section 11, Resources): add is
// the coordinator's, claim grants or queues, release grants the line's head, list shows
// holders, expiries and the line; refusals name the next command.
func TestTheResourceVerbsClaimQueueAndRelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")

	assert.Contains(t, ta.ok("resource list"), "no resources")
	code, _, errs := ta.do("resource add bench-1 --kind oven")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "is not a kind")
	assert.Contains(t, ta.ok("resource add bench-1 --kind bench --capacity 1"), "RESOURCE bench-1 kind=bench capacity=1")

	assert.Contains(t, ta.ok("resource claim bench-1 --as m1 --for 2h"), "GRANTED bench-1 to=m1")
	assert.Contains(t, ta.ok("resource claim bench-1 --as m2 --for 1h"), "WAITING bench-1 as=m2 place=1")
	code, _, errs = ta.do("resource claim bench-1 --as m2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "--for")
	code, _, errs = ta.do("resource claim nothing --as m2 --for 1h")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "resource list")

	var v struct {
		Resources []store.ResourceRow `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("resource list --json")), &v))
	require.Len(t, v.Resources, 1)
	assert.Equal(t, "m1", v.Resources[0].Holders[0].Member)
	assert.Equal(t, "m2", v.Resources[0].Line[0].Member)
	assert.Contains(t, ta.ok("resource list"), "held=1 waiting=1")

	assert.Contains(t, ta.ok("resource renew bench-1 --as m1 --for 3h"), "GRANTED bench-1 to=m1")
	assert.Contains(t, ta.ok("resource release bench-1 --as m1"), "RELEASED bench-1 by=m1 granted=m2")
	v.Resources = nil
	require.NoError(t, json.Unmarshal([]byte(ta.ok("resource list --json")), &v))
	assert.Equal(t, "m2", v.Resources[0].Holders[0].Member)
	assert.Empty(t, v.Resources[0].Line)

	code, _, errs = ta.do("resource release bench-1 --as m1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "neither holds")
	assert.Contains(t, ta.ok("help resource"), "never polls")

	// the workers' verbs are the member's own: the actor is --as
	code, _, errs = ta.do("resource add bench-2 --kind port --actor intruder")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the coordinator's alone")
}
