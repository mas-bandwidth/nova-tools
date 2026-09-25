package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Card census is `nova-sprint census --sprint <S> [--keys <states>]`
// (nova-tools #3037): the redis-pipe prototype's 1,042-card read as one
// pipelined round trip over a NAMED key set, the sprint's card index sets
// s:<S>:idx:card:<state>. Nothing is scanned. Each index is one
//
//	FCALL_RO ns_census_read 0 <S> <state> <field> ...
//
// (the nova_sprint library, census.lua), so the members and their hash fields
// come back in the same reply, and every call plus one TIME (the clock the
// ages are measured on) travel in a single pipeline flush. No SORT: it is
// @dangerous and the coordinator ACL refuses it (#3620). It is read-only and
// loads nothing; the deploy loads the library (nova-sprint fn load).
//
// Output is TSV, a header then one row per card sorted by index order, then
// label:
//
//	label	state	bench	route	attempt	age
//
// state is the card hash's state field; a label whose card hash is not in
// Redis prints state MISSING (never a blank row, never a value from an earlier
// read). An absent field prints "-". age is Redis TIME minus the newest of
// beat_at, ended_at, launched_at, dealt_at (ms) and cut_at (s), rounded to the
// second. A label found in two named indexes prints once, under the first, and
// counts as dup; a card whose hash state is not the index it sits in counts as
// drift. The summary line is last:
//
//	CENSUS sprint=<S> sets=<k> cards=<n> missing=<n> dup=<n> drift=<n> round_trips=<n> ms=<ms>
//
// round_trips counts the pipeline flushes the census issued (one); ms is the
// wall time of the whole census, reads and rendering.

// CardStates is the default named key set: every card index state the
// nova_sprint library writes, in the order a card moves through them.
var CardStates = []string{
	"queued", "dealt", "launched", "running", "ended", "reconcile-required",
	"orphan-effect", "harvested", "refused", "review-ready", "land-ready", "landed",
}

// CensusReadFunction is the library function that reads one card index with
// the named hash fields of each member (census.lua).
const CensusReadFunction = "ns_census_read"

// cardCensusFields are the card hash fields read per member, after the label.
var cardCensusFields = []string{"state", "bench", "route", "attempt", "beat_at", "ended_at", "launched_at", "dealt_at", "cut_at"}

var censusTokenRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// CardCensusRequest names one card census.
type CardCensusRequest struct {
	Sprint string
	// States are the index states read, in print order; empty means CardStates.
	States []string
}

// CardCensusSummary is the CENSUS line's counts.
type CardCensusSummary struct {
	Sprint     string
	Sets       int
	Cards      int
	Missing    int
	Dup        int
	Drift      int
	RoundTrips int
	Elapsed    time.Duration
}

// Line is the CENSUS summary line.
func (s CardCensusSummary) Line() string {
	return fmt.Sprintf("CENSUS sprint=%s sets=%d cards=%d missing=%d dup=%d drift=%d round_trips=%d ms=%.1f",
		s.Sprint, s.Sets, s.Cards, s.Missing, s.Dup, s.Drift, s.RoundTrips, float64(s.Elapsed.Microseconds())/1000)
}

// Check refuses a request before any Redis read.
func (req CardCensusRequest) Check() error {
	if !censusTokenRE.MatchString(req.Sprint) {
		return fmt.Errorf("--sprint %q: want a sprint name matching [a-z0-9-]{1,40}", req.Sprint)
	}
	seen := map[string]bool{}
	for _, s := range req.States {
		if !censusTokenRE.MatchString(s) {
			return fmt.Errorf("--keys %q: want card index states, for example %s", s, strings.Join(CardStates[:3], ","))
		}
		if seen[s] {
			return fmt.Errorf("--keys names %s twice", s)
		}
		seen[s] = true
	}
	return nil
}

func (req CardCensusRequest) states() []string {
	if len(req.States) == 0 {
		return CardStates
	}
	return req.States
}

// CardCensusRow is one printed row.
type CardCensusRow struct {
	Label, Index, State, Bench, Route, Attempt string
	// Missing is a label whose card hash is not in Redis; it prints state
	// MISSING. A stored value that reads MISSING or "-" prints Go quoted.
	Missing bool
	// Age is -1 when the card has no timestamp (or its hash is missing).
	Age time.Duration
}

// String is the TSV row.
func (r CardCensusRow) String() string {
	age := "-"
	if r.Age >= 0 {
		age = r.Age.String()
	}
	if r.Missing {
		return strings.Join([]string{censusCell(r.Label), "MISSING", "-", "-", "-", "-"}, "\t")
	}
	return strings.Join([]string{censusCell(r.Label), censusCell(r.State), censusCell(r.Bench),
		censusCell(r.Route), censusCell(r.Attempt), age}, "\t")
}

