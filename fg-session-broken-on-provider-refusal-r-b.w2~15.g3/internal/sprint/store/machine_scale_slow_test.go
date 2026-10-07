//go:build slow

package store

// crScale at full size, for the slow tier: every seed, every stop point,
// every call.
var crScale = crSize{
	Seeds:        20,
	StopPoints:   stopPoints(1, 30),
	CallStride:   1,
	LoopTrials:   5,
	LoopTicks:    150,
	LoopRounds:   60,
	ZombieTrials: 40,
	ZombieRounds: 60,
	Ready:        1000,
	SilentRounds: 60,
	CutTrials:    2,
	CutRounds:    30,
}

func stopPoints(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}
