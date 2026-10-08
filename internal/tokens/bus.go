package tokens

import (
	"context"
	"maps"
	pathpkg "path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// --bus <host:port>: friends' self-reports, read from the Redis bus's log (docs/SPEC-TOKENS.md rule 6 and docs/SPEC-BUS.md:
// bus2:log holds every message once, with its sender, subject and body).
//
// The tool reads the log and nothing else. It sends nothing, acks nothing, creates no group
// and writes no key: a fold whose numbers depended on anything but the messages themselves
// would be a fold nobody could reproduce, and the order of two competing notes is in the
// notes themselves (`supersedes=`), not in the log's order, which is a fact about when the
// store took a message and not about which number the friend meant.
//
// THE PARSER BELOW IS THE SERIALIZER `report` WRITES WITH. They live in one file so that
// one grammar cannot become two, which is exactly how the prototype ended up accepting
// two body shapes and telling them apart by whether the fifth field was a word.

// SubjectPrefix opens every tokens note's subject, exactly: lower case, one blank.
const SubjectPrefix = "tokens "

// reposComment is the one comment shape a body may carry meaning, and it carries no number.
var reposComment = regexp.MustCompile(`^# repos: ([a-z0-9._-]+(?:,[ ]*[a-z0-9._-]+)*)[ ]*$`)

// noteIDShape is what an id looks like: the bus's ULID, 26 characters of Crockford base32
// (docs/SPEC-BUS.md, the data).
var noteIDShape = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// countShape is a body line's count: a decimal integer, with the person's optional rough mark.
var countShape = regexp.MustCompile(`^~?[0-9]+$`)

// ValidNoteID reports whether an id has the shape the bus assigns (a ULID). `report --supersedes`
// checks the shape and nothing else, because the log is not read by a local report.
func ValidNoteID(id string) bool { return noteIDShape.MatchString(id) }

// Subject renders a tokens note's subject: the date, and the tool's one trailer, which is
// where the stamp, the build id and a correction's predecessor set travel.
func Subject(day, at, build string, supersedes []string) string {
	s := SubjectPrefix + day + " at=" + at + " build=" + build
	if len(supersedes) > 0 {
		s += " supersedes=" + strings.Join(supersedes, ",")
	}
	return s
}

// BodyLine renders one body line, which is what fold --bus parses. Every stored field goes
// through oneline.Field for the reason the day file's cells do: six tab-separated fields
// is a promise, and a model id holding a tab would make it seven.
func BodyLine(day, who, model, repo string, t Type, count int64, basis string) string {
	line := strings.Join([]string{oneline.Field(day), oneline.Field(who), oneline.Field(model),
		oneline.Field(repo), TypeNames[t], strconv.FormatInt(count, 10)}, "\t")
	if basis != UTC && basis != "" {
		line += "\tday_basis=" + oneline.Field(basis)
	}
	return line
}

// parsedSubject is a subject that IS a tokens note's.
type parsedSubject struct {
	day        string
	at         string
	build      string
	supersedes []string
	badSet     string // why the predecessor set is refused, empty when it is not
}

// ParseSubject reads a tokens note's subject. The second return is false when the subject
// is not a tokens note's at all — a different case from a tokens note whose trailer is
// wrong, which is a note refused by name.
func ParseSubject(subject string) (parsedSubject, bool) {
	rest, ok := strings.CutPrefix(subject, SubjectPrefix)
	if !ok {
		return parsedSubject{}, false
	}
	day := rest
	trailer := ""
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		day, trailer = rest[:i], rest[i+1:]
	}
	if !ValidDay(day) {
		return parsedSubject{}, false
	}
	p := parsedSubject{day: day}
	if trailer == "" {
		return p, true
	}
	// The trailer is ONE shape in ONE order: `at=<stamp> build=<id>[ supersedes=<set>]`.
	// Accepting keys in any order or position produced invalid tokens notes.
	toks := strings.Split(trailer, " ")
	keys := []string{"at", "build", "supersedes"}
	for i, tok := range toks {
		if i >= len(keys) {
			return parsedSubject{}, false
		}
		k, _, ok := strings.Cut(tok, "=")
		if !ok || k != keys[i] {
			return parsedSubject{}, false
		}
	}
	for _, tok := range toks {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			return parsedSubject{}, false
		}
		switch k {
		case "at":
			p.at = v
		case "build":
			p.build = v
		case "supersedes":
			ids := strings.Split(v, ",")
			seen := map[string]bool{}
			for i, id := range ids {
				if !ValidNoteID(id) {
					p.badSet = "the predecessor " + id + " is not a bus id (a 26-character ULID)"
				} else if seen[id] {
					p.badSet = "the predecessor set names " + id + " twice; it is a set, sorted ascending, with no duplicate"
				} else if i > 0 && ids[i-1] > id {
					p.badSet = "the predecessor set is not sorted ascending: " + v
				}
				seen[id] = true
			}
			p.supersedes = ids
		default:
			return parsedSubject{}, false
		}
	}
	if p.at == "" || p.build == "" {
		return parsedSubject{}, false
	}
	// The trailer must match `at=<RFC 3339 UTC> build=<id>`.
	if t, err := time.Parse(time.RFC3339, p.at); err != nil || !strings.HasSuffix(p.at, "Z") || !t.Equal(t.UTC()) {
		return parsedSubject{}, false
	}
	return p, true
}

