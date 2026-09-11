package tokens

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --bus <dir>: friends' self-reports.
//
// The tool reads the checkout AS FILES. It never pulls, fetches, pushes, runs git, or
// talks to a network: a fold whose numbers depended on a network call would be a fold
// nobody could reproduce, and the order of two competing notes is now in the notes
// themselves (`supersedes=`) rather than in a commit history, which is a fact about a
// checkout and not about which number the friend meant.
//
// THE PARSER BELOW IS THE SERIALIZER `report` WRITES WITH. They live in one file so that
// one grammar cannot become two, which is exactly how the prototype ended up accepting
// two body shapes and telling them apart by whether the fifth field was a word.

// BusDateLayout is how nova-bus writes a note's Date line.
const BusDateLayout = "Mon Jan  2 15:04:05 UTC 2006"

// SubjectPrefix opens every tokens note's subject, exactly: lower case, one space.
const SubjectPrefix = "tokens "

// reposComment is the one comment shape a body may carry meaning, and it carries no number.
var reposComment = regexp.MustCompile(`^# repos: ([a-z0-9._-]+(?:,[ ]*[a-z0-9._-]+)*)[ ]*$`)

// noteIDShape is what an id looks like: nova-bus's <sender>-<12 hex>.
var noteIDShape = regexp.MustCompile(`^[a-z0-9-]+-[0-9a-f]{12}$`)

// countShape is a body line's count: a decimal integer, with the person's optional rough mark.
var countShape = regexp.MustCompile(`^~?[0-9]+$`)

// ValidNoteID reports whether an id has the shape nova-bus assigns. `report --supersedes`
// checks the shape and nothing else, because the lane is not on that machine.
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

// BodyLine renders one body line, which is what fold --bus parses.
func BodyLine(day, who, model, repo string, t Type, count int64, basis string) string {
	line := strings.Join([]string{day, who, model, repo, TypeNames[t], strconv.FormatInt(count, 10)}, "\t")
	if basis != UTC && basis != "" {
		line += "\tday_basis=" + basis
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
	for _, tok := range strings.Split(trailer, " ") {
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
					p.badSet = "the predecessor " + id + " is not <sender>-<12 hex>"
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
	return p, true
}

// note is one tokens note on the way to being folded.
type note struct {
	lane, path, id string
	subject        parsedSubject
	dateLine       int
	dead           *Unparsed // the whole note is refused; no line of it folds
	lineErrs       []Unparsed
	msgs           []Message
	rough          int
	comments       int
	redated        int
	touched        []string
	zones          map[string]bool
}

// clean is a note validated WHOLE — header, Date:, every body line — which is what a
// predecessor must be and what a successor must be before it replaces anything.
func (n *note) clean() bool { return n.dead == nil && len(n.lineErrs) == 0 }

// ReadBus reads every lane the roster names and returns ONE source per lane, because the
// label of a self-report is the lane owner's name whatever the `who` field of a line
// inside it says.
func ReadBus(dir string, rules *Rules, at time.Time) []*Source {
	roster, err := laneNames(dir)
	if err != nil {
		s := &Source{Label: KindBus, Kind: KindBus, Path: dir, Reports: nil, Basis: UTC}
		s.Stat.Files = 0
		s.unreadable(filepath.Join(dir, "participants.json"), err.Error())
		return []*Source{s}
	}

	// Every tokens note on the bus, first, because a successor may name a note in
	// another lane or for another day and the refusal has to be able to say which.
	all := map[string]*note{}
	byLane := map[string][]*note{}
	sources := map[string]*Source{}
	for _, name := range roster {
		s := &Source{Label: Label(KindBus, name), Kind: KindBus, Path: filepath.Join(dir, "from-"+name), Basis: UTC}
		sources[name] = s
		ents, err := os.ReadDir(s.Path)
		if err != nil {
			if !os.IsNotExist(err) {
				s.unreadable(s.Path, err.Error())
			}
			continue
		}
		var files []string
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(s.Path, e.Name()))
			}
		}
		sort.Strings(files)
		for _, path := range files {
			raw, err := readSource(path)
			if err != nil {
				s.unreadable(path, err.Error())
				continue
			}
			n := readNote(name, path, string(raw), rules, at)
			if n == nil {
				continue
			}
			s.Stat.Files++
			all[n.id] = n
			byLane[name] = append(byLane[name], n)
		}
	}

	for _, name := range roster {
		foldLane(sources[name], name, byLane[name], all)
	}

	out := make([]*Source, 0, len(roster))
	for _, name := range roster {
		out = append(out, sources[name])
	}
	return out
}

