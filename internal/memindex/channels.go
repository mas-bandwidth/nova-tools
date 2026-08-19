package memindex

import (
	"math"
	"sort"
)

// Scored is one channel's opinion of one chunk. Rank is 0-based within the
// channel's own ordering; Score is channel-native (BM25 is unbounded, Jaccard
// is [0,1]) and is NEVER compared across channels — fusion is rank-only,
// because weighted raw-score fusion of an unbounded scale against a bounded
// one is brittle and query-dependent.
type Scored struct {
	Chunk int32
	Rank  int
	Score float64
}

// Channel is the seam. This version ships bm25 and trigram; a semantic
// channel (flat exact cosine over local embeddings) would implement the same
// interface and nothing upstream would change. A channel's Query MUST be
// deterministic: ties break by chunk id.
type Channel interface {
	Name() string
	Query(text string, k int) []Scored
}

// ---------------------------------------------------------------------------
// BM25 channel

// BM25 scores candidate chunks reached through posting lists — the query
// touches only its own terms' postings, never the whole corpus, which is the
// lookup-not-scan property expressed as a data structure.
type BM25 struct {
	C     *Corpus
	K1, B float64
}

func NewBM25(c *Corpus) *BM25 { return &BM25{C: c, K1: 1.2, B: 0.75} }

func (b *BM25) Name() string { return "bm25" }

// idf is the Lucene-smoothed form. The classic form goes NEGATIVE for terms
// appearing in more than half the documents — which a small, topically
// coherent memory corpus is full of — and negative idf scrambles rankings.
func (b *BM25) idf(term string) float64 {
	n := float64(b.C.DF[term])
	N := float64(len(b.C.Chunks))
	return math.Log(1 + (N-n+0.5)/(n+0.5))
}

func (b *BM25) Query(text string, k int) []Scored {
	qterms := Tokenize(text)
	if len(qterms) == 0 {
		return nil
	}
	// Query terms are deduplicated: a membership query is the candidate's own
	// text, and repeating a word in the query must not double-count evidence.
	seen := map[string]bool{}
	scores := map[int32]float64{}
	for _, t := range qterms {
		if seen[t] {
			continue
		}
		seen[t] = true
		ids, ok := b.C.Post[t]
		if !ok {
			continue
		}
		idf := b.idf(t)
		for _, id := range ids {
			c := &b.C.Chunks[id]
			tf := float64(c.Terms[t])
			scores[id] += idf * tf * (b.K1 + 1) / (tf + b.K1*(1-b.B+b.B*float64(c.Len)/b.C.AvgLen))
		}
	}
	return topK(scores, k)
}

// ---------------------------------------------------------------------------
// Trigram channel

// Trigram ranks by character-3-gram Jaccard similarity between the query and
// each chunk. It buys robustness to morphology and small rewording that
// word-token BM25 misses, at the cost of a bounded per-chunk scan — which
// costs the CPU, not the mind, and the requirement bounds the mind's budget.
// It is never on unless the caller names it, and the caller should name it
// only once eval says it helps: on the corpus this was built for, eval
// measured bm25+trigram WORSE than bm25 alone, which is why it is a channel
// and not a default.
type Trigram struct {
	C    *Corpus
	sets []map[string]struct{} // lazily built, chunk id -> trigram set
}

func NewTrigram(c *Corpus) *Trigram { return &Trigram{C: c} }

func (t *Trigram) Name() string { return "trigram" }

func trigrams(s string) map[string]struct{} {
	set := map[string]struct{}{}
	for i := 0; i+3 <= len(s); i++ {
		set[s[i:i+3]] = struct{}{}
	}
	return set
}

func (t *Trigram) Query(text string, k int) []Scored {
	if t.sets == nil {
		t.sets = make([]map[string]struct{}, len(t.C.Chunks))
		for i := range t.C.Chunks {
			t.sets[i] = trigrams(t.C.Chunks[i].Text)
		}
	}
	q := trigrams(Normalize(text))
	if len(q) == 0 {
		return nil
	}
	scores := map[int32]float64{}
	for id := range t.C.Chunks {
		set := t.sets[id]
		inter := 0
		// Iterate the smaller set for the intersection.
		small, large := q, set
		if len(set) < len(q) {
			small, large = set, q
		}
		for g := range small {
			if _, ok := large[g]; ok {
				inter++
			}
		}
		if inter == 0 {
			continue
		}
		union := len(q) + len(set) - inter
		scores[int32(id)] = float64(inter) / float64(union)
	}
	return topK(scores, k)
}