// nearMissSubject says whether a subject that ParseSubject refused was MEANT as a tokens
// note, and why it is not one. The test is deliberately narrow: the first word is `tokens`
// in any case and the second is a day.
func nearMissSubject(subject string) (string, bool) {
	f := strings.Fields(subject)
	if len(f) < 2 || !strings.EqualFold(f[0], "tokens") || !ValidDay(f[1]) {
		return "", false
	}
	return "the subject names tokens and a day and is not the exact shape, so nothing in this note was folded: " + subject, true
}

// note is one tokens note on the way to being folded.
type note struct {
	lane, id string
	subject  parsedSubject
	dead     *Unparsed // the whole note is refused; no line of it folds
	lineErrs []Unparsed
	msgs     []Message
	rough    int
	comments int
	redated  int
	touched  []string
	zones    map[string]bool
}

// noteKey binds an id to its lane so another sender cannot plant an id that changes the
// owner's correction chain (security#75 finding 3).
type noteKey struct{ lane, id string }

// clean is a note validated WHOLE — subject, at, every body line — which is what a
// predecessor must be and what a successor must be before it replaces anything.
func (n *note) clean() bool { return n.dead == nil && len(n.lineErrs) == 0 }

// logPage is the most one read of the log asks for; the log is read in pages from the
// entry after the last one seen, so no message is dropped for the log being long.
const logPage = 10000

// ReadBus reads the bus's log through b and returns ONE source per lane, because the label
// of a self-report is the sender's name whatever the `who` field of a line inside it says.
// A store that does not answer is one unreadable source, never a short log. path is the
// store's address, shown as the sources' path (SPEC-TOKENS.md rule 6).
func ReadBus(ctx context.Context, b *bus.Bus, path string, rules *Rules, at time.Time) []*Source {
	fail := func(err error) []*Source {
		s := &Source{Label: KindBus, Kind: KindBus, Path: path, Reports: nil, Basis: UTC}
		s.unreadable(path+"/"+bus.LogKey, err.Error())
		return []*Source{s}
	}
	roster, err := b.Names(ctx)
	if err != nil {
		return fail(err)
	}
	var entries []bus.Entry
	for from := "-"; ; {
		page, err := b.Log(ctx, from)
		if err != nil {
			return fail(err)
		}
		if len(page) == 0 {
			break
		}
		entries = append(entries, page...)
		from = "(" + page[len(page)-1].Entry
	}
	return FoldBus(path, roster, entries, rules, at)
}

