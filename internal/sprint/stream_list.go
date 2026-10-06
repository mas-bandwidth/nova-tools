package sprint

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The one listing of streams by repository (docs/SPEC-SPRINT.md section 11,
// streams). One snapshot, the work and merge tables read once, is every
// stream: the repositories and bases its placed cards' briefs name, the
// release on its control card, and its open and landed counts.

// StreamListFilter keeps streams. Empty Repo and Release keep every stream
// that is drawn. Cards adds each placed card of a kept stream.
type StreamListFilter struct {
	Repo    string
	Release string
	Cards   bool
}

// StreamCardView is one card of a stream listing.
type StreamCardView struct {
	ID    string   `json:"id"`
	State string   `json:"state"`
	Tier  string   `json:"tier"`
	Title string   `json:"title"`
	Needs []string `json:"needs,omitempty"`
}

// StreamView is one stream in the listing.
type StreamView struct {
	Stream  string           `json:"stream"`
	Repos   []string         `json:"repos"`
	Bases   []string         `json:"bases"`
	Release string           `json:"release,omitempty"`
	Open    int              `json:"open"`
	Landed  int              `json:"landed"`
	Finding string           `json:"finding,omitempty"`
	Cards   []StreamCardView `json:"cards,omitempty"`
}

// RepoAct is drop or hold by the repository a card's brief names
// (docs/SPEC-SPRINT.md section 11, streams). Expect is the count the owner
// read. Streams, when set, is only those streams. Who and Reason are the
// step's, as drop and hold record them.
type RepoAct struct {
	Repo    string
	Streams []string
	Expect  int
	Reason  string
	Who     string
}

// StreamsList is every drawn stream, in row order, that the filter keeps.
// Repos and bases are the distinct REPO: and BASE: lines of the stream's
// placed cards, in name order. More than one of either is a finding. Open is
// a card that has not landed. Landed is the landed column. The release is
// the control card's (stream set --release).
func StreamsList(s *Snapshot, f StreamListFilter) []StreamView {
	if s == nil {
		return nil
	}
	var out []StreamView
	for _, st := range s.Streams() {
		if s.Work.Hidden(st) {
			continue
		}
		cards := streamWork(s, st)
		repos, bases := []string{}, []string{}
		seenR, seenB := map[string]bool{}, map[string]bool{}
		var open, landed int
		var lines []StreamCardView
		for _, c := range cards {
			addName(&repos, seenR, CardStreamRepo(c.F("brief")))
			addName(&bases, seenB, CardStreamBase(c.F("brief")))
			switch {
			case c.Col == Landed:
				landed++
			case IsOpen(c.Col):
				open++
			}
			if f.Cards {
				needs := Split(c.F("needs"))
				lines = append(lines, StreamCardView{
					ID: c.ID, State: c.Col, Tier: CardTier(c), Title: taskTitle(c.F("brief")), Needs: needs,
				})
			}
		}
		slices.Sort(repos)
		slices.Sort(bases)
		release := s.StreamCtl(st).F(FieldRelease)
		if f.Repo != "" && !slices.Contains(repos, f.Repo) {
			continue
		}
		if f.Release != "" && release != f.Release {
			continue
		}
		out = append(out, StreamView{
			Stream: st, Repos: repos, Bases: bases, Release: release,
			Open: open, Landed: landed, Finding: streamFinding(st, repos, bases), Cards: lines,
		})
	}
	return out
}

// DropByRepo takes off the table the open cards whose briefs name r.Repo
// (docs/SPEC-SPRINT.md section 11, streams). It refuses, writing nothing,
// when --expect is omitted or is not that count, when a named stream is not
// a stream, or when no reason is given. The count is what the owner reads
// before it runs.
func DropByRepo(s *Snapshot, r RepoAct) Plan {
	cards, why := repoSelect(s, r, true)
	if why != "" {
		return Plan{Refused: []Refusal{{Key: r.Repo, Why: why}}}
	}
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	return Drop(s, DropReq{Sel: Sel{IDs: ids}, Reason: r.Reason, Who: r.Who})
}

// HoldByRepo holds each stream that has an open card whose brief names r.Repo
// (docs/SPEC-SPRINT.md section 11, streams). A stream that also names another
// repository is held whole. It refuses, writing nothing, when --expect is
// omitted or is not the number of those streams, when a named stream is not
// a stream, or when no reason is given.
func HoldByRepo(s *Snapshot, r RepoAct) Plan {
	_, why := repoSelect(s, r, false)
	if why != "" {
		return Plan{Refused: []Refusal{{Key: r.Repo, Why: why}}}
	}
	names := repoStreams(s, r)
	if r.Expect <= 0 {
		return Plan{Refused: []Refusal{{Key: r.Repo, Why: fmt.Sprintf("refused without --expect <n>: %d streams name %s (%s); nothing was changed", len(names), r.Repo, orDash(strings.Join(names, ",")))}}}
	}
	if r.Expect != len(names) {
		return Plan{Refused: []Refusal{{Key: r.Repo, Why: fmt.Sprintf("--expect %d but %d streams name %s (%s); nothing was changed", r.Expect, len(names), r.Repo, orDash(strings.Join(names, ",")))}}}
	}
	if r.Reason == "" {
		return Plan{Refused: []Refusal{{Key: r.Repo, Why: "--reason <text> is required; nothing was changed"}}}
	}
	return HoldNames(s, HoldReq{Names: names, Reason: r.Reason, Who: r.Who, Kind: HoldStream})
}

// repoSelect checks the act and, for a drop, the open cards it would take.
// why is set when the act is refused whole. A hold's count is streams, so
// the card list is not the expect.
func repoSelect(s *Snapshot, r RepoAct, drop bool) ([]*Card, string) {
	if r.Repo == "" {
		return nil, "--repo <owner/name> is required; nothing was changed"
	}
	if _, why := namedStreams(s, r.Streams); why != "" {
		return nil, why
	}
	if !drop {
		return nil, ""
	}
	cards := repoCards(s, r)
	if r.Reason == "" {
		return nil, "--reason <text> is required; nothing was changed"
	}
	if r.Expect <= 0 {
		return nil, fmt.Sprintf("refused without --expect <n>: %d open cards name %s; nothing was changed", len(cards), r.Repo)
	}
	if r.Expect != len(cards) {
		return nil, fmt.Sprintf("--expect %d but %d open cards name %s; nothing was changed", r.Expect, len(cards), r.Repo)
	}
	return cards, ""
}

// namedStreams refuses a name that is not a drawn stream. Empty is every stream.
func namedStreams(s *Snapshot, streams []string) (map[string]bool, string) {
	if len(streams) == 0 {
		return nil, ""
	}
	allow := map[string]bool{}
	var missing []string
	for _, st := range streams {
		if s == nil || !s.Work.HasRow(st) || s.Work.Hidden(st) {
			missing = append(missing, st)
			continue
		}
		allow[st] = true
	}
	if len(missing) > 0 {
		return nil, "no stream " + strings.Join(missing, ", ") + "; nothing was changed"
	}
	return allow, ""
}

// repoCards is the open cards whose brief names repo, in stream and work order.
func repoCards(s *Snapshot, r RepoAct) []*Card {
	allow, _ := namedStreams(s, r.Streams)
	var out []*Card
	for _, st := range s.Streams() {
		if s.Work.Hidden(st) {
			continue
		}
		if allow != nil && !allow[st] {
			continue
		}
		for _, c := range streamWork(s, st) {
			if !IsOpen(c.Col) {
				continue
			}
			if CardStreamRepo(c.F("brief")) == r.Repo {
				out = append(out, c)
			}
		}
	}
	return out
}

// repoStreams is the drawn streams that have an open card naming repo, in row order.
func repoStreams(s *Snapshot, r RepoAct) []string {
	allow, _ := namedStreams(s, r.Streams)
	var names []string
	seen := map[string]bool{}
	for _, c := range repoCards(s, r) {
		if allow != nil && !allow[c.Row] {
			continue
		}
		if seen[c.Row] {
			continue
		}
		seen[c.Row] = true
		names = append(names, c.Row)
	}
	return names
}

// streamWork is the stream's placed work cards in column order, then score, then id.
func streamWork(s *Snapshot, stream string) []*Card {
	var out []*Card
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := slices.Index(States, out[i].Col), slices.Index(States, out[j].Col)
		if si != sj {
			return si < sj
		}
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func addName(dst *[]string, seen map[string]bool, name string) {
	if name == "" || seen[name] {
		return
	}
	seen[name] = true
	*dst = append(*dst, name)
}

// streamFinding says the stream names more than one repository or base.
func streamFinding(stream string, repos, bases []string) string {
	var parts []string
	if len(repos) > 1 {
		parts = append(parts, "more than one repository: "+strings.Join(repos, ", "))
	}
	if len(bases) > 1 {
		parts = append(parts, "more than one base: "+strings.Join(bases, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "stream " + stream + " names " + strings.Join(parts, "; ")
}

// taskTitle is the first sentence of THE TASK, the title a listing prints.
func taskTitle(brief string) string {
	const mark = "THE TASK"
	i := strings.Index(brief, mark)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(brief[i+len(mark):])
	rest = strings.TrimLeft(rest, ".:")
	rest = strings.TrimSpace(rest)
	if n := strings.IndexByte(rest, '\n'); n >= 0 {
		rest = strings.TrimSpace(rest[:n])
	}
	if n := strings.Index(rest, ". "); n >= 0 {
		return rest[:n+1]
	}
	return rest
}
