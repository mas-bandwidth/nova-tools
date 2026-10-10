package sprint

import (
	"sort"
	"strings"
)

// FieldRepo is a stream's control card field: the repositories its cards name, in the
// order their briefs name them (docs/SPEC-SPRINT.md section 11, the streams verb).
const FieldRepo = "repo"

// FieldBase is a stream's control card field: the bases its cards name, from each
// brief's BASE: line, at admission.
const FieldBase = "base"

// StreamsReq is what a streams listing shows: only the streams recording a repository,
// only the streams of a release, and every card of each stream when Cards.
type StreamsReq struct {
	Repos   []string
	Release string
	Cards   bool
}

// StreamCard is one card of a stream in a streams listing: its state, tier, the first
// sentence of its THE TASK, and the ids it needs.
type StreamCard struct {
	ID    string   `json:"id"`
	State string   `json:"state"`
	Tier  string   `json:"tier,omitempty"`
	Title string   `json:"title,omitempty"`
	Needs []string `json:"needs"`
}

// StreamRow is one stream in a streams listing: the repositories and bases its cards
// record, its release, the state it shows, its open and landed counts, its work ok, failed
// and ok% over its work cards, and its cards when asked.
type StreamRow struct {
	Stream  string       `json:"stream"`
	Repos   []string     `json:"repos"`
	Bases   []string     `json:"bases"`
	Release string       `json:"release,omitempty"`
	State   string       `json:"state,omitempty"`
	Open    int          `json:"open"`
	Landed  int          `json:"landed"`
	OK      int          `json:"ok"`
	Failed  int          `json:"failed"`
	OKPct   float64      `json:"okpct"`
	Cards   []StreamCard `json:"cards,omitempty"`
}

// StreamsView is the whole listing: one row per stream, and the streams whose cards
// name more than one repository (the finding the verb prints).
type StreamsView struct {
	Streams []StreamRow `json:"streams"`
	Mixed   []string    `json:"mixed,omitempty"`
}

// StreamRepos is the repositories a stream's cards name: the union of its control
// card's repo field (every add records there) and the cards placed in its row.
func StreamRepos(s *Snapshot, stream string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		for _, r := range Split(v) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	if ctl := s.StreamCtl(stream); ctl != nil {
		add(ctl.F(FieldRepo))
	}
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream {
			add(c.F(FieldRepo))
		}
	}
	sort.Strings(out)
	return out
}

// StreamBases is the bases a stream's cards name, as StreamRepos reads the repositories.
func StreamBasesOf(s *Snapshot, stream string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		for _, b := range Split(v) {
			if !seen[b] {
				seen[b] = true
				out = append(out, b)
			}
		}
	}
	if ctl := s.StreamCtl(stream); ctl != nil {
		add(ctl.F(FieldBase))
	}
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream {
			add(c.F(FieldBase))
		}
	}
	sort.Strings(out)
	return out
}

// StreamTitle is the first sentence of a brief's THE TASK: the text after `THE TASK.`
// or `THE TASK:` at the start of a line, to the first `. ` and without its final dot.
// "" when the brief names no task.
func StreamTitle(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(t, "THE TASK.")
		if !ok {
			rest, ok = strings.CutPrefix(t, "THE TASK:")
		}
		if !ok {
			continue
		}
		title := strings.TrimSpace(rest)
		if i := strings.Index(title, ". "); i >= 0 {
			title = title[:i]
		}
		return strings.TrimSpace(strings.TrimSuffix(title, "."))
	}
	return ""
}

// StreamStats is every stream's counters over the fleet table's work cards,
// grouped by the card's stream field: the same WorkerStats definition the
// friends and fleet rows use, so a stream's ok% is landed-or-ok over the
// attempts a worker actually ran to an end, launch refusals, provider failures
// and withdrawals left out. A snapshot with no fleet table has no counters.
func StreamStats(s *Snapshot) map[string]WorkerCounters {
	if s == nil || s.Fleet == nil {
		return nil
	}
	return WorkerStatsBy(s.Fleet.Column(Ready, Working, DoneOK, DoneFailed, DoneDefect, Withdrawn, Refused, Provider), "stream")
}

// StreamsOf is the streams listing: every stream row, with the repositories and bases
// its cards record, its release, its open and landed counts and, with Cards, every card
// in work order (a state at a time). Only keeps the streams a repository in Repos
// records (any of them) and, with Release set, the streams of that release. Mixed names
// every stream whose cards name more than one repository.
func StreamsOf(s *Snapshot, r StreamsReq) StreamsView {
	var v StreamsView
	work := StreamStats(s)
	for _, stream := range s.Work.Rows() {
		repos := StreamRepos(s, stream)
		if len(r.Repos) > 0 && !anyRepo(r.Repos, repos) {
			continue
		}
		var release string
		var fields map[string]string
		if ctl := s.StreamCtl(stream); ctl != nil {
			release = ctl.F(FieldRelease)
			fields = ctl.Fields
		}
		if r.Release != "" && release != r.Release {
			continue
		}
		row := StreamRow{Stream: stream, Repos: repos, Bases: StreamBasesOf(s, stream), Release: release}
		if w, ok := work[stream]; ok {
			row.OK, row.Failed, row.OKPct = w.OK, w.Failed, w.OKPct
		}
		for _, col := range States {
			for _, c := range s.Work.Cell(stream, col) {
				if IsOpen(col) {
					row.Open++
				} else {
					row.Landed++
				}
				if r.Cards {
					row.Cards = append(row.Cards, StreamCard{ID: c.ID, State: col, Tier: c.F(FieldTier),
						Title: StreamTitle(c.F("brief")), Needs: Split(c.F("needs"))})
				}
			}
		}
		row.State = ShownStreamState(fields, row.Open)
		if len(repos) > 1 {
			v.Mixed = append(v.Mixed, stream)
		}
		v.Streams = append(v.Streams, row)
	}
	sort.Strings(v.Mixed)
	return v
}

// anyRepo says one of the wanted repositories is one of the repositories recorded.
func anyRepo(want, have []string) bool {
	for _, w := range want {
		for _, h := range have {
			if RepoName(w) == RepoName(h) {
				return true
			}
		}
	}
	return false
}

// repoName is a repository as the owner/name a --repo flag names: a URL or a clone
// path's last two path segments, without a .git suffix; a value already owner/name
// unchanged.
func RepoName(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, ".git")
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	} else if i := strings.Index(v, "@"); i >= 0 && strings.Contains(v, ":") {
		v = v[i+1:]
		if j := strings.Index(v, ":"); j >= 0 {
			v = v[j+1:]
		}
	}
	parts := strings.Split(strings.Trim(v, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return v
}