// FoldBus is the fold over the log's entries, apart from the store that holds them (SPEC-TOKENS.md rule 6). The
// lanes are the roster's names and every sender the log shows, sorted, each one source.
func FoldBus(path string, roster []string, entries []bus.Entry, rules *Rules, at time.Time) []*Source {
	// Every tokens note on the bus, first, because a successor may name a note in
	// another lane or for another day and the refusal has to be able to say which.
	names := map[string]bool{}
	for _, name := range roster {
		names[name] = true
	}
	for _, e := range entries {
		if from := e.Message().From; from != "" {
			names[from] = true
		}
	}
	lanes := slices.Sorted(maps.Keys(names))
	all := map[noteKey]*note{}
	byLane := map[string][]*note{}
	sources := map[string]*Source{}
	for _, name := range lanes {
		sources[name] = &Source{Label: Label(KindBus, name), Kind: KindBus, Path: pathpkg.Join(path, "from-"+name), Basis: UTC}
	}
	for _, e := range entries {
		m := e.Message()
		if m.From == "" {
			continue
		}
		// files= is what this lane OPENED, tokens note or not: a lane of near-miss
		// subjects printed byte-identical output to a lane holding nothing at all
		// (lesson 30). The near miss itself comes back from readNote as a dead note
		// and is counted and printed like any other unparsed one; a message that is
		// simply another piece of the lane's traffic is nil here and is only counted.
		sources[m.From].Stat.Files++
		n := readNote(m.From, e.Entry, m, rules, at)
		if n == nil {
			continue
		}
		all[noteKey{lane: m.From, id: n.id}] = n
		byLane[m.From] = append(byLane[m.From], n)
	}

	for _, name := range lanes {
		foldLane(sources[name], name, byLane[name], all)
	}

	out := make([]*Source, 0, len(lanes))
	for _, name := range lanes {
		out = append(out, sources[name])
	}
	return out
}

// readNote parses one message. It returns nil when the message is not a tokens note at all —
// the subject is the whole test, exact, because the prototype's case-insensitive match
// with any text after the date folded notes nobody meant as a report. entry is the message's
// id in the log stream, which names it when its own id field is empty.
func readNote(lane, entry string, m bus.Message, rules *Rules, at time.Time) *note {
	subject, ok := ParseSubject(m.Subject)
	label := Label(KindBus, lane)
	id := m.ID
	if id == "" {
		id = entry
	}
	if !ok {
		why, near := nearMissSubject(m.Subject)
		if !near {
			return nil
		}
		// A NEAR MISS is not an ordinary message of the lane: it names tokens and a day, so
		// a friend meant it as a report. Counting it silently made the lane print the
		// same bytes as a lane holding nothing at all (lesson 30), so it is an unparsed
		// note with its id -- counted, printed, exit 1 -- and it still folds nothing.
		n := &note{lane: lane, id: id, zones: map[string]bool{}}
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: why,
			Remedy: "a note's subject named tokens and a day but is not the exact shape (" + n.id +
				"): it is `" + SubjectPrefix + "YYYY-MM-DD`, with nothing after it or with `at=<RFC 3339 UTC> build=<id>` -- " +
				"nova-tokens report --who <you> --day <day> --note <file> writes one"}
		return n
	}
	n := &note{lane: lane, id: id, subject: subject, zones: map[string]bool{}}

	// The message's at (the store's time, to the second) is validated and never used for
	// order. A correction with a bad stamp is refused whole rather than half-read.
	switch {
	case m.At.IsZero():
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: "no at stamp; a tokens note wants the RFC 3339 instant the bus wrote"}
	case m.At.After(at):
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: "the message's at is later than this fold's own at= stamp: " + m.At.UTC().Format(time.RFC3339)}
	}
	if subject.badSet != "" && n.dead == nil {
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: subject.badSet + "; send a correction whose subject carries supersedes=<id>"}
	}
	if n.dead != nil {
		return n
	}
	// The body's lines are numbered from 1; a refusal of the whole note is line 0.
	parseBody(n, label, strings.Split(strings.ReplaceAll(strings.TrimPrefix(m.Body, "\ufeff"), "\r\n", "\n"), "\n"), 0, rules)
	return n
}

