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

// LedgerDay is one calendar day's batch of rows for ReplaceLedgerDays.
type LedgerDay struct {
	Day     string
	Entries []LedgerEntry
}

// LedgerStore is the durable side of the token ledger. ReplaceLedgerDay swaps one day's rows
// for the given ones atomically, so indexing a day twice is the same ledger as once.
// ReplaceLedgerDays swaps multiple days' rows atomically in one pipelined round trip (#7fbdefecf56e).
// LedgerReport is the monthly GROUP BY; indexed is how many calendar-day keys existed and
// missing is how many did not.
type LedgerStore interface {
	ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error
	ReplaceLedgerDays(ctx context.Context, days []LedgerDay) error
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

// Client returns the underlying redis.Client.
func (s *RedisLedger) Client() *redis.Client { return s.rdb }

// DialLedger opens a client on addr (host:port) as the ACL user with its password ("" for
// the default user, "" for no password). The fleet Redis has its default user off, so a
// seat's password without its user is WRONGPASS (#3461).
// It silences go-redis's own logger first (#3463): a dial failure otherwise prints the
// library's untyped, local-time "connection pool: failed to dial after 5 attempts" lines
// to the process's stderr ahead of the verb's one typed FAILED line. The
// error they carry is not lost: it is the error the first command returns, which the verb prints.
func DialLedger(addr, user, password string) *RedisLedger {
	silenceRedisLogger()
	return NewRedisLedger(redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password}))
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

// ReplaceLedgerDay swaps one day's rows by delegating to ReplaceLedgerDays.
func (s *RedisLedger) ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error {
	return s.ReplaceLedgerDays(ctx, []LedgerDay{{Day: day, Entries: entries}})
}

// ReplaceLedgerDays writes all given days in one EVAL Lua script, DEL then HSET for each day,
// so all days in the batch are written atomically in one round trip and any runtime error
// reverts all keys to their pre-existing state (#7fbdefecf56e, johnny-a228317dbe3b).
func (s *RedisLedger) ReplaceLedgerDays(ctx context.Context, days []LedgerDay) error {
	if len(days) == 0 {
		return nil
	}
	seenDays := make(map[string]bool, len(days))
	type preparedDay struct {
		key    string
		fields []any
	}
	prepared := make([]preparedDay, 0, len(days))
	for _, d := range days {
		if seenDays[d.Day] {
			return fmt.Errorf("the day %s appears twice in the batch", d.Day)
		}
		seenDays[d.Day] = true
		if err := CheckLedgerDay(d.Day, d.Entries); err != nil {
			return err
		}
		fields := make([]any, 0, 2*len(d.Entries))
		for _, e := range d.Entries {
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
		prepared = append(prepared, preparedDay{key: LedgerKey(d.Day), fields: fields})
	}
	keys := make([]string, len(prepared))
	args := make([]any, 0, len(prepared)*2)
	for i, d := range prepared {
		keys[i] = d.key
		args = append(args, len(d.fields))
		for _, f := range d.fields {
			args = append(args, f)
		}
	}
	if err := s.rdb.Eval(ctx, replaceLedgerDaysScript, keys, args...).Err(); err != nil {
		if len(prepared) == 1 {
			return fmt.Errorf("%s: %w", prepared[0].key, err)
		}
		return err
	}
	return nil
}

const replaceLedgerDaysScript = `
local backups = {}
for i = 1, #KEYS do
	local key = KEYS[i]
	local t = redis.call('TYPE', key)['ok'] or redis.call('TYPE', key)
	if t == 'hash' then
		backups[i] = { type = 'hash', data = redis.call('HGETALL', key) }
	elseif t == 'none' then
		backups[i] = { type = 'none' }
	else
		return redis.error_reply("WRONGTYPE Operation against a key holding the wrong kind of value")
	end
end

local status, err = pcall(function()
	local argIdx = 1
	for i = 1, #KEYS do
		local key = KEYS[i]
		local count = tonumber(ARGV[argIdx])
		argIdx = argIdx + 1
		redis.call('DEL', key)
		if count > 0 then
			local fields = {}
			for c = 1, count do
				fields[c] = ARGV[argIdx]
				argIdx = argIdx + 1
			end
			redis.call('HSET', key, unpack(fields))
		end
	end
end)

if not status then
	for i = 1, #KEYS do
		local key = KEYS[i]
		local b = backups[i]
		redis.call('DEL', key)
		if b and b.type == 'hash' and #b.data > 0 then
			for k = 1, #b.data, 2 do
				redis.call('HSET', key, b.data[k], b.data[k+1])
			end
		end
	end
	error(err)
end

return "OK"
`

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
	// One round trip. If a specific command failed, report it under its key;
	// otherwise report the pipeline's execution error (e.g. auth or dial failure).
	if _, err := pipe.Exec(ctx); err != nil {
		for i, d := range days {
			if cmdErr := cmds[i].Err(); cmdErr != nil {
				return nil, 0, 0, fmt.Errorf("%s: %w", LedgerKey(d), cmdErr)
			}
		}
		return nil, 0, 0, err
	}
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
