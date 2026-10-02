package swarm

import (
	"math"
)

// Bench width: a measured power of two (docs/SPEC-SWARM.md "Benches").
//
// `bench size` runs the known-answer card W times concurrently for
// W = 1, 2, 4, ... and keeps doubling while three rules hold at the end of
// each round: (a) the one-minute load is at most 1.25 x cores; (b) throughput
// scales, cards per minute at W at least 1.5 x cards per minute at W/2;
// (c) no card abstained. The width is the last W that held. A batch fills a
// bench up to its width and, per tick, launches at most cores x 1.5 - load
// cards, never more than cores in one tick: the bench's headroom. A loaded
// machine therefore drops down without changing its width.

// SizeLoadFactor is rule (a): the one-minute load holds at most 1.25 x cores.
const SizeLoadFactor = 1.25

// SizeScaleFactor is rule (b): cards per minute at W holds at least 1.5 x
// cards per minute at W/2.
const SizeScaleFactor = 1.5

// SizeHeadroomFactor is the launch headroom per tick: cores x 1.5 - load.
const SizeHeadroomFactor = 1.5

// SizeRound is one doubling round's measurements.
type SizeRound struct {
	W               int
	Cores           int
	Load            float64
	CardsPerMin     float64
	PrevCardsPerMin float64
	Abstains        int
	Held            bool
}

// SizeRoundHolds answers the three rules for one round: load at most
// 1.25 x cores, throughput at least 1.5 x the previous round (vacuous for the
// first round, which has no W/2), and no abstain.
func SizeRoundHolds(r SizeRound) bool {
	if r.Abstains != 0 {
		return false
	}
	if r.Cores > 0 && r.Load > SizeLoadFactor*float64(r.Cores) {
		return false
	}
	if r.W > 1 && r.PrevCardsPerMin > 0 && r.CardsPerMin < SizeScaleFactor*r.PrevCardsPerMin {
		return false
	}
	return true
}

// MeasureWidthFromRounds is the doubling loop over already-run rounds: the
// width is the last W that held, and the doubling ends at the first round
// that breaks a rule. No round held is width 0.
func MeasureWidthFromRounds(rounds []SizeRound) int {
	width := 0
	for _, r := range rounds {
		if !SizeRoundHolds(r) {
			break
		}
		width = r.W
	}
	return width
}

// Headroom is how many cards one tick may launch: cores x 1.5 minus the
// one-minute load, never more than cores in one tick, floored at 0. A
// non-positive cores count is no pinning and no cap: -1, unbounded.
func Headroom(cores int, load float64) int {
	if cores <= 0 {
		return -1
	}
	h := int(math.Floor(SizeHeadroomFactor*float64(cores) - load))
	if h < 0 {
		return 0
	}
	if h > cores {
		return cores
	}
	return h
}

// PlanBenchLaunch fills a bench up to its width and drops down under load:
// at most width cards, at most headroom cards, at most queued cards. A width
// of 0 is an unmeasured bench and fills by cores as today; a non-positive
// cores count caps by width only.
func PlanBenchLaunch(cores int, load float64, width, queued int) int {
	if queued <= 0 {
		return 0
	}
	n := queued
	if width > 0 && n > width {
		n = width
	}
	if cores > 0 {
		if h := Headroom(cores, load); n > h {
			n = h
		}
	} else if width <= 0 {
		return queued
	}
	if n < 0 {
		return 0
	}
	return n
}

// Version8 is the row's version: the tool's identity in 8 characters.
func Version8(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
