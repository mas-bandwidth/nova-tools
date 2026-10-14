package friend

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rankedRead is the server's LANE line as nova-sprint prints it (cmd/nova-sprint/lanetake.go,
// laneLine; docs/SPEC-FRIEND.md, the LANE line): rank=<d> between holds= and kind=, and a read
// card held by a reader-only friend's lane said kind=read.
func rankedRead(take func(context.Context, []string) (string, error)) func(context.Context, []string) (string, error) {
	return func(ctx context.Context, argv []string) (string, error) {
		out, err := take(ctx, argv)
		return strings.Replace(out, " kind=work job=", " rank=1 kind=read job=", 1), err
	}
}

// The fault of 2026-10-14 03:51Z, red before the fix: a reader-only friend (roles=reader) was
// dealt read cards <card>.r<n>.<friend>, their briefs reached her inbox, and every lane said
// "refused: the server's LANE line names no card at a generation and its job" and ran nothing.
// The server's line carries rank=<d> before kind= and job=, and the parser read kind and job
// only when they followed holds= directly. A lane runs the read card its LANE line names.
func TestAReaderLaneRunsAReadCardFromTheLaneLine(t *testing.T) {
	t.Parallel()
	line := "TAKE OK moved=1\nNOTE LANE friend.f lane=1 epoch=15 holds=x.r1.f@1 rank=1 kind=read job=x.r1.f~15\n"
	h, err := ParseLaneHold("friend.f", 1, line)
	require.NoError(t, err)
	assert.Equal(t, LaneHold{Lane: 1, Epoch: 15, Card: "x.r1.f", Gen: 1, Kind: "read", Job: "x.r1.f~15"}, h)

	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"x.r1.f", "queued"}}, []string{"x.r1.f"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{"x.r1.f": true}, active: map[string]int{}}
		r, _ := laneRig(t, lh, 1)
		srv := newLaneServer(true, map[string]string{"x.r1.f~15": "ready"})
		srv.wire(r)
		r.d.Take = rankedRead(srv.take)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		turns, _, _ := lh.got()
		assert.Equal(t, []string{"x.r1.f"}, ranCards(turns), "the lane runs the read card the server says it holds: %s", records)
		assert.Contains(t, records, "took card x.r1.f@1 (kind read, epoch 15)", records)
		assert.NotContains(t, records, "names no card at a generation and its job", records)
		_, _, cols := srv.got()
		assert.Equal(t, "done", cols["x.r1.f~15"], records)
	})
}

// A LANE line the lane cannot read is the server's fault, and asking again every TakeRetry
// gets the same line forever: the lane says one refusal naming the field the line is
// missing, and asks no more until the daemon restarts.
func TestAnUnparseableLaneLineIsOneRefusalNotALoop(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ line, field string }{
		{"NOTE LANE friend.bob lane=1 epoch=15 holds=x.r1.f@1 rank=1 kind=read\n", "job"},
		{"NOTE LANE friend.bob lane=1 epoch=15 holds=x.r1.f rank=1 kind=read job=x.r1.f~15\n", "gen"},
		{"NOTE LANE friend.bob lane=1 epoch=15 holds=x.r1.f@1 rank=1 job=x.r1.f~15\n", "kind"},
		{"NOTE LANE friend.bob lane=1 epoch=15 holds=x.r1.f@1 rank=1 kind=read job=../etc\n", "job"},
	} {
		_, err := ParseLaneHold("friend.bob", 1, c.line)
		var bad *Unparseable
		require.ErrorAs(t, err, &bad, c.line)
		assert.Equal(t, c.field, bad.Field, c.line)
		var refused *Refused
		assert.ErrorAs(t, err, &refused, "an unparseable line is a refusal too: %s", c.line)
	}

	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"x.r1.f", "queued"}}, []string{"x.r1.f"}, nil)
		lh := &lanesHarness{dir: dir, finish: map[string]bool{"x.r1.f": true}, active: map[string]int{}}
		r, _ := laneRig(t, lh, 1)
		srv := newLaneServer(true, map[string]string{"x.r1.f~15": "ready"})
		srv.wire(r)
		r.d.Take = func(ctx context.Context, argv []string) (string, error) {
			out, err := srv.take(ctx, argv)
			return strings.Replace(out, " job=x.r1.f~15", "", 1), err
		}
		r.run(t, 120)

		records := strings.Join(r.records, "\n")
		turns, _, _ := lh.got()
		assert.Empty(t, turns, records)
		assert.Equal(t, 1, strings.Count(records, "asked the server what it holds: refused"), "one refusal line: %s", records)
		assert.Contains(t, records, "names no job=", "the refusal names the missing field: %s", records)
		assert.Contains(t, records, "asks no more until the daemon restarts", records)
		takes, _, _ := srv.got()
		assert.Len(t, takes, 1, "the lane stops asking: %v", takes)
	})
}
