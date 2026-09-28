package memindex

import (
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// A full sort is deliberately retained as the independent ordering oracle.
// The bounded selector must preserve scores, tie-breaking, and ranks exactly.
func sortedScores(scores map[int32]float64, k int) []Scored {
	out := make([]Scored, 0, len(scores))
	for id, score := range scores {
		out = append(out, Scored{Chunk: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Chunk < out[j].Chunk
	})
	if len(out) > k {
		out = out[:k]
	}
	for i := range out {
		out[i].Rank = i
	}
	return out
}

func TestTopKMatchesFullSort(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(42, 91))
	for _, n := range []int{0, 1, 7, 50, 51, 257, 4096} {
		scores := make(map[int32]float64, n)
		for _, id := range rng.Perm(n) {
			// Many ties, with non-contiguous chunk IDs and fractional scores.
			scores[int32(id*3+2)] = float64(rng.IntN(23)) / 7
		}
		for _, k := range []int{0, 1, 7, 50, n, n + 1} {
			want := sortedScores(scores, k)
			for repeat := 0; repeat < 3; repeat++ {
				if got := topK(scores, k); !reflect.DeepEqual(got, want) {
					t.Fatalf("n=%d k=%d: got %v, want %v", n, k, got, want)
				}
			}
		}
	}
}

// TestTopKSelectsWithABoundedHeap is the negative control for reverting the
// heap selector in channels.go. TestTopKMatchesFullSort is an ordering
// oracle both the pre-2f3c6e0e full sort and the heap satisfy, so it stayed
// green when the production file was restored.
func TestTopKSelectsWithABoundedHeap(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("channels.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		`"container/heap"`,
		"type scoreHeap",
		"heap.Init",
		"heap.Fix",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("channels.go must keep the bounded heap top-k selector; missing %q", want)
		}
	}
	// The unguarded selector allocated every candidate, sorted, then sliced.
	if strings.Contains(text, "make([]Scored, 0, len(scores))") {
		t.Error("topK must not collect every candidate before selecting k")
	}
}

func BenchmarkTopK(b *testing.B) {
	for _, n := range []int{128, 1024, 16384, 131072} {
		for _, k := range []int{50, n} {
			b.Run(fmt.Sprintf("candidates=%d/k=%d", n, k), func(b *testing.B) {
				scores := make(map[int32]float64, n)
				for i := 0; i < n; i++ {
					scores[int32(i)] = float64((i*7919)%1009) / 7
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					_ = topK(scores, k)
				}
			})
		}
	}
}

func BenchmarkBM25CommonTerm(b *testing.B) {
	for _, n := range []int{128, 2048, 32768} {
		b.Run(fmt.Sprintf("chunks=%d", n), func(b *testing.B) {
			c := &Corpus{Chunks: make([]Chunk, n), DF: map[string]int{"common": n}, Post: map[string][]int32{"common": make([]int32, n)}, AvgLen: 10}
			for i := range c.Chunks {
				c.Chunks[i] = Chunk{Terms: map[string]int{"common": 1 + i%5}, Len: 6 + i%9}
				c.Post["common"][i] = int32(i)
			}
			channel := NewBM25(c)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = channel.Query("common", 50)
			}
		})
	}
}
