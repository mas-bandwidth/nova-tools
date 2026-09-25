// Package record holds the token ledger: the index of nova-tokens' day TSVs in the fleet
// Redis, one hash per day (docs/SPEC-STATE.md test 17, #2201). Draft 1 of SPEC-STATE put it
// in a Postgres table (#3243); Postgres was retired under #2623, so the same rows and the
// same monthly GROUP BY live here on Redis with no new dependency. The day files stay the
// record; the ledger is what a month query reads instead of every file.
//
// KEY LAYOUT. tokens:ledger:<YYYY-MM-DD> is a hash. Each field is one row's key, the JSON
// array ["<card>","<model>","<repo>"], so (day, card, model, repo) is the primary key; each
// value is the JSON object {"provider","tokens","rough","sources"} whose "tokens" array
// carries the five types in LedgerTypes order with null for a type no source reported --
// never 0, because a dash in the day file is an absence. A month is the day keys of its
// calendar days, read in one pipelined round trip: no SCAN, no index key to keep true.
package record

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// LedgerPrefix is the owner prefix of every key this package writes.
const LedgerPrefix = "tokens:ledger:"

// LedgerKey is the hash that holds one day's rows.
func LedgerKey(day string) string { return LedgerPrefix + day }

// LedgerTypes are the five token types in the order every row and report prints them.
var LedgerTypes = [5]string{"input", "output", "cache_write", "cache_read", "reasoning"}

// LedgerEntry is one ledger row. Tokens[i] counts only when Known[i].
type LedgerEntry struct {
	Day, Card, Model, Repo string
	Provider               string
	Tokens                 [5]int64
	Known                  [5]bool
	Rough                  int
	Sources                string
}

// LedgerTotal is one GROUP BY group: the key columns the report grouped on (empty when not
// grouped on), how many ledger rows it summed, and each type's sum and whether any row
// reported it.
type LedgerTotal struct {
	Day, Model, Repo string
	Rows             int
	Tokens           [5]int64
	Known            [5]bool
}

// LedgerStore is the durable side of the token ledger. ReplaceLedgerDay swaps one day's rows
// for the given ones atomically, so indexing a day twice is the same ledger as once.
// LedgerReport is the monthly GROUP BY; indexed is how many calendar-day keys existed and
// missing is how many did not.
type LedgerStore interface {
	ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error
	LedgerReport(ctx context.Context, month, by string) ([]LedgerTotal, int, int, error)
	Close() error
}

// LedgerGroupings are the --by values the report takes, and the key columns each groups on.
var LedgerGroupings = map[string][]string{
	"model": {"model"},
	"repo":  {"repo"},
	"day":   {"day"},
	"tuple": {"day", "model", "repo"},
}

// ErrLedgerKey is a row that does not name its whole key.
var ErrLedgerKey = errors.New("a ledger row names day, card, model and repo")

// CheckLedgerDay refuses a day that is not a calendar day (it names a key), and a batch that
// is not all of that day or repeats a key: a caller that meant two rows under one key meant
// their sum.
func CheckLedgerDay(day string, entries []LedgerEntry) error {
	if t, err := time.Parse(time.DateOnly, day); err != nil || t.Format(time.DateOnly) != day {
		return fmt.Errorf("day %q is not YYYY-MM-DD", day)
	}
	seen := map[[4]string]bool{}
	for _, e := range entries {
		if e.Day == "" || e.Card == "" || e.Model == "" || e.Repo == "" {
			return ErrLedgerKey
		}
		if e.Day != day {
			return fmt.Errorf("a row of day %s in the batch for day %s", e.Day, day)
		}
		k := [4]string{e.Day, e.Card, e.Model, e.Repo}
		if seen[k] {
			return fmt.Errorf("the key (%s) appears twice in one day; sum it first", strings.Join(k[:], ", "))
		}
		seen[k] = true
	}
	return nil
}

// SortLedgerTotals is the report's order: by the key columns ascending.
func SortLedgerTotals(t []LedgerTotal) {
	sort.SliceStable(t, func(i, j int) bool {
		a, b := t[i], t[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Repo < b.Repo
	})
}

// GroupLedger is the GROUP BY: rows summed per the key columns of by, each type the SUM over
// the rows that reported it and unknown when none did -- the fold's per-type rule.
func GroupLedger(entries []LedgerEntry, by string) ([]LedgerTotal, error) {
	cols, ok := LedgerGroupings[by]
	if !ok {
		return nil, fmt.Errorf("--by %q is not one of model, repo, day, tuple", by)
	}
	groups := map[[3]string]*LedgerTotal{}
	for _, e := range entries {
		var k [3]string
		for _, c := range cols {
			switch c {
			case "day":
				k[0] = e.Day
			case "model":
				k[1] = e.Model
			case "repo":
				k[2] = e.Repo
			}
		}
		g := groups[k]
		if g == nil {
			g = &LedgerTotal{Day: k[0], Model: k[1], Repo: k[2]}
			groups[k] = g
		}
		g.Rows++
		for i := range e.Tokens {
			if e.Known[i] {
				g.Tokens[i] += e.Tokens[i]
				g.Known[i] = true
			}
		}
	}
	out := make([]LedgerTotal, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	SortLedgerTotals(out)
	return out, nil
}

// MonthDays are the calendar days of a YYYY-MM month.
func MonthDays(month string) ([]string, error) {
	first, err := time.Parse("2006-01", month)
	if err != nil || first.Format("2006-01") != month {
		return nil, fmt.Errorf("month %q is not YYYY-MM", month)
	}
	var days []string
	for d := first; d.Month() == first.Month(); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(time.DateOnly))
	}
	return days, nil
}

