package cardgen

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestGeneratedBenchStatusUsesTheSharedLoadChoice(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	h := Header{Repo: "owner/repo", Base: "base", BenchAt: now, Benches: []sprint.GateBench{
		{Name: "bench-a", Bench: true, LoadKnown: true, Cores: 64, Load1: 164, At: now},
		{Name: "bench-b", Bench: true, LoadKnown: true, Cores: 32, Load1: 1, At: now},
	}}
	out := Render(h, Card{ID: "c1", Tier: "flash", File: "internal/p/x.go", Test: "internal/p TestRule", Paths: []string{"internal/p/x.go"}})
	assert.Contains(t, out, "STATUS: BENCH: bench-b (load 1.0 of 32 cores")
	assert.NotContains(t, out, "\nBENCH:", "a gate choice cannot become the card's execution-member constraint")
	h.BenchLoadCap = 0.001
	assert.Contains(t, Render(h, Card{ID: "c1", Tier: "flash"}), "STATUS: BENCH REFUSED: no eligible current bench")
}
