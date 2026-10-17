package hook_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/seatstamp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookStampTyped pins the hook stamp's contract end to end through the
// hook's own judgment: the time is int64 milliseconds in every form, a
// malformed stamp is refused by name and rides the push status (never read as
// no stamp in silence), the RFC 3339 predecessor is refused as legacy_shape
// (the one rule pkg/hookwire pins too), and each read is counted under the
// form it arrived in. The reader's strict cases are TestSeatHookTypedStamp.
// This card's change over the stream base is read-side tests only
// (seat_stamp_test.go and stamp_typed_test.go); the typed reader itself ships
// with the seat hook reader, and the writer-side cutover is a later card's.
// The kept rule is the pkg/hookwire one: a legacy RFC 3339 stamp refuses as
// legacy_shape rather than being read as a time.
func TestHookStampTyped(t *testing.T) {
	t.Parallel()
	const epoch, atMS = 5, int64(1792000000000)

	t.Run("every form reads one int64 millisecond stamp and is counted", func(t *testing.T) {
		t.Parallel()
		r := seatstamp.New()
		for form, in := range forms(epoch, atMS, "holder") {
			got, err := r.ReadStamp(in)
			require.NoError(t, err, form.String())
			assert.Equal(t, hook.Stamp{Epoch: epoch, AtMS: atMS, By: "holder"}, got, form.String())
		}
		for _, form := range allForms {
			assert.Equal(t, uint64(1), r.Counts().Reads(form), form.String())
		}
	})

	t.Run("a malformed stamp is refused by name on the push status", func(t *testing.T) {
		t.Parallel()
		r := seatstamp.New()
		rfc := `{"epoch":5,"at":"2026-10-11T12:00:00Z","by":"x"}`
		st := hook.JudgeWith(r, "ada", coordinatorRecord(epoch), coordinatorReads(rfc, rfc))
		for _, p := range st.Pushes {
			assert.Equal(t, hook.PushDown, p.State)
			assert.Contains(t, p.Refused, "STAMP REFUSED: legacy_shape")
		}
		assert.Equal(t, uint64(len(st.Pushes)), r.Counts().Refused(seatstamp.ReasonLegacyShape))
	})
}
