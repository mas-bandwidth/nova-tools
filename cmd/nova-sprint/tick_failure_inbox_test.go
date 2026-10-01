package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failFleetRead makes the tick's read of the fleet table fail, the real case
// of 2026-10-01: a fleet table that lacks a column.
func (ta *testApp) failFleetRead() {
	ta.m.Fail = func(p string) error {
		if strings.HasPrefix(p, "readset ") && strings.HasSuffix(p, "fleet") {
			return errors.New("fleet: no such column")
		}
		return nil
	}
}

// A failed tick of nova-sprint tick reaches the inbox (docs/SPEC-SPRINT.md
// section 14): the verb fails as before, and inbox prints one note naming the
// failure, still one after three failed ticks, with the recovery after it.
func TestAFailedTickReachesTheInbox(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.failFleetRead()
	for i := 0; i < 3; i++ {
		code, _, errs := ta.do("tick")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "no such column")
	}
	out := ta.ok("inbox")
	assert.Equal(t, 1, strings.Count(out, "the tick failed"), out)
	assert.Contains(t, out, "no such column")
	assert.Contains(t, out, "for=")
	ta.m.Fail = nil
	ta.ok("tick")
	out = ta.ok("inbox")
	assert.Contains(t, out, "the tick recovered")
	assert.Contains(t, out, "failed=3")
}

// The run loop's failed ticks reach the inbox the same way: five failed ticks
// of the loop leave one note, and the loop's own error line is still printed.
func TestRunLoopFailedTicksReachTheInboxOnce(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.failFleetRead()
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)
	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 20, 5, &out, &errb)
	assert.Contains(t, errb.String(), "no such column")
	assert.Equal(t, 1, strings.Count(ta.ok("inbox"), "the tick failed"))
}
