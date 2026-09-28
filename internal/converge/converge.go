// Package converge answers one question mechanically: are we converging?
//
// Glenn, 2026-09-15: convergence is the health metric — the contraction ratio
// per stream, every tick. Rowan answered it by hand on 2026-09-18 out of six
// different places, and the answer was a paragraph nobody could diff against
// the next one. A stream here is one number a converging family drives in one
// direction, read now and read at --since, with the ratio between them.
//
// Everything in this file is PURE: it takes already-fetched data and returns
// findings. The forge, git, the filesystem and the clock are seams (forge.go,
// git.go, sources.go), so every test is fake-driven and nothing here reaches a
// network. See docs/SPEC-CHECK.md.
package converge

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Trend is which way a stream moved, in the stream's own direction of travel.
type Trend string

const (
	// Contracting is the number moving the way this stream converges.
	Contracting Trend = "contracting"
	// Flat is a number that did not move, and a first tick, which has nothing
	// to have moved from.
	Flat Trend = "flat"
	// Widening is the number moving the other way: the one word this verb
	// exists to print.
	Widening Trend = "widening"
	// AbsentTrend is a stream whose source was not named. It is not zero and it
	// is not flat — it is unread, and the line says so rather than contributing
	// a number nobody measured.
	AbsentTrend Trend = "absent"
)

// Field is one key=value extra on a stream's line, after the four the spec
// fixes. The key is written by this package; the value goes through oneline.
type Field struct {
	Key   string
	Value string
}

// Stream is one measure of convergence: what it counts, what it counts now,
// what it counted at --since, and which way it is meant to move.
type Stream struct {
	Name    string // LANDING, CLASSES, SCRIPTS, PRS, EDGES, FLEET, LEDGER
	Measure string // what the number counts, printed as measure=

	Now    float64
	Before float64

	HaveNow    bool
	HaveBefore bool

	// Lower is true when fewer is converging. CLASSES is the one stream where
	// it is false: a class made mechanical cannot come back, so more entries is
	// the family contracting, not widening.
	Lower bool

	// Want is the flag that would have fed this stream, on an absent one.
	Want string

	// BeforeFrom names where `before` came from when it was not read from a
	// source of its own: `state` for the two streams a single photograph cannot
	// give a history.
	BeforeFrom string

	Extra []Field
}

// Absent reports whether this stream was read at all.
func (s Stream) Absent() bool { return !s.HaveNow }

// Ratio is always now/before, whichever way the stream converges, so one column
// means one thing down the whole reading. A before of zero has no ratio: the
// line prints `-` rather than an infinity dressed up as a measurement.
func (s Stream) Ratio() (float64, bool) {
	if !s.HaveNow || !s.HaveBefore {
		return 0, false
	}
	if s.Before == 0 {
		if s.Now == 0 {
			return 1, true
		}
		return 0, false
	}
	return s.Now / s.Before, true
}

// Trend is the direction in the stream's own terms.
func (s Stream) Trend() Trend {
	if !s.HaveNow {
		return AbsentTrend
	}
	if !s.HaveBefore {
		return Flat
	}
	switch {
	case nearly(s.Now, s.Before):
		return Flat
	case (s.Now < s.Before) == s.Lower:
		return Contracting
	default:
		return Widening
	}
}