func censusCell(v string) string {
	switch v {
	case "":
		return "-"
	case "-":
		return strconv.Quote(v)
	}
	return censusValue(v)
}

// CardCensusHeader is the TSV header line.
const CardCensusHeader = "label\tstate\tbench\troute\tattempt\tage"

// RunCardCensus reads every named card index of the sprint in one pipeline and
// writes the header, the rows and the CENSUS line. A refusal writes nothing.
func RunCardCensus(ctx context.Context, s *Store, req CardCensusRequest, out io.Writer) (CardCensusSummary, error) {
	start := time.Now()
	if err := req.Check(); err != nil {
		return CardCensusSummary{}, err
	}
	rows, sum, err := s.CardCensus(ctx, req)
	if err != nil {
		return CardCensusSummary{}, err
	}
	w := bufio.NewWriter(out)
	w.WriteString(CardCensusHeader)
	w.WriteByte('\n')
	for _, row := range rows {
		w.WriteString(row.String())
		w.WriteByte('\n')
	}
	sum.Elapsed = time.Since(start)
	w.WriteString(sum.Line())
	w.WriteByte('\n')
	return sum, w.Flush()
}

// CardCensus is the read: one pipeline of TIME plus one FCALL_RO
// ns_census_read per named index. Rows come back sorted by index order, then label.
func (s *Store) CardCensus(ctx context.Context, req CardCensusRequest) ([]CardCensusRow, CardCensusSummary, error) {
	if err := req.Check(); err != nil {
		return nil, CardCensusSummary{}, err
	}
	states := req.states()
	pipe := s.client.Pipeline()
	clock := pipe.Time(ctx)
	reads := make([]*redis.Cmd, len(states))
	for i, st := range states {
		args := make([]any, 0, len(cardCensusFields)+2)
		args = append(args, req.Sprint, st)
		for _, f := range cardCensusFields {
			args = append(args, f)
		}
		reads[i] = pipe.FCallRo(ctx, CensusReadFunction, nil, args...)
	}
	sum := CardCensusSummary{Sprint: req.Sprint, Sets: len(states), RoundTrips: 1}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, CardCensusSummary{}, fmt.Errorf("census read of s:%s:idx:card:{%s}: %w", req.Sprint, strings.Join(states, ","), err)
	}
	now, err := clock.Result()
	if err != nil {
		return nil, CardCensusSummary{}, fmt.Errorf("census TIME: %w", err)
	}
	nowMS := now.UnixMilli()
	width := len(cardCensusFields) + 1
	seen := map[string]bool{}
	var rows []CardCensusRow
	for i, st := range states {
		vals, err := reads[i].StringSlice()
		if err != nil {
			return nil, CardCensusSummary{}, fmt.Errorf("census %s s:%s:idx:card:%s: %w", CensusReadFunction, req.Sprint, st, err)
		}
		if len(vals)%width != 0 {
			return nil, CardCensusSummary{}, fmt.Errorf("census %s s:%s:idx:card:%s: %d values, not a multiple of %d", CensusReadFunction, req.Sprint, st, len(vals), width)
		}
		var group []CardCensusRow
		for j := 0; j < len(vals); j += width {
			label := vals[j]
			if seen[label] {
				sum.Dup++
				continue
			}
			seen[label] = true
			group = append(group, cardRow(label, st, vals[j+1:j+width], nowMS))
		}
		sort.Slice(group, func(a, b int) bool { return group[a].Label < group[b].Label })
		rows = append(rows, group...)
	}
	for _, r := range rows {
		switch {
		case r.Missing:
			sum.Missing++
		case r.State != r.Index:
			sum.Drift++
		}
	}
	sum.Cards = len(rows)
	return rows, sum, nil
}

// cardRow builds one row from the ns_census_read values, in cardCensusFields
// order. The function returns ” for a field the hash lacks and for every
// field of a hash that does not exist; a card hash always holds state, so an
// empty state is a missing hash.
func cardRow(label, index string, v []string, nowMS int64) CardCensusRow {
	row := CardCensusRow{Label: label, Index: index, State: v[0], Bench: v[1], Route: v[2], Attempt: v[3], Age: -1}
	if row.State == "" {
		return CardCensusRow{Label: label, Index: index, Missing: true, Age: -1}
	}
	newest := int64(-1)
	for _, at := range v[4:] {
		ms, ok := censusMillis(at)
		if ok && ms > newest {
			newest = ms
		}
	}
	if newest >= 0 {
		age := nowMS - newest
		if age < 0 {
			age = 0
		}
		row.Age = (time.Duration(age) * time.Millisecond).Round(time.Second)
	}
	return row
}

// censusMillis reads a card timestamp: the library writes cut_at in seconds
// and the others in milliseconds, so a value under 1e11 is seconds.
func censusMillis(at string) (int64, bool) {
	n, err := strconv.ParseInt(at, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n < 1e11 {
		n *= 1000
	}
	return n, true
}
