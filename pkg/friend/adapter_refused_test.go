package friend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheSurveyedHarnessesRefuseWithTheirReasonAndArePassive(t *testing.T) {
	t.Parallel()
	require.Len(t, Refusals, len(RefusedHarnesses), "every refusal is registered, in order")
	for _, h := range RefusedHarnesses {
		t.Run(h, func(t *testing.T) {
			t.Parallel()
			require.True(t, Known(h), "a surveyed harness is a harness name install takes")
			d, err := NewDeliverer(h, "/w/bob", "", nil, nil)
			require.NoError(t, err)
			assert.Equal(t, Stub{Harness: h, Reason: Refusals[h]}, d, "one passive adapter carries the surveyed reason")
			_, passive := d.(interface{ Passive() })
			assert.True(t, passive, "the daemon takes nothing off the stream for it")
			_, err = d.Deliver(context.Background(), "x")
			assert.EqualError(t, err, "no deliver command for "+h+": "+Refusals[h]+"; run the session's blocking read: nova-bus recv --as <friend>")
		})
	}
}