// parseBody reads the note's lines: six tab-separated fields with an optional seventh, a
// blank, or a `#` comment of which one shape means something. Anything else is one
// TOKENS UNPARSED line naming the note and the line number in the file.
func parseBody(n *note, label string, body []string, offset int, rules *Rules) {
	for i, raw := range body {
		lineNo := offset + i + 1
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			n.comments++
			if m := reposComment.FindStringSubmatch(line); m != nil {
				var repos []string
				for _, name := range strings.Split(m[1], ",") {
					repos = append(repos, strings.TrimSpace(name))
				}
				n.touched = append(n.touched, repos...)
			}
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 6 && len(f) != 7 {
			n.lineErrs = append(n.lineErrs, Unparsed{Label: label, Note: n.id, Line: lineNo, Text: line})
			continue
		}
		basis := UTC
		if len(f) == 7 {
			zone, ok := strings.CutPrefix(f[6], "day_basis=")
			if !ok || !ValidZone(zone) {
				n.lineErrs = append(n.lineErrs, Unparsed{Label: label, Note: n.id, Line: lineNo, Text: line})
				continue
			}
			basis = zone
		}
		day, model, repo, typeName, count := f[0], f[2], f[3], f[4], f[5]
		t, known := TypeByName(typeName)
		if !ValidDay(day) || model == "" || repo == "" || !known || !countShape.MatchString(count) {
			n.lineErrs = append(n.lineErrs, Unparsed{Label: label, Note: n.id, Line: lineNo, Text: line})
			continue
		}
		rough := 0
		if strings.HasPrefix(count, "~") {
			rough = 1
			count = count[1:]
		}
		v, err := strconv.ParseInt(count, 10, 64)
		if err != nil {
			n.lineErrs = append(n.lineErrs, Unparsed{Label: label, Note: n.id, Line: lineNo, Text: line})
			continue
		}
		if day != n.subject.day {
			n.redated++
		}
		n.zones[basis] = true
		rules.SetDay(day)
		m := Message{Day: day, Basis: basis, Model: model, Repo: rules.Attribute([]string{repo}, ""), Rough: rough}
		m.Counts.Set(t, v)
		n.msgs = append(n.msgs, m)
		n.rough += rough
	}
}

// foldLane resolves one lane's notes: the predecessor sets, the chains, the tips, and the
// one conflict that folds nothing.
func foldLane(s *Source, lane string, notes []*note, all map[noteKey]*note) {
	label := Label(KindBus, lane)

	// Validate every successor's predecessor set, to a fixed point: a successor whose
	// target is itself refused is refused in turn, and the set never partially applies.
	for again := true; again; {
		again = false
		for _, n := range notes {
			if n.dead != nil || len(n.subject.supersedes) == 0 {
				continue
			}
			if why := badPredecessors(n, all); why != "" {
				n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: why + "; send a correction whose subject carries supersedes=<id>"}
				again = true
			}
		}
	}
	// A cycle at any length, through any member, refuses every note on it.
	laneAll := map[string]*note{}
	for _, n := range notes {
		laneAll[n.id] = n
	}
	for _, n := range notes {
		if n.dead == nil && onCycle(n, laneAll, map[string]bool{}) {
			n.dead = &Unparsed{Label: label, Note: n.id, Line: 0,
				Text: "a cycle: this note's predecessor set reaches itself; send a correction whose subject carries supersedes=<id>"}
		}
	}

	// The successor is validated whole before it replaces anything. A note with
	// an unparsed body line was not marked dead, so a half-read correction could
	// replace its predecessor.
	superseded := map[string]string{}
	for _, n := range notes {
		if !n.clean() {
			continue
		}
		for _, id := range n.subject.supersedes {
			superseded[id] = n.id
		}
	}

	// Every basis the lane's LINES carried, utc included. Omitting utc would cause
	// a lane with mixed six-field and seven-field lines to print the zone instead
	// of `mixed`.
	laneZones := map[string]bool{}
	byDay := map[string][]*note{}
	for _, n := range notes {
		byDay[n.subject.day] = append(byDay[n.subject.day], n)
	}
	for _, day := range slices.Sorted(maps.Keys(byDay)) {
		var tips []*note
		for _, n := range byDay[day] {
			if n.dead != nil {
				s.Unparseds = append(s.Unparseds, *n.dead)
				s.Stat.Unparsed++
				continue
			}
			s.Unparseds = append(s.Unparseds, n.lineErrs...)
			s.Stat.Unparsed += len(n.lineErrs)
			s.Stat.Comments += n.comments
			if _, gone := superseded[n.id]; gone {
				continue
			}
			tips = append(tips, n)
		}
		if len(tips) > 1 {
			ids := make([]string, 0, len(tips))
			for _, n := range tips {
				ids = append(ids, n.id)
			}
			sort.Strings(ids)
			s.Conflicts = append(s.Conflicts, Conflict{Label: label, Day: day, Notes: ids})
			continue
		}
		// Only now, with the lane-day settled, is a superseded note reported as one.
		for _, n := range byDay[day] {
			if by, gone := superseded[n.id]; gone {
				s.Supersededs = append(s.Supersededs, Superseded{Label: label, Note: n.id, By: by, Day: day})
				s.Stat.Superseded++
			}
		}
		for _, n := range tips {
			s.Stream = append(s.Stream, n.msgs...)
			s.Stat.Redated += n.redated
			for z := range n.zones {
				laneZones[z] = true
			}
			if len(n.touched) > 0 {
				s.Toucheds = append(s.Toucheds, Touched{Label: label, Day: day, Repos: n.touched})
			}
		}
	}
	s.Basis = laneBasis(laneZones)
	s.Reports = reportedTypes(s.Stream)
}

