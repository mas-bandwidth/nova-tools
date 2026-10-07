package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// epochDown is a store whose epoch cannot be read: Redis gone, or the caller's context
// ended while the tick began.
type epochDown struct{ *Mem }

func (epochDown) Epoch(context.Context) (EpochState, error) {
	return EpochState{}, errors.New("the store did not answer")
}

// A tick whose repin fails returns the error and does not panic: the deferred notes
// were read from the store repin returned, nil on its failure, and the server process
// died of it (found 2026-10-04 by the server's load test as its run loop stopped).
func TestATickWhoseRepinFailsReturnsTheError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	st := *h.st
	st.B, st.root = epochDown{h.m}, nil
	_, err := st.Tick(h.ctx)
	require.ErrorContains(t, err, "the store did not answer")
}
