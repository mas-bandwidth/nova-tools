package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// FNV-1a modulo a power of two sees only the low bits of the salt's bytes: with
// weights 1,1 the draw was the parity of the salt's odd bytes, agreeing with that
// prediction on every card (2000 of 2000). Through splitmix64's finaliser it agrees
// as often as a coin does.
func TestTheDrawIsNotTheSaltsParity(t *testing.T) {
	t.Parallel()
	rs := []Route{{Name: "a", Weight: 1}, {Name: "b", Weight: 1}}
	agree, got := 0, map[string]int{}
	const n = 2000
	for i := 0; i < n; i++ {
		salt := "s1-" + itoa(i) + "\x00" + "1" + "\x00" + "2026-10-01T05:00:00Z"
		odd := 0
		for _, c := range []byte(salt) {
			odd ^= int(c & 1)
		}
		pred := "a"
		if (0xcbf29ce484222325&1)^uint64(odd) == 1 {
			pred = "b"
		}
		r := draw(append([]Route(nil), rs...), salt).Name
		got[r]++
		if r == pred {
			agree++
		}
	}
	assert.InDelta(t, n/2, agree, n/10, "the draw agrees with the salt's parity %d of %d times", agree, n)
	assert.InDelta(t, n/2, got["a"], n/10, "1:1 over %d draws: %v", n, got)
}
