package sprint

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The release gate's frame (docs/SPEC-SPRINT.md, the release check, card
// release-check-frame; docs/SPEC-RELEASE.md, "release check"): a registry of
// checks, each a pure function over a ReleaseFacts, so a release ships when
// the tool says so and never when someone feels it is done. A check reads the
// facts and nothing else (no socket, no clock of its own), says ok or fail
// with the evidence, and on a fail the evidence names what to look at. The
// verb `nova-sprint release check` runs the registry and writes nothing.

// ReleaseFacts is everything a check may read: the clock, the store's log and
// the sprint's dealt bound. Later checks add the git facts to this interface;
// the unit tests fake it with a struct, and the verb binds it to the store.
type ReleaseFacts interface {
	Now() time.Time
	Log() []Line
	DealtMax() time.Duration
}

// ReleaseResult is one check's answer.
type ReleaseResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Evidence string `json:"evidence"`
}

// Line is the check's one printed line: RELEASE CHECK <name> ok|fail <evidence>.
func (r ReleaseResult) Line() string {
	word := "fail"
	if r.OK {
		word = "ok"
	}
	return "RELEASE CHECK " + r.Name + " " + word + " " + r.Evidence
}

// ReleaseCheck is one entry of the registry: its name, the bar it holds
// (stated in docs/SPEC-RELEASE.md, "release check") and the pure function.
type ReleaseCheck struct {
	Name string
	Bar  string
	Run  func(ReleaseFacts) ReleaseResult
}

// ReleaseChecks is the registry, in the order the verb runs them. A later card
// of stream sprint-v1-release adds its check here and its bar to the spec.
var ReleaseChecks = []ReleaseCheck{
	{CheckNoStuckFriend, "no friend was stuck at any moment of the last " + StuckWindow.String(), NoStuckFriend},
}

// ReleaseReport is the verb's result value: the lines are rendered from it and
// --json is the same value.
type ReleaseReport struct {
	Results []ReleaseResult `json:"results"`
	Checks  int             `json:"checks"`
	Failed  int             `json:"failed"`
	Ready   bool            `json:"ready"`
	Summary string          `json:"summary"`
}

// ExitCode is 0 when every check passed and 1 when one failed; a refusal of
// the arguments is the verb's exit 2.
func (r ReleaseReport) ExitCode() int {
	if r.Ready {
		return 0
	}
	return 1
}

// ReleaseCheckNames is the registry's names, in order.
func ReleaseCheckNames() []string {
	out := make([]string, len(ReleaseChecks))
	for i, c := range ReleaseChecks {
		out[i] = c.Name
	}
	return out
}

// RunReleaseChecks runs the named checks (every check when names is empty), in
// registry order, and summarises: RELEASE OK checks=<n> when all pass,
// RELEASE NOT READY failed=<n> otherwise. A name that is no check is an error
// naming the checks there are, and nothing runs.
func RunReleaseChecks(f ReleaseFacts, names []string) (ReleaseReport, error) {
	want := map[string]bool{}
	for _, n := range names {
		if !hasName(n) {
			return ReleaseReport{}, fmt.Errorf("no release check named %q; the checks are %s", n, strings.Join(ReleaseCheckNames(), ", "))
		}
		want[n] = true
	}
	rep := ReleaseReport{Results: []ReleaseResult{}}
	for _, c := range ReleaseChecks {
		if len(want) > 0 && !want[c.Name] {
			continue
		}
		r := c.Run(f)
		r.Name = c.Name
		if !r.OK {
			rep.Failed++
		}
		rep.Results = append(rep.Results, r)
	}
	rep.Checks = len(rep.Results)
	rep.Ready = rep.Failed == 0
	if rep.Ready {
		rep.Summary = fmt.Sprintf("RELEASE OK checks=%d", rep.Checks)
	} else {
		rep.Summary = fmt.Sprintf("RELEASE NOT READY failed=%d", rep.Failed)
	}
	return rep, nil
}

func hasName(n string) bool {
	for _, c := range ReleaseChecks {
		if c.Name == n {
			return true
		}
	}
	return false
}

// ReleaseStreamLines is the log lines of the streams the glob names (path.Match
// over the stream's name; "" keeps every line). A card's stream is the first
// stream any of its lines names, so a line that names none (a fleet move) is
// kept with its card's.
func ReleaseStreamLines(lines []Line, glob string) ([]Line, error) {
	if glob == "" {
		return lines, nil
	}
	if _, err := path.Match(glob, ""); err != nil {
		return nil, fmt.Errorf("--streams %q is not a glob: %v", glob, err)
	}
	streamOf := map[string]string{}
	for _, l := range lines {
		if l.Stream == "" {
			continue
		}
		for _, id := range l.Names() {
			if _, ok := streamOf[id]; !ok {
				streamOf[id] = l.Stream
			}
		}
	}
	var out []Line
	for _, l := range lines {
		s := l.Stream
		if s == "" {
			s = streamOf[l.Card]
		}
		if ok, _ := path.Match(glob, s); ok {
			out = append(out, l)
		}
	}
	return out, nil
}

