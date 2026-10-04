package tokens

// One session window, folded.
//
// This reader folds one Claude Code session jsonl into the four counts and one weighted
// equivalent, so a window's spend is a model line in the daily ledger like everybody
// else's. The tool carries the concepts and none of the fleet (docs/STANDARD.md section
// 4): the row's role and the weights are the caller's flags, never constants here.
//
// WEIGHTED is the comparable number. A cache read is not a fresh input token and an output
// token is not one either, so a raw sum of the four flatters a window that reads a huge
// cache and understates one that writes a lot. The weights are a comparison, not a price:
// DefaultWeights is the ratios of one vendor's published list prices (cache write 1.25x
// an input token, cache read 0.1x, output 5x), and the session verb's --weights flag
// carries them and takes yours.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Weights is the four ratios the WEIGHTED equivalent is built from: how many fresh input
// tokens one input, one cache write, one cache read and one output token are worth.
type Weights struct{ Input, CacheWrite, CacheRead, Output float64 }

// DefaultWeights is a comparison, not a price: the ratios of one vendor's published list
// prices (cache write 1.25x an input token, cache read 0.1x, output 5x). The session
// verb's --weights flag carries these four numbers and takes yours.
var DefaultWeights = Weights{Input: 1, CacheWrite: 1.25, CacheRead: 0.1, Output: 5}

// Flag renders w as the in,cw,cr,out four comma-separated numbers the session verb's
// --weights flag takes, so the flag's default is DefaultWeights printed and never a
// second copy of it.
func (w Weights) Flag() string {
	g := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	return g(w.Input) + "," + g(w.CacheWrite) + "," + g(w.CacheRead) + "," + g(w.Output)
}

// ParseWeights reads the in,cw,cr,out four numbers --weights takes. A value that is not
// four finite numbers is a refusal saying what the flag WANTS, never a guess
// (docs/STANDARD.md section 3, onboarding point 2).
func ParseWeights(s string) (Weights, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return Weights{}, fmt.Errorf("--weights wants four comma-separated numbers in,cw,cr,out, got %d in %s", len(parts), oneline.Field(s))
	}
	nums := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		// v*0 is zero for every finite number and NaN for a NaN and both infinities:
		// one product refuses the three values no token count can carry.
		if err != nil || v*0 != 0 {
			return Weights{}, fmt.Errorf("--weights wants four comma-separated numbers in,cw,cr,out, and %s is not a finite number", oneline.Field(p))
		}
		nums[i] = v
	}
	return Weights{Input: nums[0], CacheWrite: nums[1], CacheRead: nums[2], Output: nums[3]}, nil
}

// SessionLabel is the source label the row names, so every number stays traceable to the
// flag of the run that wrote it.
const SessionLabel = "claude-session"

// SessionSum is one session's usage: the turns, the four counts, and the days the turns
// fell on so a fold can write the right day file.
type SessionSum struct {
	Turns      int
	Input      int64
	CacheWrite int64
	CacheRead  int64
	Output     int64

	// Days is the per-day split, keyed by the UTC day of each turn's stamp. A session that
	// crosses midnight is two rows, never one row dated by the file.
	Days map[string]*SessionSum

	// Unstamped counts the turns whose stamp this tool could not read. They are in the
	// totals and in no day: a turn measured but not dated is named, never dropped and
	// never dated by a guess.
	Unstamped int

	// Models is every model the transcript names, sorted, and Unnamed counts the turns that
	// name none. A turn with no model cannot be booked under one, and is never booked under
	// a guess: the fold refuses while Unnamed is above zero.
	Models  []string
	Unnamed int

	// DayModels is the split of Days by the model of each turn: day, then model. A session
	// that changes model mid-window is one row per model, never one row under the last.
	DayModels map[string]map[string]*SessionSum
}

// sessionLine is the part of a transcript line this reader needs.
type sessionLine struct {
	Timestamp string `json:"timestamp"`
	Message   *struct {
		ID    string                     `json:"id"`
		Model string                     `json:"model"`
		Usage map[string]json.RawMessage `json:"usage"`
	} `json:"message"`
}

