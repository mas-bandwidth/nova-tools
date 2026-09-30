package driver

import (
	"math/rand/v2"
	"time"
)

// Seeded is invented facts from a seed: the same seed, against the same
// table, plays the same run. Each probability is per report (work, read: a
// card reported again draws again), per batch (stuck, cross, red) or per
// member and tick (Down, and Back, the chance a member that is down comes up).
type Seeded struct {
	rng                             *rand.Rand
	Fail, Broken, Stuck, Cross, Red float64
	Down, Back                      float64
}

// NewSeeded is a seeded source.
func NewSeeded(seed uint64) *Seeded {
	return &Seeded{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

func (s *Seeded) Work(card string) (bool, string) {
	if s.rng.Float64() < s.Fail {
		return false, "the tests went red"
	}
	return true, ""
}

func (s *Seeded) Read(card string) (bool, string) {
	if s.rng.Float64() < s.Broken {
		return false, "an edge case is not handled"
	}
	return true, ""
}

// Use sets the chances the source draws against: the per report and per batch
// chances as they are, and Down and Up, which are each second's, as the
// chance of a tick of every (PerTick: the configured every, not the time that
// passes between two draws).
func (s *Seeded) Use(c Chances, every time.Duration) {
	s.Fail, s.Broken, s.Stuck, s.Cross = c.Fail, c.Broken, c.Stuck, c.Cross
	s.Down, s.Back = PerTick(c.Down, every), PerTick(c.Up, every)
}

// Drawn is the chances this source draws against, as it holds them: Down and
// Up are the chance of one tick, which is the flags' own only at a tick of a
// second. It is what a run prints as the chances it draws with.
func (s *Seeded) Drawn() Chances {
	return Chances{Broken: s.Broken, Fail: s.Fail, Stuck: s.Stuck, Cross: s.Cross, Down: s.Down, Up: s.Back}
}

// Merge draws a batch's fact; others is read only for a cross fact, and a
// cross drawn with no card queued in another stream is green.
func (s *Seeded) Merge(stream string, batch []string, others func() []string) Outcome {
	r := s.rng.Float64()
	switch {
	case r < s.Stuck:
		return Outcome{Conflict: batch[s.rng.IntN(len(batch))]}
	case r < s.Stuck+s.Cross:
		if o := others(); len(o) > 0 {
			return Outcome{Cross: batch[s.rng.IntN(len(batch))] + "=" + o[s.rng.IntN(len(o))]}
		}
	case r < s.Stuck+s.Cross+s.Red:
		return Outcome{Red: true, Suspects: []string{batch[s.rng.IntN(len(batch))]}}
	}
	return Outcome{}
}

// Up draws each member once: an up member goes down with the chance Down, and
// a down member comes up with the chance Back. A chance of 0 draws nothing.
func (s *Seeded) Up(tick int, members []string, up map[string]bool) map[string]bool {
	next := map[string]bool{}
	for _, m := range members {
		next[m] = up[m]
		p := s.Back
		if up[m] {
			p = s.Down
		}
		if p > 0 && s.rng.Float64() < p {
			next[m] = !up[m]
		}
	}
	return next
}
