package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resources' verbs on the twin (resource.go; docs/SPEC-SPRINT.md section 19):
// add, claim, renew, release, remove and list, their lines and their refusals.

func TestResourceVerbsClaimRenewReleaseAndList(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	code, _, errs := ta.do("resource claim bench-a --as m1 --for 1h")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no resource bench-a on the table; the coordinator adds one: resource add bench-a --kind bench|branch|port|account --capacity <n>")
	out := ta.ok("resource list")
	assert.Contains(t, out, "RESOURCES none: nova-sprint resource add <name> --kind bench|branch|port|account --capacity <n>")
	code, _, errs = ta.do("resource add bench-a --capacity 1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "--kind wants one of bench, branch, port, account")
	out = ta.ok("resource add bench-a --kind bench --capacity 1")
	assert.Contains(t, out, "RESOURCE-ADD OK bench-a kind=bench capacity=1")
	out = ta.ok("resource claim bench-a --as m1 --for 1h")
	assert.Contains(t, out, "RESOURCE-CLAIM OK bench-a as=m1 until=2030-01-02T04:04:05Z held=1/1: work, then nova-sprint resource release bench-a --as m1")
	code, _, errs = ta.do("resource claim bench-a --as m2 --for 30m")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "nova-sprint resource claim REFUSED: bench-a is held (1/1); m2 is 1st in the line and keeps the place without asking again")
	assert.Contains(t, errs, "run: nova-sprint resource claim bench-a --as m2 --for 2h --wait 30m")
	// a claim again keeps the place; --wait asks again until the wait is over
	code, _, errs = ta.do("resource claim bench-a --as m2 --for 30m --wait 12s")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "m2 is 1st in the line")
	out = ta.ok("resource list")
	assert.Contains(t, out, "RESOURCE bench-a kind=bench held=1/1 holders: m1 (until 2030-01-02T04:04:05Z, ")
	assert.Contains(t, out, "; waiting: 1. m2 (for 30m0s, since ")
	// renew is the holder's alone
	code, _, errs = ta.do("resource renew bench-a --as m2 --for 1h")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "m2 holds no lease of bench-a: it is 1st in the line; a renewal never grants")
	out = ta.ok("resource renew bench-a --as m1 --for 2h")
	assert.Contains(t, out, "RESOURCE-RENEW OK bench-a as=m1 until=")
	// the release grants the head
	out = ta.ok("resource release bench-a --as m1")
	assert.Contains(t, out, "RESOURCE-RELEASE OK bench-a as=m1 gave=yes held=1/1 granted=m2")
	out = ta.ok("resource list --json")
	var got struct {
		Resources []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			Capacity int    `json:"capacity"`
			Holders  []struct {
				Who string `json:"who"`
			} `json:"holders"`
			Waiters []any `json:"waiters"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	require.Len(t, got.Resources, 1)
	assert.Equal(t, "bench-a", got.Resources[0].Name)
	assert.Equal(t, "bench", got.Resources[0].Kind)
	require.Len(t, got.Resources[0].Holders, 1)
	assert.Equal(t, "m2", got.Resources[0].Holders[0].Who)
	assert.Empty(t, got.Resources[0].Waiters)
	// remove is refused while held; a release by a stranger gives nothing
	code, _, errs = ta.do("resource remove bench-a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "resource bench-a is held by m2 and waited for by nobody; release them first")
	out = ta.ok("resource release bench-a --as zed")
	assert.Contains(t, out, "gave=no")
	ta.ok("resource release bench-a --as m2")
	out = ta.ok("resource add bench-a --capacity 2")
	assert.Contains(t, out, "RESOURCE-ADD OK bench-a kind=- capacity=2", "a row that is there takes its capacity")
	out = ta.ok("resource remove bench-a")
	assert.Contains(t, out, "RESOURCE-REMOVE OK bench-a")
}

func TestResourceVerbsDryRunWriteNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("resource add bench-a --kind bench --capacity 1")
	ta.ok("resource claim bench-a --as m1 --for 1h")
	before := ta.applies()
	out := ta.ok("resource claim bench-a --as m2 --for 1h --dry-run")
	assert.Contains(t, out, "RESOURCE-CLAIM DRY-RUN bench-a as=m2 would_grant=no place=1 held=1/1; nothing was written")
	out = ta.ok("resource claim bench-a --as m1 --for 1h --dry-run")
	assert.Contains(t, out, "would_grant=yes")
	out = ta.ok("resource renew bench-a --as m2 --for 1h --dry-run")
	assert.Contains(t, out, "RESOURCE-RENEW DRY-RUN bench-a as=m2 would_apply=no")
	out = ta.ok("resource release bench-a --as m1 --dry-run")
	assert.Contains(t, out, "RESOURCE-RELEASE DRY-RUN bench-a as=m1 would_apply=yes")
	out = ta.ok("resource remove bench-a --dry-run")
	assert.Contains(t, out, "RESOURCE-REMOVE DRY-RUN bench-a would_remove=no held=1 waiting=0")
	out = ta.ok("resource add other --kind port --capacity 3 --dry-run")
	assert.Contains(t, out, "RESOURCE-ADD DRY-RUN other kind=port capacity=3 exists=no; nothing was written")
	assert.Equal(t, before, ta.applies(), "a dry run wrote")
	out = ta.ok("resource list")
	assert.NotContains(t, out, "other")
	assert.Contains(t, out, "holders: m1 (")
}

func TestResourceVerbsRefuseBadUse(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	for _, line := range []string{
		"resource add",
		"resource add a b --kind bench",
		"resource add 'bad name' --kind bench",
		"resource add x --kind cloud",
		"resource add x --kind bench --capacity 0",
		"resource claim x",
		"resource claim x --as m1",
		"resource claim x --as m1 --for 0",
		"resource claim x --as m1 --for 25h",
		"resource claim x --as m1 --for 1h --wait -1s",
		"resource renew x --as m1",
		"resource release x",
		"resource remove",
		"resource list x",
	} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, "%q: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "run: nova-sprint", "%q: %q", line, errs)
	}
	out := ta.ok("help resource")
	assert.Contains(t, out, "nova-sprint resource claim <name> --as <member> --for <duration> [--wait <duration>] [--dry-run]")
	assert.Contains(t, out, "no member holds one by agreement with another member")
	out = ta.ok("help")
	assert.Contains(t, out, "The resources: a shared resource")
	assert.True(t, strings.Contains(out, "resource add"), "the banner names the verbs")
}