// CheckNoStuckFriend is the name of the first check.
const CheckNoStuckFriend = "no-stuck-friend"

// StuckWindow is how far back no friend may have been stuck.
const StuckWindow = 4 * time.Hour

// NoStuckFriend: no friend was stuck at any moment of the last StuckWindow.
// Stuck is the deadline rule's own lateness (WorkDeadline, docs/SPEC-SPRINT.md
// section 1, a friend's card's deadline; the tick's N4 judgment): a friend held
// a working card past its deadline (FieldFriendDeadline, else DeadlineUnfinished,
// counted from the card's first take, a take-back counting), or was dealt a card
// and did not take it past the dealt bound (ReleaseFacts.DealtMax). A friend
// held down has her unstarted cards taken back (FriendTake), so a ready card
// on her row is a card of a friend who is up. The spells are replayed from the
// log's fleet moves alone, so a spell that has ended still counts while it
// overlaps the window.
func NoStuckFriend(f ReleaseFacts) ReleaseResult {
	now := f.Now()
	from := now.Add(-StuckWindow)
	spans, cards := friendLateSpans(f.Log(), now, f.DealtMax())
	var hits []lateSpan
	for _, s := range spans {
		if s.to.After(from) && s.from.Before(now) {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		return ReleaseResult{Name: CheckNoStuckFriend, OK: true,
			Evidence: fmt.Sprintf("no friend was stuck since %s (%d friend cards read from the log)", stamp(from), cards)}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].from.Before(hits[j].from) })
	var told []string
	for i, s := range hits {
		if i == 3 {
			break
		}
		begun := s.from
		if begun.Before(from) {
			begun = from
		}
		primary, _, _ := strings.Cut(s.card, ".")
		told = append(told, fmt.Sprintf("friend %s stuck from %s: %s %s (limit %s); look at: nova-sprint card %s, nova-sprint log --card %s",
			s.friend, stamp(begun), s.card, s.word, s.limit, primary, primary))
	}
	more := ""
	if len(hits) > len(told) {
		more = fmt.Sprintf("; and %d more spells", len(hits)-len(told))
	}
	return ReleaseResult{Name: CheckNoStuckFriend, Evidence: strings.Join(told, "; ") + more}
}

// lateSpan is one stretch of time a friend's card was past its deadline.
type lateSpan struct {
	friend, card, word string
	limit              time.Duration
	from, to           time.Time
}

// friendCard is one friend card's replay state.
type friendCard struct {
	friend       string
	col          string
	untakenSince time.Time // dealt and not taken since; zero once taken
	firstTaken   time.Time
	limit        time.Duration
}

// friendLateSpans replays the log's fleet moves onto the friends' rows and
// returns every stretch a card was past its deadline, and how many friend
// cards the log held.
func friendLateSpans(lines []Line, now time.Time, dealtMax time.Duration) (spans []lateSpan, cards int) {
	state := map[string]*friendCard{}
	closeAt := func(id string, c *friendCard, end time.Time) {
		if !c.untakenSince.IsZero() && end.After(c.untakenSince.Add(dealtMax)) {
			spans = append(spans, lateSpan{c.friend, id, WordNeverTaken, dealtMax, c.untakenSince.Add(dealtMax), end})
		}
		if !c.firstTaken.IsZero() && end.After(c.firstTaken.Add(c.limit)) {
			spans = append(spans, lateSpan{c.friend, id, "not finished", c.limit, c.firstTaken.Add(c.limit), end})
		}
	}
	for _, l := range lines {
		if l.Kind != LineMove || l.Table != Fleet {
			continue
		}
		ids := l.Cards
		if len(ids) == 0 {
			ids = []string{l.Card}
		}
		row, col, placed := strings.Cut(l.To, ":")
		friend, isFriend := FriendOfRow(row)
		for _, id := range ids {
			c := state[id]
			if c != nil && (l.Removed || !placed || !isFriend || c.friend != friend || (col != Ready && col != Working && col != Withdrawn)) {
				closeAt(id, c, l.At)
				delete(state, id)
				c = nil
			}
			if l.Removed || !placed || !isFriend || (col != Ready && col != Working && col != Withdrawn) {
				continue
			}
			if c == nil {
				c = &friendCard{friend: friend, limit: DeadlineUnfinished}
				state[id] = c
				cards++
			}
			if d, err := strconv.Atoi(l.Set[FieldFriendDeadline]); err == nil && d > 0 {
				c.limit = time.Duration(d) * time.Second
			}
			switch col {
			case Ready:
				if c.untakenSince.IsZero() && c.firstTaken.IsZero() {
					c.untakenSince = l.At
				}
			case Working:
				if !c.untakenSince.IsZero() {
					if l.At.After(c.untakenSince.Add(dealtMax)) {
						spans = append(spans, lateSpan{c.friend, id, WordNeverTaken, dealtMax, c.untakenSince.Add(dealtMax), l.At})
					}
					c.untakenSince = time.Time{}
				}
				if c.firstTaken.IsZero() {
					c.firstTaken = l.At
				}
			}
			c.col = col
		}
	}
	for id, c := range state {
		closeAt(id, c, now)
	}
	return spans, cards
}
