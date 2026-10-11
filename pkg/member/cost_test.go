package member

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receiptHeader is the header native writes on a job's usage.tsv (pkg/swarm
// CardUsageColumns).
const receiptHeader = "job\tattempt\tstarted\tended\tend\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

func writeReceipt(t *testing.T, rows ...string) string {
	t.Helper()
	job := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(strings.Join(append([]string{receiptHeader}, rows...), "\n")+"\n"), 0o644))
	return job
}

// TestARunWithNoResultStillRecordsItsCost pins that a launch that ended with no result,
// its summary line never printed (killed at its cap, its log cut), still records what it
// spent: its job's durable receipt is read back (ReceiptUsage), every attempt's tokens
// summed with the model it ran, the harness's cost kept only when every attempt reported
// one (else the sprint prices the tokens at the route's prices), and the member's failed
// finish carries it as --usage.
func TestARunWithNoResultStillRecordsItsCost(t *testing.T) {
	t.Parallel()
	job := writeReceipt(t,
		"c1\t1\t2026-10-04T10:00:00Z\t2026-10-04T10:00:01Z\tfailed\t1\topenrouter\tm\t100\t20\t-\t-\t-\t0.04",
		"c1\t2\t2026-10-04T10:00:01Z\t2026-10-04T10:09:00Z\twall\t-\topenrouter\tm\t300\t40\t-\t50\t-\t-",
	)
	u, found, err := ReceiptUsage(job)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, int64(400), u.Tokens.Input)
	assert.Equal(t, int64(60), u.Tokens.Output)
	assert.Equal(t, int64(50), u.Tokens.CacheRead)
	assert.Equal(t, "openrouter/m", u.Model)
	assert.Empty(t, u.Actual, "the second attempt reported no cost: a part is not the whole, the sprint prices the tokens")

	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err = g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Ran: true, End: EndNoResult, Report: "the child ended without a result", Usage: u.String()})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "finish --as m c1@2")
	assert.Contains(t, lines[0], "--failed")
	assert.Contains(t, lines[0], "--usage "+u.String())
	assert.Contains(t, lines[0], "input=400")
}

// ReceiptUsage's other answers: a priced receipt keeps the harness's cost (a measured zero
// included), no receipt or no row is not found, and a receipt that does not read is an
// error, never a zero.
func TestAReceiptReadsBackOrSaysWhyNot(t *testing.T) {
	t.Parallel()
	row := func(tokensIn, usd string) string {
		return "c1\t1\t2026-10-04T10:00:00Z\t2026-10-04T10:00:01Z\tdone\t0\topenrouter\tm\t" + tokensIn + "\t2\t-\t-\t-\t" + usd
	}
	for _, c := range []struct {
		name, actual, err string
		rows              []string
		found             bool
	}{
		{name: "priced", rows: []string{row("10", "0.10"), row("20", "0")}, found: true, actual: "0.1"},
		{name: "no row", found: false},
		{name: "usd not an amount", rows: []string{row("10", "not-money")}, err: `usd "not-money", not an amount`},
		{name: "tokens not a count", rows: []string{row("ten", "0.1")}, err: `tokens_in "ten", not a count`},
		{name: "short row", rows: []string{"c1\t1"}, err: "row has 2 fields; its header has 14"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			u, found, err := ReceiptUsage(writeReceipt(t, c.rows...))
			if c.err != "" {
				assert.ErrorContains(t, err, c.err)
				assert.False(t, found)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.found, found)
			assert.Equal(t, c.actual, u.Actual)
		})
	}
	_, found, err := ReceiptUsage(t.TempDir())
	assert.NoError(t, err)
	assert.False(t, found, "no receipt")
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
