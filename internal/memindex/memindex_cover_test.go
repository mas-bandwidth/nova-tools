package memindex

import (
	"sort"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memindex_cover_test.go reaches Merge (memindex.go:380), the only function the
// unit tier left at 0.0%. Merge combines corpuses built over separate roots.
// The tests pin the main path — part order, per-part Root, path distinctness,
// Files/Bytes/DF/Post/ByClass/AvgLen — and the caller precondition its one
// guard rests on: Merge has no error return, so the only reachable refusal is
// the loud panic a roots slice shorter than parts produces.

func TestMemindexCoverMergeCombinesPartsInOrder(t *testing.T) {
	t.Parallel()

	// The same relative path under two roots is the case that keeps the class
	// meaningful and retrieval keyed by (root, path) instead of path alone.
	alpha := fstest.MapFS{
		"shared/note.md": {Data: []byte("the alpha compressor holds pressure through the night watch\n")},
		"only-a.md":      {Data: []byte("a unique alpha paragraph about the diaphone and the jetty\n")},
	}
	beta := fstest.MapFS{
		"shared/note.md": {Data: []byte("the beta glazing resists salt haze in daylight\n")},
		"only-b.md":      {Data: []byte("a unique beta paragraph about the anemometer and the gusts\n")},
	}

	cases := []struct {
		name       string
		parts      []fstest.MapFS
		roots      []string
		query      string
		wantRoot   string
		wantShared int
	}{
		{"one part", []fstest.MapFS{alpha}, []string{"alpha"}, "compressor", "alpha", 1},
		{"two parts keep the same relative path distinct", []fstest.MapFS{alpha, beta}, []string{"alpha", "beta"}, "glazing", "beta", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := make([]*Corpus, len(tc.parts))
			for i, fsys := range tc.parts {
				parts[i] = buildFS(t, fsys)
			}

			// The expected sums come from the parts, so the assertions do not
			// depend on how many chunks a fixture happens to yield.
			var wantFiles []string
			var wantBytes int64
			var wantTerms, wantChunks int
			wantByClass := map[string]int{}
			wantDF := map[string]int{}
			for _, p := range parts {
				wantFiles = append(wantFiles, p.Files...)
				wantBytes += p.Bytes
				for _, ch := range p.Chunks {
					wantChunks++
					wantTerms += ch.Len
					wantByClass[ch.Class]++
					for term := range ch.Terms {
						wantDF[term]++
					}
				}
			}

			got := Merge(parts, tc.roots)

			require.Len(t, got.Chunks, wantChunks, "Merge dropped or duplicated chunks")
			assert.Equal(t, wantFiles, got.Files, "Files must concatenate the parts in order")
			assert.Equal(t, wantBytes, got.Bytes, "Bytes must sum the parts")
			assert.Equal(t, wantByClass, got.ByClass, "ByClass must sum the parts")
			assert.Equal(t, wantDF, got.DF, "DF must count each part's chunk once per term")
			require.NotZero(t, wantChunks, "the fixture must yield indexable chunks")
			assert.InDelta(t, float64(wantTerms)/float64(wantChunks), got.AvgLen, 1e-12, "AvgLen must be the merged mean")

			// Chunks keep part order, and each carries the root of the part it
			// came from; merged ids are the slice indices in that order.
			id := 0
			for i, p := range parts {
				for _, ch := range p.Chunks {
					require.Less(t, id, len(got.Chunks), "Merge lost part %d's chunk %d", i, id)
					assert.Equal(t, tc.roots[i], got.Chunks[id].Root, "chunk %d came from part %d and must carry root %q", id, i, tc.roots[i])
					assert.Equal(t, ch.File, got.Chunks[id].File, "chunk %d is out of part order", id)
					id++
				}
			}

			// Postings stay ascending and point at chunks that hold the term:
			// the invariant Merge's guard panics on.
			for term, ids := range got.Post {
				assert.True(t, sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] }),
					"postings for %q are not ascending", term)
				for _, cid := range ids {
					require.Less(t, int(cid), len(got.Chunks), "posting for %q points past the corpus", term)
					assert.Contains(t, got.Chunks[cid].Terms, term, "posting %d for %q points at a chunk without it", cid, term)
				}
			}

			// A path shared by two roots stays two chunks; retrieval keys it by
			// (root, path), so the hit names the root it came from.
			shared := 0
			for _, ch := range got.Chunks {
				if ch.File == "shared/note.md" {
					shared++
				}
			}
			assert.Equal(t, tc.wantShared, shared, "shared/note.md must appear once per part that holds it")

			hits := Retrieve(got, []Channel{NewBM25(got)}, tc.query, 5)
			require.NotEmpty(t, hits, "the merged corpus did not answer %q", tc.query)
			assert.Equal(t, tc.wantRoot, hits[0].Root, "query %q surfaced root %q, want %q", tc.query, hits[0].Root, tc.wantRoot)
		})
	}
}

// Merge indexes roots[i] for each part; a caller that passes fewer roots than
// parts gets a loud panic instead of a silently wrong (empty) root on every
// chunk of the unrooted part.
func TestMemindexCoverMergeRefusesTooFewRoots(t *testing.T) {
	t.Parallel()

	part := buildFS(t, fstest.MapFS{
		"a.md": {Data: []byte("a paragraph long enough to index about the compressor\n")},
	})
	assert.Panics(t, func() { Merge([]*Corpus{part}, nil) },
		"Merge must refuse a roots slice shorter than parts, not index a zero root")
}
