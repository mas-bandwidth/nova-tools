package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// fsck's seat-agreement through the command (docs/SPEC-SPRINT.md, "Handing
// over the seat", fsck-seat-agreement-r.w2), the config row a fake: the key, the
// record and the server's actor are read from the store, the line names all
// four values and the fixes, the exit is 1 on a drift, 0 on agreement and 2
// when the config row could not be read, and nothing is written.
func TestFsckNamesTheFourSeatValues(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	ta.ok("coordinator rowan --take --approved-by glenn --reason 'the holder is away' --actor rowan")
	st, err := ta.a.store(common{redis: "mem:0", actor: "stella"})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, st.SetServerActor(ctx, "stella"))
	require.NoError(t, ta.m.SetCoordinator(ctx, "stella"))

	var asked []string
	row := func(name string, err error) func(string) sprint.ConfigCoordinator {
		return func(pg string) sprint.ConfigCoordinator {
			asked = append(asked, pg)
			return func(context.Context) (string, error) { return name, err }
		}
	}
	fsck := func(row func(string) sprint.ConfigCoordinator, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := ta.a.fsckSeat(args, &out, &errb, row)
		return code, out.String(), errb.String()
	}

	code, out, errs := fsck(row("stella", nil), "--pg", "db:5432")
	assert.Equal(t, 1, code, errs)
	assert.Regexp(t, `^FSCK DRIFT check=seat-agreement key=stella record=rowan server=stella config=stella .*nova-sprint seat --repair`, out)
	assert.Contains(t, out, "nova-config sprint set --coordinator rowan", out)
	assert.Equal(t, []string{"db:5432"}, asked, "the row is read at --pg")
	key, err := ta.m.Coordinator(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stella", key, "fsck writes nothing")

	code, out, _ = fsck(row("stella", nil), "--json")
	assert.Equal(t, 1, code)
	var v struct {
		Status string                 `json:"status"`
		Exit   int                    `json:"exit"`
		Checks []sprint.SeatAgreement `json:"checks"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	assert.Equal(t, "drift", v.Status)
	assert.Equal(t, 1, v.Exit)
	require.Len(t, v.Checks, 1)
	assert.Equal(t, "rowan", v.Checks[0].Record)

	code, out, errs = fsck(row("", errors.New("no route")))
	assert.Equal(t, 2, code, out)
	assert.Contains(t, errs, "fsck seat FAILED: the nova-config sprint row was not read: no route", errs)

	ta.ok("seat --repair --reason 'the restart wrote its actor' --actor rowan")
	require.NoError(t, st.SetServerActor(ctx, "rowan"))
	code, out, errs = fsck(row("rowan", nil))
	assert.Equal(t, 0, code, errs)
	assert.Equal(t, "FSCK OK check=seat-agreement key=rowan record=rowan server=rowan config=rowan\n", out)

	code, _, errs = fsck(row("rowan", nil), "extra")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "fsck seat REFUSED: takes no words", errs)
}
