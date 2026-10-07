//go:build perf

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// whereWallMax is the wall time one where --json may take at 3,000 cards on the
// in-memory store: the requirement is under 1 s on the live store
// (docs/SPEC-SPRINT.md section 1); the store's own exchanges add to this. A
// wall-clock bound lives behind -tags perf (docs/STANDARD.md, section 8), which
// gates a release, never a change; the change is gated on the round trips
// (TestWhereReadsTheTableNotEveryCardAtThreeThousandCards).
//
//	go test -tags perf -count=1 -p 1 -parallel 1 -run TestWhereAtThreeThousandCards ./cmd/nova-sprint/
const whereWallMax = 200 * time.Millisecond

// timedWhere is the wall time of one where --json alone: the test's beats are
// made before the clock starts.
func (ta *testApp) timedWhere() time.Duration {
	ta.t.Helper()
	ta.beat()
	var out, errb bytes.Buffer
	began := time.Now()
	code := ta.a.run([]string{"where", "--json"}, &out, &errb)
	took := time.Since(began)
	require.Zero(ta.t, code, "where --json: %s", errb.String())
	return took
}

// where --json at 3,000 cards (bigSprint) completes inside whereWallMax of real
// time once the tick has counted the where record.
func TestWhereAtThreeThousandCardsIsInsideTheWallBound(t *testing.T) {
	t.Parallel()
	ta := bigSprint(t)
	cards := ta.timedWhere()
	ta.ok("tick")
	took := ta.timedWhere()
	t.Logf("where --json at 3,000 cards: %s from the where record, %s reading the cards before it", took, cards)
	require.Less(t, took, whereWallMax, "where --json took %s, the bound is %s", took, whereWallMax)
}
