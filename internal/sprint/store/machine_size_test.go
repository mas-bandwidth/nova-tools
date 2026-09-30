package store

// crSize is how large the tick's property tests run (crScale): seeds of the
// forty-primary sprint, the rounds a stop begins at, the stride over the
// calls a race or a failure is injected at, the two-loop and zombie trials
// and their rounds, the ready primaries dealt, the silent coordinator's
// rounds, and the cut trials and rounds.
type crSize struct {
	Seeds                      int
	StopPoints                 []int
	CallStride                 int
	LoopTrials, LoopTicks      int
	LoopRounds                 int
	ZombieTrials, ZombieRounds int
	Ready                      int
	SilentRounds               int
	CutTrials, CutRounds       int
}
