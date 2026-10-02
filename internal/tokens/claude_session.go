package tokens

// The coordinator's own session, folded.
//
// Every worker's spend is on a swarm card line and in the ledger; the coordinator's own
// window, often the most expensive line of all, is not, unless this reader folds it.
//
// This reader folds one Claude Code session jsonl into the four counts and one weighted
// equivalent, so the coordinator's window is a model line in the daily ledger like every
// worker's.
//
// WEIGHTED is the comparable number. A cache read is not a fresh input token and an output
// token is not one either, so a raw sum of the four flatters a window that reads a huge
// cache and understates one that writes a lot. The weights are the provider's own price
// ratios: cache write 1.25x an input token, cache read 0.1x, output 5x.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// The weights the WEIGHTED equivalent is built from, as the provider prices them.
const (
	WeightCacheWrite = 1.25
	WeightCacheRead  = 0.1
	WeightOutput     = 5.0
)

// CoordinatorSeat is the suffix of the ledger row a coordinator's session folds into. The row
// names the model the TRANSCRIPT names and the seat: `<model>/coordinator`. The same model
// driving a worker is a different line, because the question the ledger answers is what the
// coordinating cost, not what the model cost. The model is read from each turn (the same
// `message.model` field the spend fold reads) and never assumed: a transcript of another
// model is booked under that model, and a transcript that names none is refused.
const CoordinatorSeat = "/coordinator"

// CoordinatorModelOf is the ledger model cell of a coordinator turn made by `model`.
func CoordinatorModelOf(model string) string { return model + CoordinatorSeat }

// CoordinatorRepo is the repo cell of that row. A coordinator's turns are not one repo's
// work -- they span every repo -- and a row attributed to whichever repo a tool call
// happened to name would move the cost around from day to day.
const CoordinatorRepo = "coordinator"

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

// Weighted is the fresh-input-equivalent: input + 1.25 x cache write + 0.1 x cache read +
// 5 x output, rounded down to a whole token.
func (s SessionSum) Weighted() int64 {
	return int64(float64(s.Input) +
		WeightCacheWrite*float64(s.CacheWrite) +
		WeightCacheRead*float64(s.CacheRead) +
		WeightOutput*float64(s.Output))
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

// Line is the one line the session verb prints.
func (s SessionSum) Line() string {
	return fmt.Sprintf("SESSION turns=%d input=%d cache_write=%d cache_read=%d output=%d weighted=%d avg_context=%d",
		s.Turns, s.Input, s.CacheWrite, s.CacheRead, s.Output, s.Weighted(), s.AvgContext())
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
// that day name, each `<model>/coordinator`. A day the session has no turn on is a row of
// zeros for every model the session names, because "this window spent nothing that day" is
// a measurement; a session that names no model has no rows.
func (s SessionSum) Rows(day string) []DayRow {
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
		r := DayRow{Date: day, Model: CoordinatorModelOf(m), Repo: CoordinatorRepo, Basis: UTC, Sources: []string{SessionLabel}}
		r.Counts.Set(Input, d.Input)
		r.Counts.Set(Output, d.Output)
		r.Counts.Set(CacheWrite, d.CacheWrite)
		r.Counts.Set(CacheRead, d.CacheRead)
		rows = append(rows, r)
	}
	return rows
}
