//go:build !slow

package store

// crScale is the size of the tick's property tests in the unit tier: each
// keeps its assertion at a size that fits the package's two seconds. The slow
// tier (go test -tags slow, make test-slow) runs them at full size
// (machine_scale_slow_test.go); they need no store.
var crScale = crSize{
	Seeds:        1,
	StopPoints:   []int{9},
	CallStride:   23,
	LoopTrials:   1,
	LoopTicks:    40,
	LoopRounds:   25,
	ZombieTrials: 1,
	ZombieRounds: 20,
	Ready:        120,
	SilentRounds: 8,
	CutTrials:    1,
	CutRounds:    12,
}
