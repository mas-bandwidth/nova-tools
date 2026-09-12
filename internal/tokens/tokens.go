/*
Package tokens is the accounting layer: the one message shape every source produces, the
one fold over a stream of them, the day file, and the readers for the five declared
source kinds.

The shape of the thing is one sentence. Each source hands the fold a stream of messages —
{day, basis, model, repo, usage} — and the fold is ONE function over that stream, keyed
exactly by (day, model, repo), with the five token types kept apart and a type no source
reported left as a dash. What differs per source kind is only how the stream is produced,
which is why the attribution rule, the dash rule and the sum rule are each written once
here rather than once per reader: the prototype this replaces had two copies of the
attribution table in two scripts and they disagreed about three repos.

Nothing in this package removes a file, and nothing in it runs a program except the one
subprocess the OpenCode reader declares.
*/
package tokens

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Type is one of the five token types. They are kept apart everywhere: nothing in this
// package folds one into another, and a provider that does not expose reasoning or cache
// usage is not evidence that none occurred.
type Type int

// The five, in the order every row prints them.
const (
	Input Type = iota
	Output
	CacheWrite
	CacheRead
	Reasoning
	NTypes
)

// TypeNames are the five names as a source, a bus line and a day file spell them.
var TypeNames = [NTypes]string{"input", "output", "cache_write", "cache_read", "reasoning"}

// Dash is the cell of a type the source did not report. It is not a zero, and the
// difference is the whole of rule 15: a zero is a measurement and a dash is an absence,
// and a zero that meant "not measured" would sum into a month claiming to be complete.
const Dash = "-"

// TypeByName resolves one of the five names.
func TypeByName(name string) (Type, bool) {
	for i, n := range TypeNames {
		if n == name {
			return Type(i), true
		}
	}
	return 0, false
}

// Counts is the five types, each either a count or absent.
type Counts struct {
	n   [NTypes]int64
	has [NTypes]bool
}

// Set records a measurement. A second Set on one type ADDS, which is how a row fed by two
// sources sums per type over the sources that reported that type.
func (c *Counts) Set(t Type, v int64) {
	c.n[t] += v
	c.has[t] = true
}

// Get is the count and whether any source reported it.
func (c Counts) Get(t Type) (int64, bool) { return c.n[t], c.has[t] }

// Cell is the day-file cell: the number, or a dash where nothing reported it.
func (c Counts) Cell(t Type) string {
	if !c.has[t] {
		return Dash
	}
	return strconv.FormatInt(c.n[t], 10)
}

// Total is the five types summed, for the shares on a day line. A dash adds nothing.
func (c Counts) Total() int64 {
	var n int64
	for t := Type(0); t < NTypes; t++ {
		if c.has[t] {
			n += c.n[t]
		}
	}
	return n
}

// Dashes is how many of the five cells are a dash.
func (c Counts) Dashes() int {
	n := 0
	for t := Type(0); t < NTypes; t++ {
		if !c.has[t] {
			n++
		}
	}
	return n
}

// Add folds one Counts into another, per type, over the types the other reported.
func (c *Counts) Add(o Counts) {
	for t := Type(0); t < NTypes; t++ {
		if o.has[t] {
			c.Set(t, o.n[t])
		}
	}
}

// UTC is the day basis of every row dated from a stamp. Anything else is the zone a
// provider's export declares for its own per-day totals, and a row that is not UTC says
// so in its own column rather than being quietly counted as one.
const UTC = "utc"

// Message is the one unit every source hands the fold.
type Message struct {
	ID     string // the source's own message id, for the overlap check; not part of Key
	Day    string // YYYY-MM-DD
	Basis  string // UTC, or the zone a provider export declares
	Model  string
	Repo   string // already attributed by the reader, through repo.go's one function
	Counts Counts
	Rough  int  // how many `~` bus lines this message stands for
	Turn   bool // counted into turns= (the sources that count messages)
}

// Key is exactly (day, model, repo). Nobody's name is in it: the `who` of a bus line and
// the window-or-child mark of a transcript are not columns, because a model on a repo on
// a day is one row whoever drove it.
type Key struct{ Day, Model, Repo string }

// Row is one line of a day file while it is still being accumulated.
type Row struct {
	Key
	Counts  Counts
	Rough   int
	bases   map[string]bool
	sources map[string]bool
}

// Bases is the day bases that fed this row, sorted. More than one is a row that is not
// written (rule 17).
func (r *Row) Bases() []string { return sortedKeys(r.bases) }

