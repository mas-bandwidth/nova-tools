package sprint

import "strings"

// StreamsQuery selects the streams listing (docs/SPEC-SPRINT.md section 11).
// Repo keeps streams whose cards name that repository. Release keeps streams
// whose control card records that release (stream set --release). Cards
// includes every placed card of each kept stream.
type StreamsQuery struct {
	Repo, Release string
	Cards         bool
}

// StreamCardView is one placed card: its id, state, tier, the first sentence
// of THE TASK, and the needs it names.
type StreamCardView struct {
	ID    string   `json:"id"`
	State string   `json:"state"`
	Tier  string   `json:"tier"`
	Title string   `json:"title"`
	Needs []string `json:"needs"`
}

// StreamView is one stream: the repositories and bases its cards name, the
// release on its control card, and how many of its placed cards are open or
// landed.
type StreamView struct {
	Stream  string           `json:"stream"`
	Repos   []string         `json:"repos"`
	Bases   []string         `json:"bases"`
	Release string           `json:"release"`
	Open    int              `json:"open"`
	Landed  int              `json:"landed"`
	Cards   []StreamCardView `json:"cards,omitempty"`
}

// StreamsView is every stream the query keeps, in work-row order, and the
// mix findings for those streams.
type StreamsView struct {
	Streams  []StreamView `json:"streams"`
	Findings []string     `json:"findings"`
}

// StreamsList lists each stream from one snapshot of the work and merge
// tables (docs/SPEC-SPRINT.md section 11). The caller loads both tables
// once. Repositories and bases come from the placed cards' briefs. A stream
// keeps every distinct name. Open is every placed card that is not landed.
func StreamsList(s *Snapshot, q StreamsQuery) StreamsView {
	out := StreamsView{Streams: []StreamView{}, Findings: []string{}}
	if s == nil {
		return out
	}
	q.Repo = strings.TrimSpace(q.Repo)
	q.Release = strings.TrimSpace(q.Release)
	for _, stream := range s.Streams() {
		cards := streamPlaced(s, stream)
		briefs := make([]string, len(cards))
		for i, c := range cards {
			briefs[i] = c.F("brief")
		}
		repos, bases := RecordPlaces(briefs)
		release := s.StreamCtl(stream).F(FieldRelease)
		if q.Repo != "" && !contains(repos, q.Repo) {
			continue
		}
		if q.Release != "" && release != q.Release {
			continue
		}
		row := StreamView{Stream: stream, Repos: repos, Bases: bases, Release: release}
		if q.Cards {
			row.Cards = make([]StreamCardView, 0, len(cards))
		}
		for _, c := range cards {
			if c.Col == Landed {
				row.Landed++
			} else {
				row.Open++
			}
			if !q.Cards {
				continue
			}
			needs := Split(c.F("needs"))
			if needs == nil {
				needs = []string{}
			}
			row.Cards = append(row.Cards, StreamCardView{
				ID: c.ID, State: c.Col, Tier: CardTier(c), Title: TaskTitle(c.F("brief")), Needs: needs,
			})
		}
		out.Streams = append(out.Streams, row)
		out.Findings = append(out.Findings, MixFindings(stream, repos, bases)...)
	}
	return out
}

// streamPlaced is the stream's placed work cards in column order, then score
// order. A control card lives on the merge table and is not one of them.
func streamPlaced(s *Snapshot, stream string) []*Card {
	var out []*Card
	for _, col := range States {
		out = append(out, s.Work.Cell(stream, col)...)
	}
	return out
}
