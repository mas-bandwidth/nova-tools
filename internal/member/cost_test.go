package member

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARunWithNoResultStillRecordsItsCost pins that a work card whose child
// ended with no result still reports its measured or recovered cost (--usage)
// with the failed finish, so provider tokens and cost are never silently dropped.
func TestARunWithNoResultStillRecordsItsCost(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)

	usage := "input=120 output=30 actual_usd=0.045 actual_by=harness"
	g.r.child("c1").end(Result{
		Ran:    true,
		OK:     false,
		Shaped: false,
		End:    EndNoResult,
		Report: "the child ended without a result",
		Usage:  usage,
	})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)

	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "finish --as m c1@2")
	assert.Contains(t, lines[0], "--failed")
	assert.Contains(t, lines[0], "--usage "+usage)
}

// TestAReadStillRecordsItsCost pins that a read card report (--ok or --return)
// carries --usage when the reader reported usage.
func TestAReadStillRecordsItsCost(t *testing.T) {
	t.Parallel()
	// Test read --ok
	t.Run("read ok records usage", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 2, Reader: true})
		r1 := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &r1)))
		_, err := g.tick(t)
		require.NoError(t, err)

		usage := "input=50 output=10 actual_usd=0.01 actual_by=harness"
		g.r.child("r1").end(Result{
			Ran:     true,
			Verdict: "ok",
			Report:  "clean",
			Usage:   usage,
		})
		g.s.reset()
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &r1)))
		_, err = g.tick(t)
		require.NoError(t, err)

		lines := g.s.lines("report")
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "read --as r --ok r1")
		assert.Contains(t, lines[0], "--usage "+usage)
	})

	// Test read --return
	t.Run("read return records usage", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 2, Reader: true})
		r2 := Packet{Card: "r2", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
		g.s.set("queue", 0, queueJSON(t, 7, reading("r2", &r2)))
		_, err := g.tick(t)
		require.NoError(t, err)

		usage := "input=60 output=15 actual_usd=0.015 actual_by=harness"
		g.r.child("r2").end(Result{
			Ran:     false,
			Verdict: "",
			Report:  "no verdict",
			Usage:   usage,
		})
		g.s.reset()
		g.s.set("queue", 0, queueJSON(t, 7, reading("r2", &r2)))
		_, err = g.tick(t)
		require.NoError(t, err)

		lines := g.s.lines("return")
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "read --as r --return r2")
		assert.Contains(t, lines[0], "--usage "+usage)
	})
}