// laneNames reads the roster and returns the lane owners' slugs, sorted.
func laneNames(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "participants.json"))
	if err != nil {
		return nil, err
	}
	var c struct {
		Participants []struct {
			Name string `json:"name"`
			Lane string `json:"lane"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range c.Participants {
		if slug, ok := strings.CutPrefix(p.Lane, "from-"); ok && slug != "" {
			out = append(out, slug)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("participants.json names no lane")
	}
	sort.Strings(out)
	return out, nil
}

// readNote parses one file. It returns nil when the file is not a tokens note at all —
// the subject is the whole test, exact, because the prototype's case-insensitive match
// with any text after the date folded notes nobody meant as a report.
func readNote(lane, path, text string, rules *Rules, at time.Time) *note {
	header, body, headerLines := splitNote(text)
	subject, ok := ParseSubject(header["Subject"])
	if !ok {
		return nil
	}
	n := &note{lane: lane, path: path, id: header["Id"], subject: subject, zones: map[string]bool{}}
	label := Label(KindBus, lane)
	if n.id == "" {
		n.id = filepath.Base(path)
	}
	n.dateLine = headerLines["Date"]

	// The Date: is validated and never used for order. A correction with a bad date is
	// refused whole rather than half-read.
	switch date, has := header["Date"]; {
	case !has:
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 0, Text: "no Date: header; a tokens note wants one nova-bus wrote"}
	default:
		t, err := time.Parse(BusDateLayout, date)
		if err != nil {
			n.dead = &Unparsed{Label: label, Note: n.id, Line: n.dateLine, Text: "the Date: is not a date nova-bus writes (" + BusDateLayout + "): " + date}
		} else if t.After(at) {
			n.dead = &Unparsed{Label: label, Note: n.id, Line: n.dateLine, Text: "the Date: is later than this fold's own at= stamp: " + date}
		}
	}
	if subject.badSet != "" && n.dead == nil {
		n.dead = &Unparsed{Label: label, Note: n.id, Line: 1, Text: subject.badSet + "; send a correction whose subject carries supersedes=<id>"}
	}
	if n.dead != nil {
		return n
	}
	parseBody(n, label, body, len(headerLines)+1, rules)
	return n
}

// splitNote reads the header — every line before the first blank line, `Key: value`, with
// nova-bus's two presentation tolerances: a markdown heading above it and a bullet on
// each line.
func splitNote(text string) (map[string]string, []string, map[string]int) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	header := map[string]string{}
	at := map[string]int{}
	first := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		first = 1
		for first < len(lines) && strings.TrimSpace(lines[first]) == "" {
			first++
		}
	}
	end := len(lines)
	for i := first; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			end = i
			break
		}
		if rest, cut := strings.CutPrefix(line, "- "); cut {
			line = rest
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			continue
		}
		if _, dup := header[key]; !dup {
			header[key] = strings.TrimSpace(value)
			at[key] = i + 1
		}
	}
	var body []string
	if end < len(lines) {
		body = lines[end+1:]
	}
	return header, body, at
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
		m := Message{Day: day, Basis: basis, Model: model, Repo: rules.Attribute([]string{repo}, ""), Rough: rough}
		m.Counts.Set(t, v)
		n.msgs = append(n.msgs, m)
		n.rough += rough
	}
}

// foldLane resolves one lane's notes: the predecessor sets, the chains, the tips, and the
// one conflict that folds nothing.
func foldLane(s *Source, lane string, notes []*note, all map[string]*note) {
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
				n.dead = &Unparsed{Label: label, Note: n.id, Line: 1, Text: why + "; send a correction whose subject carries supersedes=<id>"}
				again = true
			}
		}
	}
	// A cycle at any length, through any member, refuses every note on it.
	for _, n := range notes {
		if n.dead == nil && onCycle(n, all, map[string]bool{}) {
			n.dead = &Unparsed{Label: label, Note: n.id, Line: 1,
				Text: "a cycle: this note's predecessor set reaches itself; send a correction whose subject carries supersedes=<id>"}
		}
	}

	superseded := map[string]string{}
	for _, n := range notes {
		if n.dead != nil {
			continue
		}
		for _, id := range n.subject.supersedes {
			superseded[id] = n.id
		}
	}

	byDay := map[string][]*note{}
	for _, n := range notes {
		byDay[n.subject.day] = append(byDay[n.subject.day], n)
	}
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)

	for _, day := range days {
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
				if z != UTC {
					s.Basis = mergeBasis(s.Basis, z)
				}
			}
			if len(n.touched) > 0 {
				s.Toucheds = append(s.Toucheds, Touched{Label: label, Day: day, Repos: n.touched})
			}
		}
	}
	s.Reports = reportedTypes(s.Stream)
}

// badPredecessors names why a successor's predecessor set is refused, or returns the empty
// string. Every member is checked before anything is replaced.
func badPredecessors(n *note, all map[string]*note) string {
	for _, id := range n.subject.supersedes {
		p, ok := all[id]
		switch {
		case !ok:
			return "no such note in this lane for this day: " + id
		case p.lane != n.lane:
			return "the predecessor " + id + " is a note of another lane (from-" + p.lane + ")"
		case p.subject.day != n.subject.day:
			return "the predecessor " + id + " is a note for another day (" + p.subject.day + ")"
		case !p.clean():
			return "the predecessor " + id + " did not parse"
		}
	}
	return ""
}

// onCycle walks the supersedes graph from n and reports whether it comes back.
func onCycle(n *note, all map[string]*note, seen map[string]bool) bool {
	if seen[n.id] {
		return true
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
	delete(seen, n.id)
	return false
}

// mergeBasis is a lane's day_basis: utc, one zone, or `mixed` when its lines carry more
// than one. A lane is allowed to be mixed across days; a row never is.
func mergeBasis(have, zone string) string {
	switch have {
	case UTC:
		return zone
	case zone:
		return zone
	}
	return "mixed"
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
