package swarm

// A bench's width is a measured power of two (SPEC-SWARM, "Benches", #528).
//
// bench size runs the known-answer card W times concurrently for W = 1, 2, 4, ...
// and keeps doubling while three rules hold at the end of each round. The width is
// the last W that held. This file holds the doubling loop and the round it measures,
// apart from the command that drives them, so the loop is unit-testable without a
// bench, a harness or ssh.

// SizeRound is one measurement round at concurrency W.
type SizeRound struct {
	Load        float64 // the bench's one-minute load at the end of the round
	CardsPerMin float64 // W cards over the round's wall time, per minute
	Abstains    int     // cards that abstained (idle, deadline, refusal)
}

// sizeLoadFactor is rule (a): the one-minute load may not pass this multiple of cores.
const sizeLoadFactor = 1.25

// sizeScaleFactor is rule (b): cards per minute at W must be at least this multiple of
// cards per minute at W/2.
const sizeScaleFactor = 1.5

// SizeWidth finds the widest power of two at which all three rules hold. It doubles
// from 1 up to max, measuring one round per W, and stops on the first round that
// breaks any rule: (a) the one-minute load is at most 1.25 x cores; (b) throughput
// scales — cards per minute at W is at least 1.5 x cards per minute at W/2; (c) no
// card abstained. The width is the last W that held, 0 when even W=1 broke.
func SizeWidth(max, cores int, measure func(w int) SizeRound) int {
	width := 0
	var prev float64
	for w := 1; w <= max; w *= 2 {
		r := measure(w)
		if r.Load > float64(cores)*sizeLoadFactor {
			break
		}
		if prev != 0 && r.CardsPerMin < sizeScaleFactor*prev {
			break
		}
		if r.Abstains > 0 {
			break
		}
		width = w
		prev = r.CardsPerMin
	}
	return width
}
