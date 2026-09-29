package driver

import "math/rand/v2"

// Seeded is invented facts from a seed: the same seed, against the same
// table, plays the same run. Each probability is per card (work, read), per
// batch (stuck, cross, red) or per member and tick (flap).
type Seeded struct {
	rng                             *rand.Rand
	Fail, Broken, Stuck, Cross, Red float64
	Flap                            float64
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

func (s *Seeded) Merge(stream string, batch []string, others []string) Outcome {
	r := s.rng.Float64()
	switch {
	case r < s.Stuck:
		return Outcome{Conflict: batch[s.rng.IntN(len(batch))]}
	case r < s.Stuck+s.Cross && len(others) > 0:
		return Outcome{Cross: batch[s.rng.IntN(len(batch))] + "=" + others[s.rng.IntN(len(others))]}
	case r < s.Stuck+s.Cross+s.Red:
		return Outcome{Red: true}
	}
	return Outcome{}
}

func (s *Seeded) Up(tick int, members []string, up map[string]bool) map[string]bool {
	next := map[string]bool{}
	for _, m := range members {
		next[m] = up[m]
		if s.Flap > 0 && s.rng.Float64() < s.Flap {
			next[m] = !up[m]
		}
	}
	return next
}
