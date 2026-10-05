package sprint_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The fsck check seat-agreement (docs/SPEC-SPRINT.md, "Handing over the seat",
// fsck-seat-agreement-r.w1), on the twin store with a fake config row. The
// fixture is the split of 2026-10-05: the seat was taken by rowan, approved by
// glenn; the server's unit still ran as stella and its restart left the key
// saying stella, and the config row still named stella. fsck names all four
// values and each fix (seat --repair for the key, the server's actor line, the
// nova-config sprint set line for the row) and changes nothing; four equal
// values are clean.
func TestFsckFindsTheSeatKeyAgainstTheRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := store.NewMem()
	n := 0
	st := &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "stella",
		Now:   func() time.Time { return time.Date(2026, 10, 5, 15, 39, 0, 0, time.UTC) },
		NewID: func() string { n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.SetCoordinator(ctx, "stella"))
	res, err := st.Run(ctx, store.SeatStep(sprint.SeatReq{To: "rowan", Who: "rowan", Reason: "the holder is away",
		Take: true, ApprovedBy: "glenn", Owner: "glenn"}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	require.NoError(t, st.SetServerActor(ctx, "stella"))
	require.NoError(t, m.SetCoordinator(ctx, "stella")) // the restart's write of the old build

	fsck := func(config sprint.ConfigCoordinator) (sprint.SeatAgreement, error) {
		s, err := st.SeatCheck(ctx)
		require.NoError(t, err)
		return sprint.FsckSeat(ctx, s.Holder, s.Record, s.Server, config)
	}
	row := func(name string) sprint.ConfigCoordinator {
		return func(context.Context) (string, error) { return name, nil }
	}

	f, err := fsck(row("stella"))
	require.NoError(t, err)
	assert.Equal(t, sprint.SeatAgreement{Check: sprint.FsckSeatAgreement, Key: "stella", Record: "rowan", Server: "stella", Config: "stella", Holder: "rowan", Drift: f.Drift}, f)
	assert.Contains(t, f.Drift, "the key says stella and the record rowan: nova-sprint seat --repair", f.Drift)
	assert.Contains(t, f.Drift, "NOVA_SPRINT_ACTOR=stella to NOVA_SPRINT_ACTOR=rowan", f.Drift)
	assert.Contains(t, f.Drift, "the config row says stella and the seat is rowan's: nova-config sprint set --coordinator rowan", f.Drift)
	assert.Regexp(t, `^FSCK DRIFT check=seat-agreement key=stella record=rowan server=stella config=stella .*seat --repair`, f.Line())
	key, err := m.Coordinator(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stella", key, "fsck names the fix and writes nothing")

	_, err = fsck(func(context.Context) (string, error) { return "", errors.New("no route to the config store") })
	assert.ErrorContains(t, err, "the nova-config sprint row was not read: no route to the config store", "a row not read is no finding")

	// the fixes, run: the key repaired, the server restarted as the holder, the row set
	step, why, err := st.SeatRepairStep(ctx, sprint.SeatRepairReq{Who: "rowan", Reason: "the restart wrote its actor"})
	require.NoError(t, err)
	require.Empty(t, why)
	_, err = st.Run(ctx, step)
	require.NoError(t, err)
	require.NoError(t, st.SetServerActor(ctx, "rowan"))
	f, err = fsck(row("rowan"))
	require.NoError(t, err)
	assert.Empty(t, f.Drift, "four equal values are clean")
	assert.Equal(t, "FSCK OK check=seat-agreement key=rowan record=rowan server=rowan config=rowan", f.Line())

	for _, tc := range []struct {
		name, key, record, server, config, drift string
	}{
		{"the row alone", "rowan", "rowan", "rowan", "stella", "nova-config sprint set --coordinator rowan"},
		{"the server alone", "rowan", "rowan", "stella", "rowan", "NOVA_SPRINT_ACTOR=stella to NOVA_SPRINT_ACTOR=rowan"},
		{"the key alone", "stella", "rowan", "rowan", "rowan", "seat --repair"},
		{"no record: the key is the seat", "rowan", "", "rowan", "stella", "the seat is rowan's"},
		{"the machine's server, no server, an empty row", "rowan", "rowan", sprint.MachineActor, "", ""},
		{"nothing recorded but the key", "rowan", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := sprint.CheckSeatAgreement(tc.key, tc.record, tc.server, tc.config)
			if tc.drift == "" {
				assert.Empty(t, f.Drift)
				return
			}
			assert.Contains(t, f.Drift, tc.drift)
			assert.NotContains(t, f.Drift, ";", "one value split, one fix: %q", f.Drift)
		})
	}
}
