package member

import (
	"bytes"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// epochRunner is a fakeRunner that is an Epocher: it records every epoch it is told.
type epochRunner struct {
	*fakeRunner
	mu     sync.Mutex
	epochs []uint64
}

func (r *epochRunner) Epoch(epoch uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epochs = append(r.epochs, epoch)
}

func (r *epochRunner) told() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.epochs)
}

// TestTheRunnerIsToldTheSprintsEpochEachPass pins the one thing the pass hands the runner's
// cleaner: the epoch the queue answered, once a pass, the pass's cards or none; a pass whose
// queue did not answer tells nothing.
func TestTheRunnerIsToldTheSprintsEpochEachPass(t *testing.T) {
	t.Parallel()
	s, r := newScript(), &epochRunner{fakeRunner: newRunner()}
	m := New(Config{As: "m", Width: 1}, s, r, &fakePusher{}, &bytes.Buffer{})
	s.set("queue", 0, queueJSON(t, 13))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	s.set("queue", 0, queueJSON(t, 14))
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	s.set("queue", 1, "the store did not answer")
	_, err = m.Tick(time.Unix(0, 0))
	require.Error(t, err)
	assert.Equal(t, []uint64{13, 14}, r.told())
}
