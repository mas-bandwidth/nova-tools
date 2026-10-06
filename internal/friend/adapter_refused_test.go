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

// A dsh turn the session cannot take is a refusal on exit 0 as on any other
// exit: the output carries it, and the exit code does not (docs/SPEC-FRIEND.md,
// a dsh turn the session cannot take).
func TestADeafDSHTurnIsARefusalWhateverTheExit(t *testing.T) {
	t.Parallel()
	preset := `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose`
	exit, err := refused("session-zhi", preset, 0, nil)
	assert.Equal(t, 0, exit)
	var deaf DSHDeaf
	require.ErrorAs(t, err, &deaf)
	assert.Equal(t, "session-zhi", deaf.Session)
	assert.Equal(t, "dsh session session-zhi: agent preset minimal", deaf.Down)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "the message stays pending")

	exit, err = refused("session-zhi", "stopped: MISSING_CREDENTIAL\n", 0, nil)
	assert.Equal(t, 0, exit)
	require.ErrorAs(t, err, &deaf)
	assert.Equal(t, "dsh: missing credential", deaf.Down)

	exit, err = refused("session-zhi", "turn went fine\n", 0, nil)
	assert.Equal(t, 0, exit)
	assert.NoError(t, err, "a clean exit 0 is a delivery")
}