// nearly compares two measured numbers at the precision the line prints them
// to, so a mean that differs in the fifteenth decimal is flat rather than a
// widening nobody can see.
func nearly(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// Num renders one measured number: an integer when it is one, two decimals when
// it is not, and `-` when the stream does not have it.
func Num(v float64, have bool) string {
	if !have {
		return "-"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// Line is the stream's whole output: the four fields the spec fixes, then this
// stream's own extras.
func (s Stream) Line() string {
	var b strings.Builder
	ratio, haveRatio := s.Ratio()
	fmt.Fprintf(&b, "CONVERGENCE %s now=%s before=%s ratio=%s trend=%s",
		oneline.Field(s.Name), Num(s.Now, s.HaveNow), Num(s.Before, s.HaveBefore),
		Num(ratio, haveRatio), oneline.Field(string(s.Trend())))
	if s.Measure != "" {
		fmt.Fprintf(&b, " measure=%s", oneline.Field(s.Measure))
	}
	if s.Absent() && s.Want != "" {
		fmt.Fprintf(&b, " source=%s", oneline.Field(s.Want))
	}
	if s.BeforeFrom != "" {
		fmt.Fprintf(&b, " before-from=%s", oneline.Field(s.BeforeFrom))
	}
	for _, f := range s.Extra {
		fmt.Fprintf(&b, " %s=%s", oneline.Field(f.Key), oneline.Field(f.Value))
	}
	return b.String()
}

// With adds one extra field, in the order the caller adds them.
func (s Stream) With(key, value string) Stream {
	s.Extra = append(s.Extra, Field{Key: key, Value: value})
	return s
}

// WithInt adds one counted extra field.
func (s Stream) WithInt(key string, n int) Stream {
	return s.With(key, strconv.Itoa(n))
}

// WithNum adds one measured extra field, rendered like the four fixed ones.
func (s Stream) WithNum(key string, v float64, have bool) Stream {
	return s.With(key, Num(v, have))
}

// AbsentStream is a stream nobody gave a source for: named, counted on the
// verdict line, and never guessed at.
func AbsentStream(name, measure, want string, lower bool) Stream {
	return Stream{Name: name, Measure: measure, Want: want, Lower: lower}
}

// Report is one tick: every stream in the fixed order the spec prints them.
type Report struct {
	Streams []Stream
}

// Verdict is the closing line's arithmetic: how many streams were measured, how
// many are contracting, which are widening and which were never read.
func (r Report) Verdict() (measured, contracting int, widening, absent []string) {
	for _, s := range r.Streams {
		if s.Absent() {
			absent = append(absent, s.Name)
			continue
		}
		measured++
		switch s.Trend() {
		case Contracting:
			contracting++
		case Widening:
			widening = append(widening, s.Name)
		}
	}
	return measured, contracting, widening, absent
}

// list renders a name list for a line: the names, or `-` for none.
func list(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, oneline.Field(n))
	}
	return strings.Join(out, ",")
}

// VerdictLine is the last line of the reading.
func (r Report) VerdictLine() string {
	measured, contracting, widening, absent := r.Verdict()
	word := "OK"
	if len(widening) > 0 {
		word = "WARN"
	}
	return fmt.Sprintf("CONVERGENCE %s streams=%d contracting=%d widening=%s absent=%s",
		word, measured, contracting, list(widening), list(absent))
}

// Lines is the whole reading: one line per stream, then the verdict.
func (r Report) Lines() []string {
	out := make([]string, 0, len(r.Streams)+1)
	for _, s := range r.Streams {
		out = append(out, s.Line())
	}
	return append(out, r.VerdictLine())
}

// StreamState is what one tick remembers about one stream: the value it read,
// when it read it, and how many consecutive ticks that stream has been
// widening. The streak is the only thing in this verb that cannot be recomputed
// from the sources, which is why it is the only thing kept.
type StreamState struct {
	Now      float64 `json:"now"`
	At       string  `json:"at"`
	Widening int     `json:"widening_ticks"`
}

// State is the whole memory of the verb: one entry per stream. There is no
// other state anywhere, and without --state there is none at all.
type State struct {
	Streams map[string]StreamState `json:"streams"`
}

// LoadState reads the state file. A file that is not there is an empty state
// and not an error: the first tick of a new stream has nothing to remember.
func LoadState(path string) (State, error) {
	st := State{Streams: map[string]StreamState{}}
	if strings.TrimSpace(path) == "" {
		return st, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{Streams: map[string]StreamState{}}, fmt.Errorf("%s is not a convergence state file: %w", path, err)
	}
	if st.Streams == nil {
		st.Streams = map[string]StreamState{}
	}
	return st, nil
}

// Save writes the state file, whole, through a temporary file in the same
// directory: a tick killed halfway through leaves the previous tick's memory
// rather than half of this one's.
func (s State) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".convergence-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Apply folds the remembered tick into this one and returns the report with its
// `before` filled in where only history could supply it, the new state, and
// whether any stream has now widened on two consecutive ticks -- the one
// condition that is exit 1.
//
// It is one function because the two halves are one decision: a stream's
// `before` and its widening streak are read from the same remembered entry, and
// a version of this that filled one without updating the other is how a streak
// gets counted twice.
func (r Report) Apply(st State, now time.Time) (Report, State, bool) {
	out := Report{Streams: make([]Stream, 0, len(r.Streams))}
	next := State{Streams: map[string]StreamState{}}
	streak := false
	for _, s := range r.Streams {
		prev, had := st.Streams[s.Name]
		if !s.HaveBefore && had && s.HaveNow {
			s.Before = prev.Now
			s.HaveBefore = true
			s.BeforeFrom = "state"
		}
		if s.Absent() {
			// An unread stream keeps what was remembered: a tick that could not
			// reach a source must not erase the streak the last one counted.
			if had {
				next.Streams[s.Name] = prev
			}
			out.Streams = append(out.Streams, s)
			continue
		}
		entry := StreamState{Now: s.Now, At: now.UTC().Format(time.RFC3339)}
		// A tick at or before the remembered instant is the SAME tick read
		// again, not a second one. The first real run of this verb found it:
		// two runs of one command over one window would have counted one
		// widening twice and gone red, so a reading nobody took would have
		// stopped the lane. The streak counts ticks of the clock, not
		// invocations.
		same := had && !now.After(parseState(prev.At))
		if same {
			entry.At = prev.At
		}
		if s.Trend() == Widening {
			entry.Widening = prev.Widening + 1
			if same {
				entry.Widening = prev.Widening
				if entry.Widening == 0 {
					entry.Widening = 1
				}
			}
			if entry.Widening >= 2 {
				streak = true
			}
		}
		next.Streams[s.Name] = entry
		out.Streams = append(out.Streams, s)
	}
	return out, next, streak
}

// parseState reads a remembered instant, answering the zero time for a state
// file written before this field carried one. A zero time is before every tick,
// so an unreadable instant makes the next tick a new one -- the safe way round:
// a streak that is counted is a line a person reads, and one that is silently
// dropped is a red that never comes.
func parseState(at string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(at))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// JSON is the whole reading as one object: the same numbers the lines print,
// for a reader that is a program.
type JSON struct {
	At          string       `json:"at"`
	Since       string       `json:"since"`
	Streams     []StreamJSON `json:"streams"`
	Verdict     string       `json:"verdict"`
	Measured    int          `json:"streams_measured"`
	Contracting int          `json:"contracting"`
	Widening    []string     `json:"widening"`
	Absent      []string     `json:"absent"`
}

// StreamJSON is one stream in that object. A number the verb does not have is
// null rather than zero, for the same reason the line prints `-`.
type StreamJSON struct {
	Name    string            `json:"stream"`
	Measure string            `json:"measure"`
	Now     *float64          `json:"now"`
	Before  *float64          `json:"before"`
	Ratio   *float64          `json:"ratio"`
	Trend   string            `json:"trend"`
	Want    string            `json:"source,omitempty"`
	Extra   map[string]string `json:"extra,omitempty"`
}

// AsJSON renders the report.
func (r Report) AsJSON(at, since time.Time) JSON {
	measured, contracting, widening, absent := r.Verdict()
	word := "OK"
	if len(widening) > 0 {
		word = "WARN"
	}
	out := JSON{
		At:          at.UTC().Format(time.RFC3339),
		Since:       since.UTC().Format(time.RFC3339),
		Verdict:     word,
		Measured:    measured,
		Contracting: contracting,
		Widening:    widening,
		Absent:      absent,
	}
	if out.Widening == nil {
		out.Widening = []string{}
	}
	if out.Absent == nil {
		out.Absent = []string{}
	}
	for _, s := range r.Streams {
		row := StreamJSON{Name: s.Name, Measure: s.Measure, Trend: string(s.Trend()), Want: s.Want}
		if s.HaveNow {
			v := s.Now
			row.Now = &v
		}
		if s.HaveBefore {
			v := s.Before
			row.Before = &v
		}
		if ratio, ok := s.Ratio(); ok {
			v := ratio
			row.Ratio = &v
		}
		if len(s.Extra) > 0 {
			row.Extra = map[string]string{}
			for _, f := range s.Extra {
				row.Extra[f.Key] = f.Value
			}
		}
		out.Streams = append(out.Streams, row)
	}
	if out.Streams == nil {
		out.Streams = []StreamJSON{}
	}
	return out
}

// ParseSince reads the far edge of the window in either spelling the flag
// takes: an RFC3339 instant, or a Go duration meaning that long before now. A
// window that ends before it starts is refused here rather than printed as a
// negative rate.
func ParseSince(s string, now time.Time) (time.Time, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return time.Time{}, fmt.Errorf("--since is empty")
	}
	if at, err := time.Parse(time.RFC3339, t); err == nil {
		if at.After(now) {
			return time.Time{}, fmt.Errorf("--since %s is after the clock (%s); a window cannot end before it starts",
				oneline.Field(t), now.UTC().Format(time.RFC3339))
		}
		return at.UTC(), nil
	}
	if d, err := time.ParseDuration(t); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("--since %s is not a window; a duration is how long ago the window starts, such as 24h", oneline.Field(t))
		}
		return now.Add(-d).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--since %s is neither an RFC3339 instant (2026-09-18T00:00:00Z) nor a duration (24h)", oneline.Field(t))
}