// ledgerValue is a hash field's value; a nil token is a type no source reported.
type ledgerValue struct {
	Provider string    `json:"provider"`
	Tokens   [5]*int64 `json:"tokens"`
	Rough    int       `json:"rough"`
	Sources  string    `json:"sources"`
}

// RedisLedger is the ledger on the fleet Redis. It dials nothing itself: the caller opens
// the client, and Close closes it.
type RedisLedger struct {
	rdb *redis.Client
}

// NewRedisLedger wraps a client the caller opened.
func NewRedisLedger(rdb *redis.Client) *RedisLedger { return &RedisLedger{rdb: rdb} }

// DialLedger opens a client on addr (host:port) with the given password ("" for none).
// It silences go-redis's own logger first (#3463): a dial failure otherwise prints the
// library's untyped, local-time "connection pool: failed to dial after 5 attempts" lines
// to the process's stderr ahead of the verb's one typed FAILED line. The
// error they carry is not lost: it is the error Ping returns, which the verb prints.
func DialLedger(addr, password string) *RedisLedger {
	silenceRedisLogger()
	return NewRedisLedger(redis.NewClient(&redis.Options{Addr: addr, Password: password}))
}

// quietRedis is the discard logger for go-redis.
type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

// silenceRedisLoggerOnce makes the install once per process: redis.SetLogger writes a
// package-level variable inside go-redis, and two dials in one process writing it again is
// a data race (the same finding as nova-merge's #1609).
var silenceRedisLoggerOnce sync.Once

func silenceRedisLogger() { silenceRedisLoggerOnce.Do(func() { redis.SetLogger(quietRedis{}) }) }

// Ping is one PING, so a verb names an unreachable store before it reads any file.
func (s *RedisLedger) Ping(ctx context.Context) error { return s.rdb.Ping(ctx).Err() }

// Close closes the client.
func (s *RedisLedger) Close() error { return s.rdb.Close() }

// ReplaceLedgerDay is DEL then HSET of the day's hash in one MULTI/EXEC, so a re-fold's
// re-index replaces the day rather than merging into it, and no reader sees half a day.
func (s *RedisLedger) ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error {
	if err := CheckLedgerDay(day, entries); err != nil {
		return err
	}
	fields := make([]any, 0, 2*len(entries))
	for _, e := range entries {
		f, err := json.Marshal([3]string{e.Card, e.Model, e.Repo})
		if err != nil {
			return err
		}
		v := ledgerValue{Provider: e.Provider, Rough: e.Rough, Sources: e.Sources}
		for i := range e.Tokens {
			if e.Known[i] {
				n := e.Tokens[i]
				v.Tokens[i] = &n
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		fields = append(fields, string(f), string(b))
	}
	key := LedgerKey(day)
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key)
		if len(fields) > 0 {
			p.HSet(ctx, key, fields...)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// LedgerReport reads every day hash of the month in one pipelined round trip and groups it.
// A field or value that does not decode is an error naming its key, never a skipped row.
// It returns the totals, how many calendar-day keys existed (indexed), and how many did not
// (missing).
func (s *RedisLedger) LedgerReport(ctx context.Context, month, by string) ([]LedgerTotal, int, int, error) {
	if _, ok := LedgerGroupings[by]; !ok {
		return nil, 0, 0, fmt.Errorf("--by %q is not one of model, repo, day, tuple", by)
	}
	days, err := MonthDays(month)
	if err != nil {
		return nil, 0, 0, err
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(days))
	for i, d := range days {
		cmds[i] = pipe.HGetAll(ctx, LedgerKey(d))
	}
	// One round trip. A failed command's error is on its own cmd and is reported below
	// under its key, so Exec's first-error summary is not the one returned.
	_, _ = pipe.Exec(ctx)
	var entries []LedgerEntry
	indexed, missing := 0, 0
	for i, d := range days {
		key := LedgerKey(d)
		m, err := cmds[i].Result()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%s: %w", key, err)
		}
		if len(m) == 0 {
			missing++
			continue
		}
		indexed++
		for f, raw := range m {
			var k [3]string
			if err := json.Unmarshal([]byte(f), &k); err != nil || k[0] == "" || k[1] == "" || k[2] == "" {
				return nil, 0, 0, fmt.Errorf("%s: field %q is not [card, model, repo]", key, f)
			}
			var v ledgerValue
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return nil, 0, 0, fmt.Errorf("%s: field %s: value does not decode: %w", key, f, err)
			}
			e := LedgerEntry{Day: d, Card: k[0], Model: k[1], Repo: k[2], Provider: v.Provider, Rough: v.Rough, Sources: v.Sources}
			for t, n := range v.Tokens {
				if n != nil {
					e.Tokens[t], e.Known[t] = *n, true
				}
			}
			entries = append(entries, e)
		}
	}
	totals, err := GroupLedger(entries, by)
	return totals, indexed, missing, err
}
