package tokens

// The coordinator's own session, folded (G5 of pit stop 3, #828).
//
// Glenn, 2026-09-16: "This seems like a lot. How can we make the coordinator more
// efficient?" The honest answer needed a number, and there was none: every worker's spend
// was on a swarm CARD line and in the ledger, and the coordinator's own window -- the
// single most expensive line on the bench -- was measured by hand, once, and never again.
//
// This reader folds one Claude Code session jsonl into the four counts and one weighted
// equivalent, so the coordinator is a model line in the daily ledger like everybody else
// (SPEC-PULSE, "Rate and convergence" rule 8: the coordinator is a friend).
//
// WEIGHTED is the comparable number. A cache read is not a fresh input token and an output
// token is not one either, so a raw sum of the four flatters a window that reads a huge
// cache and understates one that writes a lot. The weights are the provider's own price
// ratios: cache write 1.25x an input token, cache read 0.1x, output 5x.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The weights the WEIGHTED equivalent is built from, as the provider prices them.
const (
	WeightCacheWrite = 1.25
	WeightCacheRead  = 0.1
	WeightOutput     = 5.0
)

// CoordinatorModel is the ledger row a coordinator's session folds into. It names the model
// AND the seat: the same model driving a worker is a different line, because the question
// the ledger answers is what the coordinating cost, not what the model cost.
const CoordinatorModel = "claude-fable-5-1/coordinator"

// CoordinatorRepo is the repo cell of that row. A coordinator's turns are not one repo's
// work -- they are the bench's -- and a row attributed to whichever repo a tool call
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

	s := SessionSum{Days: map[string]*SessionSum{}}
	for _, id := range order {
		t := turns[id]
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
	}
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
	out := make([]string, 0, len(s.Days))
	for d := range s.Days {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Row is the day file row one day of this session folds into.
func (s SessionSum) Row(day string) DayRow {
	d, ok := s.Days[day]
	if !ok {
		d = &SessionSum{}
	}
	r := DayRow{Date: day, Model: CoordinatorModel, Repo: CoordinatorRepo, Basis: UTC, Sources: []string{SessionLabel}}
	r.Counts.Set(Input, d.Input)
	r.Counts.Set(Output, d.Output)
	r.Counts.Set(CacheWrite, d.CacheWrite)
	r.Counts.Set(CacheRead, d.CacheRead)
	return r
}
