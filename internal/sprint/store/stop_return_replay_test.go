package store

import (
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopReturnLostReplyReplaysAfterStartAndNewGenerationBegins(t *testing.T) {
	t.Parallel()
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "work", true: "read"}[reader], func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			var card *sprint.Card
			if reader {
				h.asked1(1)
				card = h.snap().Readers.Of("s1-1")[0]
			} else {
				h.setup(1)
				h.startMachine()
				h.machine()
				card = h.snap().Fleet.Card(h.snap().Work.Card("s1-1").F("work"))
			}
			require.NotNil(t, card)
			old := max(card.Int("gen"), 1)
			begin := func(gen int) {
				if reader {
					h.must(ReadStep(sprint.ReadReq{As: card.Row, Begin: true, Sel: sprint.Sel{IDs: []string{card.ID}}, Gens: map[string]int{card.ID: gen}}))
				} else {
					h.must(TakeStep(sprint.TakeReq{As: card.Row, Sel: sprint.Sel{IDs: []string{card.ID}}, Gens: map[string]int{card.ID: gen}}))
				}
			}
			begin(old)
			epoch := h.snap().Epoch
			r := sprint.StopReturnReq{As: card.Row, IDs: []string{card.ID}, Gens: map[string]int{card.ID: old}, Reason: "owned process stopped"}
			step := StopReturnStep(r)
			step.Epoch = &epoch
			step.CallerOp = sprint.StopReturnOp(card.Row, card.ID, old, strconv.FormatUint(epoch, 10))
			_, _, _, err := h.st.StopUntil(h.ctx, "cancel the owner child", h.now.Add(time.Hour))
			require.NoError(t, err)
			accepted := h.must(step) // commit succeeded; the worker can lose this response
			require.NotEmpty(t, accepted.Op)
			h.startMachine() // START proves the committed same-owner receipt
			begin(old + 1)
			before := h.snap()
			replayed := h.must(step)
			assert.True(t, replayed.Replay)
			assert.Equal(t, accepted.Op, replayed.Op)

			legacy := StopReturnStep(r) // an older daemon has no stable --op yet
			legacy.Epoch = &epoch
			compat := h.must(legacy)
			assert.Empty(t, compat.Moved)
			assert.Empty(t, compat.Op, "compatibility replay does not write an operation")
			assert.NotEmpty(t, compat.Said)
			after := h.snap()
			assert.Equal(t, before.Fleet.Cards(), after.Fleet.Cards())
			assert.Equal(t, before.Readers.Cards(), after.Readers.Cards())

			for _, bad := range []sprint.StopReturnReq{
				{As: "another-owner", IDs: r.IDs, Gens: r.Gens, Reason: r.Reason},
				{As: r.As, IDs: r.IDs, Gens: map[string]int{card.ID: old + 1}, Reason: r.Reason},
				{As: r.As, IDs: r.IDs, Gens: r.Gens, Reason: "changed acknowledgement"},
			} {
				assert.NotEmpty(t, h.run(StopReturnStep(bad)).Refused, "RUNNING accepts only the exact durable return receipt")
			}
			wrongEpoch := epoch + 1
			legacy.Epoch = &wrongEpoch
			assert.NotEmpty(t, h.run(legacy).Refused)
			changed := r
			changed.Reason = "changed acknowledgement"
			changedStep := StopReturnStep(changed)
			changedStep.Epoch, changedStep.CallerOp = &epoch, step.CallerOp
			_, err = h.st.Run(h.ctx, changedStep)
			assert.ErrorContains(t, err, "arguments", "same identity cannot change the immutable request")

			// A subsequent STOP overwrites the current card marker. The original
			// immutable operation still settles that old client's owed reply.
			_, _, _, err = h.st.StopUntil(h.ctx, "cancel the next generation", h.now.Add(time.Hour))
			require.NoError(t, err)
			next := sprint.StopReturnReq{As: r.As, IDs: r.IDs, Gens: map[string]int{card.ID: old + 1}, Reason: r.Reason}
			h.must(StopReturnStep(next))
			h.startMachine()
			begin(old + 2)
			last := h.snap()
			assert.True(t, h.must(step).Replay)
			assert.NotEmpty(t, h.run(StopReturnStep(r)).Refused, "absent historical proof cannot mutate the current claim")
			assert.Equal(t, last.Fleet.Cards(), h.snap().Fleet.Cards())
			assert.Equal(t, last.Readers.Cards(), h.snap().Readers.Cards())
		})
	}
}
