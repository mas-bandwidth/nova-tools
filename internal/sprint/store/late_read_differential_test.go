package store

import "testing"

// Claim (e485c4b5): the reference model does not model the tick's late ask,
// and nothing tells the two apart once the clock passes the late bound. The
// slow tier (make test-slow: 2,000 seeds of 150 actions, one second an action)
// runs past it: on 701aca94 it shows 17 unknown signatures, on 4a7e7476 18,
// on e485c4b5 33: 14 new seeds of the shape "tick: read.exists=yes/no
// round.ask", none on 4a7e7476: the engine asks one more reader of a late
// read, the model asks none. Seed 1106 is one of
// them, run here in the default tier: the engine's a1.r1.r3 at action 82.
func TestEngineAgreesWithTheReferenceModelPastTheLateBound(t *testing.T) {
	t.Parallel()
	dRun(t, 1106, 1, 150)
}
