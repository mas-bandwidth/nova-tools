package sprint

import (
	"regexp"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// A stream's release (docs/SPEC-SPRINT.md section 11, stream set and where): the
// release its cards count toward, a field of its control card, written by
// `stream set <s> --release <name>` and taken off by `--release none`; `where
// --release <name>` counts the cards left in each stream of the release. Which
// streams were nova-tools v1.2.0 and which nova-sprint v1.0.0 lived in the
// coordinator's head (the coordinator's list of 2026-10-04, item 20: what is
// manual needs a verb). A clear starts the next epoch with none, as it starts
// every control card's field.
const (
	// FieldRelease is a stream's control card's field: its release.
	FieldRelease = "release"
	// ReleaseNone is the word that takes a stream out of its release.
	ReleaseNone = "none"
)

// releaseRE is a release name: one word of letters, digits, '.', '_' and '-'
// (v1.2.0, nova-sprint-v1.0.0), at most MaxIDLen.
var releaseRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// ValidRelease says a release name is one word a stream may carry: never the
// word that takes one off.
func ValidRelease(name string) bool {
	return len(name) <= MaxIDLen && releaseRE.MatchString(name) && name != ReleaseNone
}

// ReleaseStream is one stream of a release and the cards left in it.
type ReleaseStream struct {
	Stream string `json:"stream"`
	Left   int64  `json:"left"`
}

// ReleaseCount is a release, the cards left in it (the sum over its streams)
// and its streams in the work table's order.
type ReleaseCount struct {
	Release string          `json:"release"`
	Left    int64           `json:"left"`
	Streams []ReleaseStream `json:"streams"`
}

// ReleaseOf is the release named, counted from the work table's cells and the
// streams' control cards (StreamClock.Release), never from a card's record
// (docs/SPEC-SPRINT.md section 1: where reads the tables' cells and one record):
// a stream's cards left are every card its work row counts and has not landed,
// as the progress line counts them. ok is false when no stream is in it.
func ReleaseOf(work ntable.Table, clocks []StreamClock, name string) (ReleaseCount, bool) {
	in := map[string]bool{}
	for _, c := range clocks {
		if c.Release == name {
			in[c.Stream] = true
		}
	}
	if name == "" || len(in) == 0 {
		return ReleaseCount{}, false
	}
	out := ReleaseCount{Release: name, Streams: []ReleaseStream{}}
	landed := work.Column(Landed)
	for _, r := range work.Rows {
		if !in[r.Key] {
			continue
		}
		var left int64
		for k, c := range work.Columns {
			if c.Projection != ntable.Count || k >= len(r.Cells) {
				continue
			}
			if k != landed {
				left += r.Cells[k].Count
			}
		}
		out.Left += left
		out.Streams = append(out.Streams, ReleaseStream{Stream: r.Key, Left: left})
		delete(in, r.Key)
	}
	// a stream in the release with no work row (a row the read did not bring)
	// counts none left and is still named
	for _, c := range clocks {
		if in[c.Stream] {
			out.Streams = append(out.Streams, ReleaseStream{Stream: c.Stream})
		}
	}
	return out, true
}

// ReleaseNames is every release a stream carries, sorted, each once.
func ReleaseNames(clocks []StreamClock) []string {
	var out []string
	for _, c := range clocks {
		if c.Release != "" && !slices.Contains(out, c.Release) {
			out = append(out, c.Release)
		}
	}
	slices.Sort(out)
	return out
}
