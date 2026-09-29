//go:build slow

package store

import "testing"

// The longer tier of the differential test (make test-slow): 2,000 seeds of
// 150 actions each. It needs no store: the engine runs on the in-memory one.
func TestEngineAgreesWithTheReferenceModelLong(t *testing.T) {
	t.Parallel()
	dRun(t, 1000, 2000, 150)
}
