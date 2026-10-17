package sprint

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
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

// ReleaseAcceptance is the evidence available to the six stream-acceptance
// checks. Commands which gather git or documentation facts must retain the
// observed revision and raw result; an empty value is missing evidence, never
// an implicit pass.
type ReleaseAcceptance interface {
	Acceptance() AcceptanceSnapshot
}

// AcceptanceSnapshot is a read-only snapshot of the store and evidence
// gathered from the repositories named by it.
type AcceptanceSnapshot struct {
	Work, Readers, Merge *Table
	Open                 []Open
	Lines                []Line
	Streams              []string
	Coordinator          string
	LastLandings         map[string]string
	TreeGates            map[string]TreeGateEvidence
	Docs                 map[string]DocsEvidence
	Dev                  map[string]DevEvidence
}

// TreeGateEvidence records the tree and test classes actually run. A class
// entry is evidence only when its revision and command result are present.
type TreeGateEvidence struct {
	Revision         string
	Manifest         CommandEvidence // successful `go list ./...` output at Revision
	Unit, Functional []CommandEvidence
}

// CommandEvidence is an observed command result tied to a revision.
type CommandEvidence struct {
	Revision string
	Command  string
	Packages []string
	Result   string
	Output   string
}

// DocsEvidence records nova-check results against the final source revision.
type DocsEvidence struct {
	Revision      string
	Links, NoCode CommandEvidence
	Reviews       []DocumentReviewEvidence
}

// DocumentReviewEvidence is a revision-bound human or automated review record
// for prose and help properties with no existing nova-check verb.
type DocumentReviewEvidence struct {
	Stream   string
	Revision string
	Reviewer string
	Scope    string
	Findings []string
}

// DevEvidence carries the observed dev ancestry and queued promotions.
type DevEvidence struct {
	Revision         string
	Ancestors        []string
	QueuedPromotions []PromotionEvidence
}