// laneBasis is the lane's day_basis: `utc` when its lines carried nothing else, the one
// zone when they all carried that one, and `mixed` when they carried more than one. A
// lane is allowed to be mixed across days; a row never is, and that is a separate check.
func laneBasis(zones map[string]bool) string {
	basis := ""
	for z := range zones {
		switch {
		case basis == "":
			basis = z
		case basis != z:
			return "mixed"
		}
	}
	if basis == "" {
		return UTC
	}
	return basis
}

// badPredecessors names why a successor's predecessor set is refused, or returns the empty
// string. Every member is checked before anything is replaced.
func badPredecessors(n *note, all map[noteKey]*note) string {
	for _, id := range n.subject.supersedes {
		p, ok := all[noteKey{lane: n.lane, id: id}]
		switch {
		case !ok:
			var otherLanes []string
			for key, candidate := range all {
				if key.id == id && candidate.lane != n.lane {
					otherLanes = append(otherLanes, candidate.lane)
				}
			}
			if len(otherLanes) > 0 {
				sort.Strings(otherLanes)
				return "the predecessor " + id + " is a note of another lane (from-" + otherLanes[0] + ")"
			}
			return "no such note in this lane for this day: " + id
		case p.subject.day != n.subject.day:
			return "the predecessor " + id + " is a note for another day (" + p.subject.day + ")"
		case !p.clean():
			return "the predecessor " + id + " did not parse"
		}
	}
	return ""
}

// onCycle walks the supersedes graph from n and reports whether it comes back. seen is a
// two-state colour map: true means the node is on the current path, false means it was
// finished with no cycle reachable from it. A node proven acyclic is never re-walked, so a
// diamond is linear rather than exponential in the number of notes; a true on-path hit is
// still the cycle.
func onCycle(n *note, all map[string]*note, seen map[string]bool) bool {
	if state, done := seen[n.id]; done {
		return state
	}
	seen[n.id] = true
	for _, id := range n.subject.supersedes {
		p, ok := all[id]
		if !ok {
			continue
		}
		if onCycle(p, all, seen) {
			return true
		}
	}
	seen[n.id] = false
	return false
}

// reportedTypes is the types a lane's lines actually named, which is what `reports=` is
// for a bus source.
func reportedTypes(stream []Message) []Type {
	var out []Type
	seen := map[Type]bool{}
	for _, m := range stream {
		for t := Type(0); t < NTypes; t++ {
			if _, has := m.Counts.Get(t); has && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sortTypes(out)
	return out
}
