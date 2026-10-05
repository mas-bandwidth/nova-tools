package sprint

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fourth fsck check, seat-agreement (docs/SPEC-SPRINT.md, fsck-seat-agreement-r.w1):
// four values name the same coordinator:
// 1. the store key sprint:coordinator
// 2. the seat record's holder (internal/sprint/seat.go)
// 3. the actor the running server was started with (cmd/nova-sprint/serve.go records the actor it started with in its own beat or info key so fsck can read it)
// 4. the nova-config sprint row's coordinator (read through an injected reader; the command uses nova-config's library or injected function, the test a fake).
// Today they split twice: the server's launchd unit carried NOVA_SPRINT_ACTOR=stella from the 10-03 handover and every restart wrote sprint:coordinator=stella against a record of rowan, and nova-config apply moved the seat because the Postgres sprint row still named yesterday's coordinator.
// The fixes are named, never done: seat --repair for the key, and the nova-config sprint set line for the row.
// Fixture: today's values (key stella, record rowan approved by glenn).
// VERDICT: with sprint:coordinator=stella against a seat record of rowan, fsck reports seat-agreement naming all four values and seat --repair as the fix; four equal values are clean.

func TestFsckFindsTheSeatKeyAgainstTheRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Fixture: today's values (key stella, record rowan approved by glenn).
	key := "stella"
	rec := &SeatChange{
		Holder:     "rowan",
		ApprovedBy: "glenn",
		Taken:      true,
		By:         "rowan",
		From:       "stella",
		At:         time.Date(2026, 10, 3, 15, 39, 0, 0, time.UTC),
		Reason:     "10-03 handover",
	}
	server := "stella"
	fakeConfig := func(ctx context.Context) (string, error) {
		return "stella", nil
	}

	finding, err := FsckSeatAgreement(ctx, key, rec, server, fakeConfig)
	require.NoError(t, err)

	// VERDICT: with sprint:coordinator=stella against a seat record of rowan,
	// fsck reports seat-agreement naming all four values and seat --repair as the fix;
	// four equal values are clean.
	assert.False(t, finding.Clean, "drift must not be clean")
	assert.Equal(t, FsckCheckSeatAgreement, finding.Check)
	assert.Equal(t, "stella", finding.Key)
	assert.Equal(t, "rowan", finding.Record)
	assert.Equal(t, "stella", finding.Server)
	assert.Equal(t, "stella", finding.Config)
	assert.Contains(t, finding.Fix, "seat --repair", "fix must name seat --repair for the key")
	assert.Contains(t, finding.Fix, "nova-config sprint set --coordinator rowan", "fix must name nova-config sprint set for the row")

	line := finding.String()
	assert.Contains(t, line, "seat-agreement")
	assert.Contains(t, line, "key=stella")
	assert.Contains(t, line, "record=rowan")
	assert.Contains(t, line, "server=stella")
	assert.Contains(t, line, "config=stella")
	assert.Contains(t, line, "seat --repair")

	// Four equal values are clean.
	cleanRec := &SeatChange{
		Holder: "rowan",
	}
	cleanConfig := func(ctx context.Context) (string, error) {
		return "rowan", nil
	}
	cleanFinding, err := FsckSeatAgreement(ctx, "rowan", cleanRec, "rowan", cleanConfig)
	require.NoError(t, err)
	assert.True(t, cleanFinding.Clean, "four equal values are clean")
	assert.Equal(t, FsckCheckSeatAgreement, cleanFinding.Check)
	assert.Equal(t, "rowan", cleanFinding.Key)
	assert.Equal(t, "rowan", cleanFinding.Record)
	assert.Equal(t, "rowan", cleanFinding.Server)
	assert.Equal(t, "rowan", cleanFinding.Config)
	assert.Empty(t, cleanFinding.Fix)
	assert.Contains(t, cleanFinding.String(), "clean")
}