// PromotionEvidence names the landing heads a queued promotion carries.
type PromotionEvidence struct {
	ID       string
	Landings []string
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
	{CheckStreamTerminal, "every stream card is landed or dropped with a reason", CheckStreamTerminalResult},
	{CheckStreamTreeGate, "unit and functional tree gates pass at the last landing", CheckStreamTreeGateResult},
	{CheckStreamReads, "each landed card has two final-head OK reads, or one for flash", CheckStreamReadsResult},
	{CheckStreamDocs, "nova-check links and no-code pass; stream text is present tense and names no people or machines", CheckStreamDocsResult},
	{CheckStreamDev, "landings are in dev or carried by a queued promotion", CheckStreamDevResult},
	{CheckStreamJudgments, "no open judgment names the stream", CheckStreamJudgmentsResult},
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

// ReleaseStreamNames selects declared stream rows with the same glob contract
// as ReleaseStreamLines. An empty result is meaningful to the caller and must
// not be treated as a successful empty release.
func ReleaseStreamNames(rows []string, glob string) ([]string, error) {
	if glob == "" {
		return append([]string(nil), rows...), nil
	}
	if _, err := path.Match(glob, ""); err != nil {
		return nil, fmt.Errorf("--streams %q is not a glob: %v", glob, err)
	}
	var out []string
	for _, row := range rows {
		if ok, _ := path.Match(glob, row); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// CheckNoStuckFriend is the name of the first check.
const CheckNoStuckFriend = "no-stuck-friend"

const (
	CheckStreamTerminal  = "stream-terminal-cards"
	CheckStreamTreeGate  = "stream-tree-gate"
	CheckStreamReads     = "stream-final-head-reads"
	CheckStreamDocs      = "stream-docs"
	CheckStreamDev       = "stream-dev-landings"
	CheckStreamJudgments = "stream-open-judgments"
)

func acceptance(f ReleaseFacts, name string) (AcceptanceSnapshot, ReleaseResult) {
	a, ok := f.(ReleaseAcceptance)
	if !ok {
		return AcceptanceSnapshot{}, ReleaseResult{Name: name, Evidence: "acceptance snapshot unavailable; inspect nova-sprint release check evidence collection"}
	}
	s := a.Acceptance()
	if s.Work == nil || s.Readers == nil || s.Merge == nil {
		return AcceptanceSnapshot{}, ReleaseResult{Name: name, Evidence: "acceptance snapshot is incomplete (work, readers, or merge rows missing); inspect nova-sprint release check evidence collection"}
	}
	return s, ReleaseResult{Name: name}
}

func cardStream(c *Card) string {
	if c == nil {
		return ""
	}
	if st := c.F("stream"); st != "" {
		return st
	}
	return c.Row
}

func selectedStream(streams []string, stream string) bool {
	for _, st := range streams {
		if st == stream {
			return true
		}
	}
	return false
}

func CheckStreamTerminalResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamTerminal)
	if r.Evidence != "" {
		return r
	}
	var bad []string
	streams := map[string]bool{}
	for _, st := range s.Streams {
		streams[st] = true
	}
	if len(streams) == 0 {
		return ReleaseResult{Name: CheckStreamTerminal, Evidence: "no stream scope was captured; inspect nova-sprint release check stream selection"}
	}
	total := 0
	for _, c := range s.Work.Cards() {
		stream := c.F("stream")
		if stream == "" {
			stream = c.Row
		}
		if c.F("kind") == "control" || !streams[stream] {
			continue
		}
		total++
		if c.Col == Landed || c.F("outcome") == "dropped" && strings.TrimSpace(c.F("reason")) != "" {
			continue
		}
		if c.F("outcome") == "dropped" {
			bad = append(bad, c.ID+" dropped without a reason")
		} else if c.Placed() {
			bad = append(bad, c.ID+" is "+c.Col)
		} else {
			bad = append(bad, c.ID+" has no landed or dropped outcome")
		}
	}
	if len(bad) != 0 {
		return ReleaseResult{Name: CheckStreamTerminal, Evidence: strings.Join(bad, "; ") + "; inspect: nova-sprint card <id> and nova-sprint log --card <id>"}
	}
	return ReleaseResult{Name: CheckStreamTerminal, OK: true, Evidence: fmt.Sprintf("all %d stream cards are landed or dropped with reasons", total)}
}

func checkCommands(e []CommandEvidence, revision string, required []string, functional bool) string {
	seen := map[string]bool{}
	for _, c := range e {
		hasFunctional := strings.Contains(c.Command, "-tags functional") || strings.Contains(c.Command, "-tags=functional")
		if c.Revision == "" || c.Revision != revision || c.Result != "exit=0" || !strings.Contains(c.Command, "go test") || hasFunctional != functional {
			continue
		}
		for _, p := range c.Packages {
			seen[strings.TrimPrefix(path.Clean(p), "./")] = true
		}
	}
	var missing []string
	for _, p := range required {
		if !seen[strings.TrimPrefix(path.Clean(p), "./")] {
			missing = append(missing, p)
		}
	}
	return strings.Join(missing, ", ")
}

func packagesFromPaths(paths, manifest []string) ([]string, string) {
	known := map[string]bool{}
	for _, p := range manifest {
		known[strings.TrimPrefix(path.Clean(p), "./")] = true
	}
	var out []string
	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/**") {
			root := strings.TrimSuffix(p, "/**")
			found := false
			for pkg := range known {
				if pkg == root || strings.HasPrefix(pkg, root+"/") {
					out = append(out, pkg)
					found = true
				}
			}
			if !found {
				return nil, "PATHS glob " + p + " matched no packages in the complete manifest"
			}
			continue
		}
		if path.Ext(p) != ".go" {
			continue
		}
		pkg := path.Dir(p)
		if !known[pkg] {
			return nil, "PATHS entry " + p + " names package " + pkg + ", absent from the complete manifest"
		}
		out = append(out, pkg)
	}
	if len(known) == 0 {
		return nil, "the complete package manifest is missing"
	}
	seenOut := map[string]bool{}
	uniqueOut := out[:0]
	for _, p := range out {
		if !seenOut[p] {
			uniqueOut = append(uniqueOut, p)
			seenOut[p] = true
		}
	}
	sort.Strings(uniqueOut)
	return uniqueOut, ""
}

func CheckStreamTreeGateResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamTreeGate)
	if r.Evidence != "" {
		return r
	}
	for _, stream := range s.Streams {
		landing, gate := s.LastLandings[stream], s.TreeGates[stream]
		if landing == "" || gate.Revision == "" || gate.Revision != landing {
			return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "missing or stale last-landing tree-gate revision for " + stream + "; inspect the stream's last landing and tree-gate receipts"}
		}
		if gate.Manifest.Revision != landing || gate.Manifest.Result != "exit=0" || !strings.Contains(gate.Manifest.Command, "go list ./...") || len(gate.Manifest.Packages) == 0 {
			return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "complete go list ./... package manifest missing, failed or stale for " + stream + " at " + landing}
		}
		manifest := map[string]bool{}
		for _, pkg := range gate.Manifest.Packages {
			manifest[strings.TrimPrefix(path.Clean(pkg), "./")] = true
		}
		if !manifest["internal/docs"] || !manifest["internal/ci"] {
			return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "complete package manifest at " + landing + " is missing internal/docs or internal/ci for " + stream + "; inspect go list output"}
		}
		required := []string{"internal/docs", "internal/ci"}
		for _, c := range s.Work.Cards() {
			if c.Col != Landed || c.F("kind") == "control" || cardStream(c) != stream {
				continue
			}
			paths, ok := cardhdr.Value(c.F("brief"), cardhdr.KeyPaths)
			if !ok {
				return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "landed card " + c.ID + " has no PATHS list for unit and functional gate coverage; inspect its brief and gate receipts"}
			}
			packages, why := packagesFromPaths(strings.Split(paths, ","), gate.Manifest.Packages)
			if why != "" {
				return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "card " + c.ID + " PATHS package coverage is incomplete: " + why + "; inspect the revision's go list manifest and gate receipts"}
			}
			required = append(required, packages...)
		}
		if m := checkCommands(gate.Unit, landing, required, false); m != "" {
			return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "unit tree gate missing, failed, or stale for " + stream + " packages " + m + " at " + landing + "; inspect the tree-gate receipts"}
		}
		if m := checkCommands(gate.Functional, landing, required, true); m != "" {
			return ReleaseResult{Name: CheckStreamTreeGate, Evidence: "functional tree gate missing, failed, or stale for " + stream + " packages " + m + " at " + landing + "; inspect the tree-gate receipts"}
		}
	}
	return ReleaseResult{Name: CheckStreamTreeGate, OK: true, Evidence: fmt.Sprintf("unit and functional gates passed at each selected stream's last landing (%d streams)", len(s.Streams))}
}

func CheckStreamReadsResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamReads)
	if r.Evidence != "" {
		return r
	}
	var bad []string
	for _, c := range s.Work.Cards() {
		if c.Col != Landed || c.F("kind") == "control" || !selectedStream(s.Streams, cardStream(c)) {
			continue
		}
		want := ReadsNeeded(c)
		head := c.F("head")
		if head == "" {
			bad = append(bad, c.ID+" has no final head")
			continue
		}
		readers := map[string]bool{}
		for _, rd := range s.Readers.Cards() {
			if rd.F("primary") == c.ID && rd.Col == OK && rd.F("head") == head && ReadCardAgrees(rd) {
				readers[rd.F("reader")] = true
			}
		}
		if len(readers) < want {
			bad = append(bad, fmt.Sprintf("%s has %d final-head OK reads; needs %d", c.ID, len(readers), want))
		}
	}
	if len(bad) != 0 {
		return ReleaseResult{Name: CheckStreamReads, Evidence: strings.Join(bad, "; ") + "; inspect: nova-sprint card <id> and its read records"}
	}
	return ReleaseResult{Name: CheckStreamReads, OK: true, Evidence: "every landed card has the required distinct OK reads at its final head"}
}

func CheckStreamDocsResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamDocs)
	if r.Evidence != "" {
		return r
	}
	for _, stream := range s.Streams {
		docs := s.Docs[stream]
		landing := s.LastLandings[stream]
		if landing == "" || docs.Revision == "" || docs.Revision != landing {
			return ReleaseResult{Name: CheckStreamDocs, Evidence: "missing or stale documentation evidence for " + stream + " at its last landing " + landing + "; inspect the stream's nova-check results and review records"}
		}
		checks := []struct {
			name, verb string
			e          CommandEvidence
		}{{"nova-check links", "links", docs.Links}, {"nova-check no-code", "nocode", docs.NoCode}}
		var bad []string
		for _, c := range checks {
			if c.e.Revision == "" || c.e.Revision != docs.Revision || c.e.Result != "exit=0" || !strings.Contains(c.e.Command, "nova-check "+c.verb) {
				bad = append(bad, c.name)
			}
		}
		for _, scope := range []string{"present-tense", "no-people-or-machine-names", "help-matches-code"} {
			ok := false
			for _, review := range docs.Reviews {
				if review.Stream == stream && review.Revision == landing && review.Reviewer != "" && review.Scope == scope && len(review.Findings) == 0 {
					ok = true
					break
				}
			}
			if !ok {
				bad = append(bad, scope+" review")
			}
		}
		if len(bad) != 0 {
			return ReleaseResult{Name: CheckStreamDocs, Evidence: "missing, failed, or stale checks for " + stream + ": " + strings.Join(bad, ", ") + "; inspect stream help/spec and nova-check receipts"}
		}
	}
	return ReleaseResult{Name: CheckStreamDocs, OK: true, Evidence: fmt.Sprintf("document checks and revision-bound reviews passed for %d streams", len(s.Streams))}
}

func CheckStreamDevResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamDev)
	if r.Evidence != "" {
		return r
	}
	for _, stream := range s.Streams {
		landing, dev := s.LastLandings[stream], s.Dev[stream]
		if landing == "" || dev.Revision == "" {
			return ReleaseResult{Name: CheckStreamDev, Evidence: "missing last-landing or dev revision for " + stream + "; inspect stream landings and dev promotion queue"}
		}
		ancestors := map[string]bool{}
		for _, sha := range dev.Ancestors {
			ancestors[sha] = true
		}
		if ancestors[landing] {
			continue
		}
		carried := false
		for _, p := range dev.QueuedPromotions {
			for _, sha := range p.Landings {
				if sha == landing {
					carried = true
					break
				}
			}
			if carried {
				break
			}
		}
		if !carried {
			return ReleaseResult{Name: CheckStreamDev, Evidence: "last landing " + landing + " for " + stream + " is not in dev and no promotion carrying it is queued; inspect dev ancestry and promotion rows"}
		}
	}
	return ReleaseResult{Name: CheckStreamDev, OK: true, Evidence: fmt.Sprintf("each selected stream's last landing is in dev or a queued promotion (%d streams)", len(s.Streams))}
}

func CheckStreamJudgmentsResult(f ReleaseFacts) ReleaseResult {
	s, r := acceptance(f, CheckStreamJudgments)
	if r.Evidence != "" {
		return r
	}
	var bad []string
	for _, o := range s.Open {
		if o.Note.Stream != "" && selectedStream(s.Streams, o.Note.Stream) {
			bad = append(bad, o.Note.ID+" ("+o.Note.Stream+")")
		}
	}
	if len(bad) != 0 {
		return ReleaseResult{Name: CheckStreamJudgments, Evidence: "open judgments remain: " + strings.Join(bad, ", ") + "; inspect: nova-sprint inbox --open"}
	}
	return ReleaseResult{Name: CheckStreamJudgments, OK: true, Evidence: "no open judgment names a stream"}
}

// StuckWindow is how far back no friend may have been stuck.
const StuckWindow = 4 * time.Hour

// NoStuckFriend: no friend was stuck at any moment of the last StuckWindow.
// Stuck is the deadline rule's own lateness (WorkDeadline, docs/SPEC-SPRINT.md
// section 1, a friend's card's deadline; the tick's N4 judgment): a friend held
// a working card past its deadline (FieldFriendDeadline, else DeadlineUnfinished,
// counted from the card's first take, a take-back counting), or was dealt a card
// and did not take it past the dealt bound (ReleaseFacts.DealtMax). A friend
// held down has the unstarted cards taken back (FriendTake), so a ready card
// on the friend's row is a card of a friend who is up. The spells are replayed from the
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
	friend string
	card   *Card
	since  time.Time
}

const (
	stampUntakenSince = "untaken_since"
	stampFirstTaken   = "first_taken"
	stampTaken        = "taken"
	stampFirstDealt   = "first_dealt"
	stampDealt        = "dealt"
)

// friendLateSpans replays the log's fleet moves onto the friends' rows and
// returns every stretch a card was past its deadline, and how many friend
// cards the log held.
func friendLateSpans(lines []Line, now time.Time, dealtMax time.Duration) (spans []lateSpan, cards int) {
	state := map[string]*friendCard{}
	seenCards := map[string]bool{}
	snap := &Snapshot{Work: NewTable(Work)}
	if dealtMax > 0 {
		snap.Work.SetProp(PropDealtMax, dealtMax.String())
	}
	checkSpan := func(id string, c *friendCard, end time.Time) {
		if c == nil || c.card == nil {
			return
		}
		field, limit, word, _ := WorkDeadline(snap, c.card)
		var base time.Time
		if s := c.card.F(field); s != "" {
			base, _ = time.Parse(time.RFC3339, s)
		}
		if base.IsZero() {
			base = c.since
		}
		dl := base.Add(limit)
		if end.After(dl) {
			from := dl
			if from.Before(c.since) {
				from = c.since
			}
			for i := len(spans) - 1; i >= 0; i-- {
				if spans[i].card == id {
					if spans[i].word == word && (spans[i].to.Equal(from) || spans[i].to.After(from)) {
						if end.After(spans[i].to) {
							spans[i].to = end
						}
						return
					}
					break
				}
			}
			spans = append(spans, lateSpan{
				friend: c.friend,
				card:   id,
				word:   word,
				limit:  limit,
				from:   from,
				to:     end,
			})
		}
	}
	var currentEpoch uint64
	var haveEpoch bool
	for _, l := range lines {
		if haveEpoch && l.Epoch > currentEpoch {
			for id, c := range state {
				checkSpan(id, c, l.At)
			}
			clear(state)
			currentEpoch = l.Epoch
		} else if !haveEpoch && l.Epoch > 0 {
			currentEpoch = l.Epoch
			haveEpoch = true
		}
		if l.Verb == "clear" || (l.Note != nil && l.Note.Type == NMachineStopped && strings.Contains(l.Note.What, "clear")) {
			for id, c := range state {
				checkSpan(id, c, l.At)
			}
			clear(state)
			continue
		}
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
				checkSpan(id, c, l.At)
				delete(state, id)
				c = nil
			}
			if l.Removed || !placed || !isFriend || (col != Ready && col != Working && col != Withdrawn) {
				continue
			}
			if c == nil {
				c = &friendCard{
					friend: friend,
					card:   &Card{ID: id, Row: row, Fields: map[string]string{}},
					since:  l.At,
				}
				state[id] = c
				if !seenCards[id] {
					seenCards[id] = true
					cards++
				}
			} else {
				checkSpan(id, c, l.At)
				c.since = l.At
			}
			c.friend = friend
			c.card.Row = row
			c.card.Col = col
			for k, v := range l.Set {
				c.card.Fields[k] = v
			}
			switch col {
			case Withdrawn:
				delete(c.card.Fields, stampFirstTaken)
				delete(c.card.Fields, stampTaken)
				delete(c.card.Fields, stampDealt)
				if c.card.F(stampUntakenSince) == "" {
					c.card.Fields[stampUntakenSince] = stamp(l.At)
				}
			case Working:
				delete(c.card.Fields, stampUntakenSince)
				if c.card.F(stampFirstTaken) == "" {
					c.card.Fields[stampFirstTaken] = stamp(l.At)
				}
				if c.card.F(stampTaken) == "" {
					c.card.Fields[stampTaken] = stamp(l.At)
				}
			case Ready:
				if c.card.F(stampFirstDealt) == "" {
					c.card.Fields[stampFirstDealt] = stamp(l.At)
				}
				if c.card.F(stampDealt) == "" {
					c.card.Fields[stampDealt] = stamp(l.At)
				}
				if c.card.F(stampUntakenSince) == "" && c.card.F(stampFirstTaken) == "" {
					c.card.Fields[stampUntakenSince] = stamp(l.At)
				}
			}
		}
	}
	for id, c := range state {
		checkSpan(id, c, now)
	}
	return spans, cards
}
