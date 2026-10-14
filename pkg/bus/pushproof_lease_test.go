package bus

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A name whose bus push is proven where nova-hook records it reads as heard
// even when no friend daemon's session check has written bus2:push: a live
// lease and a proof (docs/SPEC-HOOK.md, the key layout and the states) are
// push proven with no NOTE (SPEC-BUS.md, bus-requires-inbox-push-proof). A
// live lease with no proof, a proof with no live lease, and a name with
// neither all read push=none, and the --require-push remedy speaks of the
// person as they, never "its".
func TestPushProofReadsAProvenHookLeaseAndUsesTheirNotIts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := NewFake(at, "ada", "bob", "cy", "dee")
	b := &Bus{Store: f}
	f.records = map[string]fakeRecord{
		// ada: a live lease and a proof, as a holding daemon and an answering
		// session write them, with no bus2:push session check beside them.
		hook.LiveKey("ada", hook.Bus):  {value: `{"epoch":1,"at":1,"by":"holder"}`, until: at.Add(time.Minute)},
		hook.ProofKey("ada", hook.Bus): {value: `{"epoch":1,"at":1,"by":"session"}`, until: at.Add(time.Minute)},
		// cy: a live lease with no proof: armed, not proven.
		hook.LiveKey("cy", hook.Bus): {value: `{"epoch":1,"at":1,"by":"holder"}`, until: at.Add(time.Minute)},
		// dee: a proof whose lease has lapsed: the holder died, so not proven.
		hook.ProofKey("dee", hook.Bus): {value: `{"epoch":1,"at":1,"by":"session"}`, until: at.Add(time.Minute)},
	}

	lines, err := b.Unheard(ctx, "ada")
	require.NoError(t, err)
	assert.Empty(t, lines, "a live proven hook lease is push proven: no NOTE")

	for _, n := range []string{"bob", "cy", "dee"} {
		lines, err = b.Unheard(ctx, n)
		require.NoError(t, err)
		require.Len(t, lines, 1, "one NOTE for %s", n)
		assert.Contains(t, lines[0], "push=none for "+n+": no proven push since never", "%s", n)
	}

	remedy := PushProof{Name: "bob"}.Deaf(at)
	assert.NotContains(t, remedy, " its ", "the remedy speaks of the person as they")
	assert.Contains(t, remedy, "bob runs their friend daemon", "the remedy names their daemon")
	assert.Contains(t, remedy, "their session answers")
}