// Sources is the sorted, comma-joinable labels that fed this row.
func (r *Row) Sources() []string { return sortedKeys(r.sources) }

// Basis is the row's one basis, or the empty string when it has two.
func (r *Row) Basis() string {
	b := r.Bases()
	if len(b) != 1 {
		return ""
	}
	return b[0]
}

// Folder is the fold: the one function over the stream, and the only place a row is made.
type Folder struct {
	rows  map[Key]*Row
	turns map[string]int
	days  map[string]bool

	// idLabel is which source first fed each message id, and overlaps counts the ids two
	// sources both fed. SPEC-TOKENS says the fold "deliberately does not check … that two
	// sources overlap", and the numbers here still do not change: two declarations of one
	// tree still double the day, exactly as the spec says. What changes is that the run
	// SAYS SO. (Measured 2026-09-11: ~/.claude/projects/<session>/subagents/agent-*.jsonl
	// and /private/tmp/.../tasks/*.output were the same 10,281 messages, the fold reported
	// 2,932,982,350 cache_read against the correct 1,502,293,166, written=true, check OK,
	// sum OK.)
	idLabel  map[string]string
	overlaps map[[2]string]int
}

// NewFolder returns an empty fold.
func NewFolder() *Folder {
	return &Folder{rows: map[Key]*Row{}, turns: map[string]int{}, days: map[string]bool{},
		idLabel: map[string]string{}, overlaps: map[[2]string]int{}}
}

// Overlap is two declared sources that fed the same message ids: not an error, and not a
// change to any number, but the one thing a green day file cannot say for itself.
type Overlap struct {
	A, B string
	IDs  int
}

