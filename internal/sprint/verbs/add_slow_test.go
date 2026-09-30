//go:build slow

package verbs

import "testing"

// TestAddCountThreeStreamsGates: add --stream s1,s2,s3 --count 30000
// --sentinel-every 1000 (none after the last): 90,000 cards and 87 gates at
// contiguous integer scores, the part cut and the round trips exact: IT19's
// limit, 90,087 cards and three control cards in 46 parts of 2,000 changed
// members, 47 round trips (1.5.3: n + 1). It takes about 6 s on the twin
// (the table twin copies its state a step), past the unit tier's 1 s, so it
// runs in the slow tier (make test-slow); the unit tier runs it at a twentieth
// (TestAddCountThreeStreamsGatesScaled).
func TestAddCountThreeStreamsGates(t *testing.T) {
	t.Parallel()
	threeStreams(t, 30000, 1000, 46, 47)
}
