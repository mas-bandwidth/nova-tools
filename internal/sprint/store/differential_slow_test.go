//go:build slow

package store

import "testing"

// The longer tier of the differential test (make test-slow): 2,000 seeds of
// 150 actions each. It needs no store: the engine runs on the in-memory one.
func TestEngineAgreesWithTheReferenceModelLong(t *testing.T) {
	t.Parallel()
	dRun(t, 1000, 2000, 150)
}

// The first seed of every difference the long run has found, run again:
// 2299 (the redeal bound, which the model first lacked).
func TestEngineAgreesWithTheReferenceModelFoundSeeds(t *testing.T) {
	t.Parallel()
	dRun(t, 2299, 1, 150)
}