// ReadClaudeSession sums one Claude Code session jsonl.
//
// A STREAMED assistant message writes its id on many lines, each with a usage block that
// grows; the LAST line for an id is the message and every earlier one is a partial. So the
// sum is per message id, last write wins -- the same rule ReadClaude keeps -- and a reader
// that added every line would report a session several times its own size.
func ReadClaudeSession(path string) (SessionSum, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionSum{}, err
	}
	defer f.Close()

	type turn struct {
		day                                  string
		model                                string
		dated                                bool
		input, cacheWrite, cacheRead, output int64
	}
	turns := map[string]*turn{}
	var order []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	n, bad := 0, 0
	for sc.Scan() {
		n++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var l sessionLine
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			bad++
			continue
		}
		if l.Message == nil || l.Message.Usage == nil || l.Message.Model == syntheticModel {
			continue
		}
		id := l.Message.ID
		if id == "" {
			id = fmt.Sprintf("line-%d", n)
		}
		t, ok := turns[id]
		if !ok {
			t = &turn{}
			turns[id] = t
			order = append(order, id)
		}
		t.input = usageOf(l.Message.Usage, "input_tokens")
		t.cacheWrite = usageOf(l.Message.Usage, "cache_creation_input_tokens")
		t.cacheRead = usageOf(l.Message.Usage, "cache_read_input_tokens")
		t.output = usageOf(l.Message.Usage, "output_tokens")
		t.model = l.Message.Model
		if day, ok := DayOfStamp(l.Timestamp); ok {
			t.day, t.dated = day, true
		}
	}
	if err := sc.Err(); err != nil {
		return SessionSum{}, err
	}
	if bad > 0 && len(order) == 0 {
		return SessionSum{}, fmt.Errorf("no readable turn in %d lines; %d of them are not JSON", n, bad)
	}

	s := SessionSum{Days: map[string]*SessionSum{}, DayModels: map[string]map[string]*SessionSum{}}
	named := map[string]bool{}
	for _, id := range order {
		t := turns[id]
		if t.model == "" {
			s.Unnamed++
		} else {
			named[t.model] = true
		}
		s.Turns++
		s.Input += t.input
		s.CacheWrite += t.cacheWrite
		s.CacheRead += t.cacheRead
		s.Output += t.output
		if !t.dated {
			s.Unstamped++
			continue
		}
		d, ok := s.Days[t.day]
		if !ok {
			d = &SessionSum{}
			s.Days[t.day] = d
		}
		d.Turns++
		d.Input += t.input
		d.CacheWrite += t.cacheWrite
		d.CacheRead += t.cacheRead
		d.Output += t.output
		if t.model == "" {
			continue
		}
		byModel, ok := s.DayModels[t.day]
		if !ok {
			byModel = map[string]*SessionSum{}
			s.DayModels[t.day] = byModel
		}
		m, ok := byModel[t.model]
		if !ok {
			m = &SessionSum{}
			byModel[t.model] = m
		}
		m.Turns++
		m.Input += t.input
		m.CacheWrite += t.cacheWrite
		m.CacheRead += t.cacheRead
		m.Output += t.output
	}
	s.Models = slices.Sorted(maps.Keys(named))
	return s, nil
}

func usageOf(usage map[string]json.RawMessage, key string) int64 {
	raw, ok := usage[key]
	if !ok {
		return 0
	}
	v, ok := jsonInt(raw)
	if !ok {
		return 0
	}
	return v
}

// Weighted is the fresh-input-equivalent under w: w.Input x input + w.CacheWrite x cache
// write + w.CacheRead x cache read + w.Output x output, rounded down to a whole token.
func (s SessionSum) Weighted(w Weights) int64 {
	return int64(w.Input*float64(s.Input) +
		w.CacheWrite*float64(s.CacheWrite) +
		w.CacheRead*float64(s.CacheRead) +
		w.Output*float64(s.Output))
}

// AvgContext is the average context a turn carried: everything read (input, cache write,
// cache read) over the turns. It is the number that says whether the window is the cost,
// and a session with no turn has none.
func (s SessionSum) AvgContext() int64 {
	if s.Turns == 0 {
		return 0
	}
	return (s.Input + s.CacheWrite + s.CacheRead) / int64(s.Turns)
}

// Line is the one line the session verb prints, WEIGHTED under w.
func (s SessionSum) Line(w Weights) string {
	return fmt.Sprintf("SESSION turns=%d input=%d cache_write=%d cache_read=%d output=%d weighted=%d avg_context=%d",
		s.Turns, s.Input, s.CacheWrite, s.CacheRead, s.Output, s.Weighted(w), s.AvgContext())
}

// DayList is the days this session touched, sorted.
func (s SessionSum) DayList() []string {
	return slices.Sorted(maps.Keys(s.Days))
}

// UnbookableReason is why this session cannot be folded into the ledger, or "" when it can:
// every row is booked under the model the transcript names, so a transcript that names none
// (or names it on only some turns) has no honest row. The caller refuses with this text.
func (s SessionSum) UnbookableReason() string {
	switch {
	case s.Unnamed > 0 && len(s.Models) == 0:
		return fmt.Sprintf("the transcript names no model on any of its %d turns, and the ledger row is booked under the model the transcript names, never under a guess; nothing written", s.Unnamed)
	case s.Unnamed > 0:
		return fmt.Sprintf("the transcript names no model on %d of its %d turns, and a turn is booked under the model the transcript names, never under a guess; nothing written", s.Unnamed, s.Turns)
	}
	return ""
}

// Rows is the day file rows one day of this session folds into: one per model the turns of
// that day name. The row names the model the TRANSCRIPT names and, when the caller's
// --role flag names a role, the seat too: `<model>/<role>`, with the role in the repo
// cell. The same model driving another line of work is a different row, because the
// question the ledger answers is what that work cost, not what the model cost; a window's
// turns are not one repo's work, and a row attributed to whichever repo a tool call
// happened to name would move the cost around from day to day, so with no role the model
// stands alone and the repo cell is the fixed word `unattributed`. A day the session has
// no turn on is a row of zeros for every model the session names, because "this window
// spent nothing that day" is a measurement; a session that names no model has no rows.
func (s SessionSum) Rows(day, role string) []DayRow {
	byModel := s.DayModels[day]
	names := s.Models
	if len(byModel) > 0 {
		names = slices.Sorted(maps.Keys(byModel))
	}
	rows := make([]DayRow, 0, len(names))
	for _, m := range names {
		d := byModel[m]
		if d == nil {
			d = &SessionSum{}
		}
		model, repo := m, Unattributed
		if role != "" {
			model, repo = m+"/"+role, role
		}
		r := DayRow{Date: day, Model: model, Repo: repo, Basis: UTC, Sources: []string{SessionLabel}}
		r.Counts.Set(Input, d.Input)
		r.Counts.Set(Output, d.Output)
		r.Counts.Set(CacheWrite, d.CacheWrite)
		r.Counts.Set(CacheRead, d.CacheRead)
		rows = append(rows, r)
	}
	return rows
}