// Overlaps is every pair of sources that shared an id, sorted, so the remedy can name one.
func (f *Folder) Overlaps() []Overlap {
	var out []Overlap
	for pair, n := range f.overlaps {
		out = append(out, Overlap{A: pair[0], B: pair[1], IDs: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// Add folds one message from the source named by label.
func (f *Folder) Add(label string, m Message) {
	if m.ID != "" {
		if first, seen := f.idLabel[m.ID]; !seen {
			f.idLabel[m.ID] = label
		} else if first != label {
			pair := [2]string{first, label}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			f.overlaps[pair]++
		}
	}
	k := Key{Day: m.Day, Model: m.Model, Repo: m.Repo}
	r, ok := f.rows[k]
	if !ok {
		r = &Row{Key: k, bases: map[string]bool{}, sources: map[string]bool{}}
		f.rows[k] = r
	}
	r.Counts.Add(m.Counts)
	r.Rough += m.Rough
	r.bases[m.Basis] = true
	r.sources[label] = true
	f.days[m.Day] = true
	if m.Turn {
		f.turns[m.Day]++
	}
}

// Days is every day the sources named, sorted.
func (f *Folder) Days() []string { return sortedKeys(f.days) }

// Turns is the message count for a day across the sources that count messages, and
// whether any of them fed it. A day fed by a bus note alone has none, and its version
// line says `-` rather than 0: a count nobody took is not a count of zero.
func (f *Folder) Turns(day string) (int, bool) {
	n, ok := f.turns[day]
	return n, ok
}

// Mixed is a row fed by two day bases: not written, and named. Labels is WHICH sources
// fed it: "if a row mixed two day bases it names the two labels" (SPEC-TOKENS, the
// TOKENS NOTE paragraph). Telling a caller to declare one export for that day without
// saying which two are competing is the one remedy that names nothing.
type Mixed struct {
	Key
	Bases  []string
	Labels []string
}

// DayRows is the day's rows, sorted by (model, repo), and separately the rows of that day
// that mixed two bases. A mixed row is in neither the file nor the counts.
func (f *Folder) DayRows(day string) (rows []*Row, mixed []Mixed) {
	for k, r := range f.rows {
		if k.Day != day {
			continue
		}
		if len(r.bases) > 1 {
			mixed = append(mixed, Mixed{Key: k, Bases: r.Bases(), Labels: r.Sources()})
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Repo < rows[j].Repo
	})
	sort.Slice(mixed, func(i, j int) bool {
		if mixed[i].Model != mixed[j].Model {
			return mixed[i].Model < mixed[j].Model
		}
		return mixed[i].Repo < mixed[j].Repo
	})
	return rows, mixed
}

// ---------------------------------------------------------------- what a source reports

// Stat is what one declared source yielded. A field that does not apply to the kind
// prints a dash rather than a zero: a transcript has no unparsed lines and a bus lane has
// no duplicate ids, and a dash is an absence where a zero is a measurement.
type Stat struct {
	Files      int
	Unreadable int
	Messages   int
	Dup        int
	NoID       int
	NoUsage    int
	Unparsed   int
	Comments   int
	Redated    int
	Superseded int
	Rows       int
}

// The five source kinds, as `kind=` prints them.
const (
	KindClaude   = "claude"
	KindOpenCode = "opencode"
	KindSwarm    = "swarm"
	KindBus      = "bus"
	KindProvider = "provider"
)

// applies says which Stat fields a kind can have. The fold prints a dash for the rest.
var applies = map[string]map[string]bool{
	// A transcript's and a database's `unparsed` is a DASH, because SPEC-TOKENS says so
	// in the TOKENS SOURCE paragraph: "a transcript has no unparsed lines ... a dash is
	// an absence where a zero is a measurement". A line whose stamp this tool cannot
	// read IS counted and printed -- one TOKENS UNPARSED line naming the label, and the
	// unparsed= total on TOKENS FAIL (rule 3) -- so nothing is lost by the dash; what the
	// column would claim is that a clean transcript was MEASURED for unparsed lines, and
	// the spec reserves the zero for that. The PR proposes striking the spec's clause; if
	// it is struck, these two become `"unparsed": true` and the column is the measurement.
	KindClaude:   {"files": true, "unreadable": true, "messages": true, "dup": true, "noid": true, "rows": true},
	KindOpenCode: {"files": true, "unreadable": true, "messages": true, "dup": true, "noid": true, "rows": true},
	KindSwarm:    {"files": true, "unreadable": true, "messages": true, "dup": true, "noid": true, "nousage": true, "unparsed": true, "rows": true},
	KindBus:      {"files": true, "unreadable": true, "unparsed": true, "comments": true, "redated": true, "superseded": true, "rows": true},
	KindProvider: {"files": true, "unreadable": true, "unparsed": true, "rows": true},
}

// Applies reports whether a Stat field is a measurement for this kind.
func Applies(kind, field string) bool { return applies[kind][field] }

// Unreadable is a declared source the tool could not read: counted, printed on its own
// line, and the reason the run exits 1, because a declared source is a claim that the
// report covers it.
type Unreadable struct{ Label, Path, Why string }

// Unparsed is a bus line, a whole bus note, or an export the parser could not read.
type Unparsed struct {
	Label, Note string
	Line        int
	Text        string
	// Remedy, when set, is the one act for THIS unparsed rather than for its kind: a
	// near-miss subject is a bus unparsed, and "a body line is date<TAB>who..." is not
	// what its writer has to change.
	Remedy string
}

// Superseded is a bus note a later note in the same lane replaced by name.
type Superseded struct{ Label, Note, By, Day string }

// Conflict is a lane-day with more than one tip: no winner is inferred, nothing folds.
type Conflict struct {
	Label, Day string
	Notes      []string
}

// Touched is the one comment shape a friend's note may carry, and it adds no numbers.
type Touched struct {
	Label, Day string
	Repos      []string
}

// Source is one declared source after it has been read.
type Source struct {
	Label   string // as it is printed and as it appears in a row's sources column
	Kind    string
	Path    string
	Reports []Type
	Basis   string // utc, a zone, or "mixed" for a bus lane whose lines carry more than one
	Stat    Stat
	Stream  []Message

	Unreadables []Unreadable
	Unparseds   []Unparsed
	Supersededs []Superseded
	Conflicts   []Conflict
	Toucheds    []Touched

	// byID and order build Stream through AddMessage: one entry per message id, in
	// first-seen order.
	byID  map[string]Message
	order []string
}

// AddMessage puts one message into this source's stream, collapsed onto its id.
//
// This is rule 4, and it is written ONCE: "A Claude Code transcript repeats a message id
// on every streamed line; the last line for an id carries the message's final usage, and
// that is the one counted. Within one source, a second occurrence of an id is dup=<n>,
// never a second count. A message with no id is counted in noid=<n> and not folded."
// claude.go and opencode.go each kept their own byID/order/dup loop, and two copies of one
// rule are two rules (lesson 113).
func (s *Source) AddMessage(id string, m Message) {
	if id == "" {
		s.Stat.NoID++
		return
	}
	if s.byID == nil {
		s.byID = map[string]Message{}
	}
	m.ID = id
	if _, seen := s.byID[id]; seen {
		s.Stat.Dup++
	} else {
		s.order = append(s.order, id)
	}
	s.byID[id] = m
}

// Collapse lays the collapsed messages into Stream, in first-seen order, and counts them.
// Every reader that calls AddMessage ends with it.
func (s *Source) Collapse() {
	for _, id := range s.order {
		s.Stream = append(s.Stream, s.byID[id])
	}
	s.Stat.Messages = len(s.Stream)
}

// ReportsList is the comma-joined type names this source reports at all, so a reader of a
// mixed row can see which source could not have covered which cell.
func (s *Source) ReportsList() string {
	if len(s.Reports) == 0 {
		// A lane that folded no row reports no type, and the field's value is a dash
		// like every other absence on this line. It used to render as nothing at all --
		// `reports= day_basis=utc` -- which is a field with no value in a grammar whose
		// every field has one.
		return Dash
	}
	names := make([]string, 0, len(s.Reports))
	for _, t := range s.Reports {
		names = append(names, TypeNames[t])
	}
	return strings.Join(names, ",")
}

// StatField renders one Stat field for the source line: the number, or a dash where the
// field is not a measurement for this kind.
func (s *Source) StatField(field string) string {
	if !Applies(s.Kind, field) {
		return Dash
	}
	switch field {
	case "files":
		return itoa(s.Stat.Files)
	case "unreadable":
		return itoa(s.Stat.Unreadable)
	case "messages":
		return itoa(s.Stat.Messages)
	case "dup":
		return itoa(s.Stat.Dup)
	case "noid":
		return itoa(s.Stat.NoID)
	case "nousage":
		return itoa(s.Stat.NoUsage)
	case "unparsed":
		return itoa(s.Stat.Unparsed)
	case "comments":
		return itoa(s.Stat.Comments)
	case "redated":
		return itoa(s.Stat.Redated)
	case "superseded":
		return itoa(s.Stat.Superseded)
	case "rows":
		return itoa(s.Stat.Rows)
	}
	return Dash
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// AllTypes is every type, for a source that reports all five.
var AllTypes = []Type{Input, Output, CacheWrite, CacheRead, Reasoning}

// ClaudeTypes is what a Claude Code transcript carries: no reasoning count exists in it.
var ClaudeTypes = []Type{Input, Output, CacheWrite, CacheRead}

// ValidDay reports whether s is a YYYY-MM-DD day ON THE CALENDAR. The shape alone was
// the whole test, so `--day 2026-13-40` was accepted, wrote 2026-13-40.tsv, passed
// `check`, and left MissingDays walking from a day that does not exist. time.Parse is the
// range check, and the round trip refuses what it normalises (2026-02-30 -> 2026-03-02).
func ValidDay(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	t, err := time.Parse(dayLayout, s)
	return err == nil && t.Format(dayLayout) == s
}

// ValidZone reports whether s is a day_basis a day file may carry: a zone name with no
// whitespace, and never the word utc, which the six-field form already says.
func ValidZone(s string) bool {
	if s == "" || s == UTC {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) < 0
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Label is how a source names itself in a row's sources column and on its own line:
// the kind, a colon, and the flag's label — or, for a bus lane, the lane owner's name,
// whatever the `who` field of a line inside it says.
func Label(kind, name string) string { return kind + ":" + name }

// Percent renders a bucket's share of a day to one decimal, so a day that is 40% unknown
// says so on the line a person reads.
func Percent(part, whole int64) string {
	if whole == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", float64(part)*100/float64(whole))
}

// opens counts every source file this process has opened. The fold's cost is ONE PASS over
// each declared file -- there is no index and no incremental mode, and a day file is
// recomputed whole from the sources every time -- and a count is what a test can pin where
// a time cannot: the prototype read 2,497 files in about ten seconds, and that number is a
// fact about a disk rather than about this code.
var opens atomic.Int64

// Opens is how many source files have been opened since the process started.
func Opens() int64 { return opens.Load() }

// openSource is the ONE door every reader opens a source file through, so that the count
// above cannot drift from the truth by somebody reaching for os.Open directly.
func openSource(path string) (*os.File, error) {
	opens.Add(1)
	return os.Open(path)
}

// readSource is openSource for a whole file.
func readSource(path string) ([]byte, error) {
	opens.Add(1)
	return os.ReadFile(path)
}