// topK converts a score map into a ranked, deterministic slice: score
// descending, then chunk id ascending. Map iteration order never leaks
// because everything is collected before sorting.
func topK(scores map[int32]float64, k int) []Scored {
	out := make([]Scored, 0, len(scores))
	for id, s := range scores {
		out = append(out, Scored{Chunk: id, Score: s})
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

// ---------------------------------------------------------------------------
// Fusion and per-file aggregation

// FileHit is what a judge sees: the best chunk of a file, with the file's
// class and frontmatter as receipt metadata, the channel-native score of that
// chunk for calibration, and the fused score that ordered it.
type FileHit struct {
	File    string
	Class   string
	FMName  string
	FMType  string
	Para    int
	Snippet string
	Fused   float64
	// Native is the chunk's score in NativeChan, and NativeChan is the first
	// NAMED channel that actually surfaced it — not unconditionally the first
	// channel named. In a multi-channel run a chunk can reach the fused top-k
	// through the second channel alone (bm25 scored it zero, or it fell off
	// bm25's deep cutoff on a large corpus); recording only channel 0's score
	// printed 0.00 for those, and a reader comparing 0.00 against the
	// calibration band concludes the hit is weaker than unrelated control
	// text when the number is an artifact of which channel surfaced it.
	//
	// NativeChan is "" only when no channel scored the chunk, which cannot
	// happen for a chunk Retrieve returns; receipts print "-" for both fields
	// rather than a fabricated zero if it ever does.
	Native     float64
	NativeChan string
}

// rrfK is the standard reciprocal-rank-fusion constant. Rank-only fusion
// needs no score normalization and never has to know what a channel's numbers
// mean.
const rrfK = 60.0

// Retrieve runs every channel, fuses by reciprocal rank, aggregates per file
// by best fused chunk, and returns the top k files. With one channel this
// reduces to that channel's own ranking (order-preserving), so single-channel
// output is exactly that channel's opinion.
func Retrieve(c *Corpus, channels []Channel, text string, k int) []FileHit {
	if len(channels) == 0 || k <= 0 {
		return nil
	}
	// Fusion headroom: ask each channel for more than k, so a file's best
	// chunk is unlikely to be truncated away before aggregation.
	deep := k * 10
	if deep < 50 {
		deep = 50
	}
	fused := map[int32]float64{}
	// The native score is claimed by the first channel IN THE CALLER'S ORDER
	// that surfaced the chunk, so every receipt carries a score some channel
	// actually computed, and names which one. Channels is a slice, so which
	// channel claims a chunk is deterministic.
	type nativeScore struct {
		score float64
		chn   string
	}
	native := map[int32]nativeScore{}
	for _, ch := range channels {
		name := ch.Name()
		for _, s := range ch.Query(text, deep) {
			fused[s.Chunk] += 1.0 / (rrfK + float64(s.Rank))
			if _, claimed := native[s.Chunk]; !claimed {
				native[s.Chunk] = nativeScore{score: s.Score, chn: name}
			}
		}
	}
	best := map[string]FileHit{}
	for id, f := range fused {
		ch := &c.Chunks[id]
		prev, ok := best[ch.File]
		if ok && (prev.Fused > f || (prev.Fused == f && prev.Para <= ch.Para)) {
			continue
		}
		snip := Truncate(ch.Text, 120)
		n := native[id]
		best[ch.File] = FileHit{
			File: ch.File, Class: ch.Class, FMName: ch.FMName, FMType: ch.FMType,
			Para: ch.Para, Snippet: snip, Fused: f, Native: n.score, NativeChan: n.chn,
		}
	}
	hits := make([]FileHit, 0, len(best))
	for _, h := range best {
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Fused != hits[j].Fused {
			return hits[i].Fused > hits[j].Fused
		}
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Para < hits[j].Para
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}
