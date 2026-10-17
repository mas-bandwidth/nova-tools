// Package record holds the token ledger: the index of nova-tokens' day TSVs in the fleet
// Redis, one hash per day: a month query reads the day hashes in one pipelined trip, so
// the ledger keeps one store and no second copy. The rows and the
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/record/tablejson"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
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
// ReplaceLedgerDays swaps multiple days' rows in one isolated EVAL round trip.
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
// seat's password without its user is WRONGPASS.
// It silences go-redis's own logger first: a dial failure otherwise prints the
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
// a data race, so the install is serialized once per process.
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

// ReplaceLedgerDays replaces a batch in one isolated EVAL round trip. It validates
// key types and mutation/restore permissions before deleting any day, and bounds
// each HSET's argument count. A recoverable mutation error restores prior hash
// contents; restoration itself still needs a working server and available memory.
// Redis does not automatically roll back scripts or provide crash recovery here.
// A server without redis.acl_check_cmd is refused before any mutation.
func (s *RedisLedger) ReplaceLedgerDays(ctx context.Context, days []LedgerDay) error {
	if len(days) == 0 {
		return nil
	}
	prepared, err := s.prepareDays(days)
	if err != nil {
		return err
	}
	keys := make([]string, len(prepared))
	args := make([]any, 0, len(prepared)*2)
	for i, d := range prepared {
		keys[i] = d.key
		args = append(args, len(d.fields))
		args = append(args, d.fields...)
	}
	if err := s.rdb.Eval(ctx, replaceLedgerDaysScript, keys, args...).Err(); err != nil {
		if len(prepared) == 1 {
			return fmt.Errorf("%s: %w", prepared[0].key, err)
		}
		return err
	}
	return nil
}

// preparedDay is one day's hash key and its field/value pairs, ready for EVAL.
type preparedDay struct {
	key    string
	fields []any
}

// prepareDays builds a batch's table-form fields (the owner's S2 and S4, the
// writer side of the reader card): each hash field is the LedgerKey table and
// its value the LedgerValue table of schemas/ledger, written as the padded
// standard base64 of the wire where the record lives in a text line or file.
// A value a table cannot hold is refused by name before a byte is written,
// and the legacy form is never written as a fallback.
func (s *RedisLedger) prepareDays(days []LedgerDay) ([]preparedDay, error) {
	seenDays := make(map[string]bool, len(days))
	prepared := make([]preparedDay, 0, len(days))
	for _, d := range days {
		if seenDays[d.Day] {
			return nil, fmt.Errorf("the day %s appears twice in the batch", d.Day)
		}
		seenDays[d.Day] = true
		if err := CheckLedgerDay(d.Day, d.Entries); err != nil {
			return nil, err
		}
		fields := make([]any, 0, 2*len(d.Entries))
		for _, e := range d.Entries {
			k := ledgerKeyText{Day: e.Day, Card: e.Card, Model: e.Model, Repo: e.Repo}
			v := ledgerValueText{Provider: e.Provider, Rough: int64(e.Rough), Sources: e.Sources, Tokens: make([]ledgerTokenText, len(e.Tokens))}
			for i := range e.Tokens {
				if e.Known[i] {
					n := e.Tokens[i]
					v.Tokens[i].Amount = &n
				}
			}
			f, err := ledgerRead.keys.Write(&k, schemaread.FormBase64)
			if err != nil {
				return nil, err
			}
			b, err := ledgerRead.values.Write(&v, schemaread.FormBase64)
			if err != nil {
				return nil, err
			}
			if len(f) == 0 || len(b) == 0 {
				return nil, &schemaread.Refusal{Name: "LEDGER_KEY", Reason: schemaread.ReasonUnknownForm, Help: LedgerHelp}
			}
			fields = append(fields, string(f), string(b))
		}
		prepared = append(prepared, preparedDay{key: LedgerKey(d.Day), fields: fields})
	}
	return prepared, nil
}

const replaceLedgerDaysScript = `
if type(redis.acl_check_cmd) ~= 'function' then
	return redis.error_reply('ERR ledger writes require redis.acl_check_cmd permission preflight')
end

local prepared = {}
local argIdx = 1
for i = 1, #KEYS do
	local count = tonumber(ARGV[argIdx])
	if not count or count < 0 or count % 2 ~= 0 or count > #ARGV - argIdx then
		return redis.error_reply('ERR invalid ledger field count')
	end
	argIdx = argIdx + 1
	local fields = {}
	for c = 1, count do
		fields[c] = ARGV[argIdx]
		argIdx = argIdx + 1
	end
	prepared[i] = fields
end
if argIdx ~= #ARGV + 1 then
	return redis.error_reply('ERR unused ledger arguments')
end

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
	if not redis.acl_check_cmd('DEL', key) then
		return redis.error_reply('NOPERM no permission for ledger DEL ' .. key)
	end
	-- A clear may need HSET too, to restore its old hash after a later error.
	local fields = #prepared[i] > 0 and prepared[i] or backups[i].data
	if fields and #fields > 0 and not redis.acl_check_cmd('HSET', key, fields[1], fields[2]) then
		return redis.error_reply('NOPERM no permission for ledger HSET ' .. key)
	end
end

local function set_fields(key, fields)
	-- Keep unpack below Lua's stack limit, including for restoration.
	for first = 1, #fields, 256 do
		local last = math.min(first + 255, #fields)
		redis.call('HSET', key, unpack(fields, first, last))
	end
end

local status, err = pcall(function()
	for i = 1, #KEYS do
		local key = KEYS[i]
		redis.call('DEL', key)
		set_fields(key, prepared[i])
	end
end)

if not status then
	for i = 1, #KEYS do
		local key = KEYS[i]
		local b = backups[i]
		redis.call('DEL', key)
		if b and b.type == 'hash' and #b.data > 0 then
			set_fields(key, b.data)
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
		rows, err := ReadLedgerDay(d, m)
		if err != nil {
			return nil, 0, 0, err
		}
		entries = append(entries, rows...)
	}
	totals, err := GroupLedger(entries, by)
	return totals, indexed, missing, err
}

// The ledger's typed reads (v1.5 aspect 13; the owner's S2 and S4, readers
// first). A hash field is the LedgerKey table and its value the LedgerValue
// table of schemas/ledger, each read from the table wire, the padded standard
// base64 of that wire, the schema JSON text or the legacy form the writer
// writes today ([card, model, repo] and {"provider","tokens","rough",
// "sources"}). Writers stay legacy. pkg/ledgerwire's golden test imports this
// package, so the generated tables are wired in by internal/record/ledgerread.
var (
	// LedgerKeyWire and LedgerValueWire hold the generated tables an installer
	// (internal/record/ledgerread) wires in; until then a table image is
	// refused as unknown_form and the legacy form alone reads.
	LedgerKeyWire, LedgerValueWire tablejson.Slot

	// ledgerRead is the process's one set of ledger readers.
	ledgerRead = newLedgerReaders()
)

// ledgerReaders is one set of ledger readers, each counting its own reads per
// form and its refusals by reason (S2, S4: the per-form counter is the
// evidence a cutover reads). ReadLedgerDay reads through the process's one
// set; a test makes its own to count.
type ledgerReaders struct {
	keys   *schemaread.Reader[ledgerKeyText]
	values *schemaread.Reader[ledgerValueText]
	// unknownKeys and unknownValues count the ledger fields the strict read
	// accepted with an unknown field, which readDay refuses.
	unknownKeys, unknownValues atomic.Uint64
}

// newLedgerReaders is a set of ledger readers with empty counters.
func newLedgerReaders() *ledgerReaders {
	return &ledgerReaders{
		keys:   schemaread.NewReader(tablejson.Codec("LEDGER_KEY", LedgerHelp, &LedgerKeyWire, ledgerKeyIsLegacy, ledgerKeyLoadLegacy, ledgerKeyConstraints)),
		values: schemaread.NewReader(tablejson.Codec("LEDGER_VALUE", LedgerHelp, &LedgerValueWire, ledgerValueIsLegacy, ledgerValueLoadLegacy, ledgerValueConstraints)),
	}
}

// LedgerHelp is the command a ledger refusal points at.
const LedgerHelp = "nova-tokens ledger -h"

// ledgerKeyText is a hash field in the shape of the LedgerKey table's JSON
// text. A legacy field names no day: the hash's key carries it.
type ledgerKeyText struct {
	Day   string `json:"day"`
	Card  string `json:"card"`
	Model string `json:"model"`
	Repo  string `json:"repo"`
}

// ledgerTokenText is one TokenValue slot; a nil amount is a type no source
// reported.
type ledgerTokenText struct {
	Amount *int64 `json:"amount,omitempty"`
}

// ledgerValueText is a hash value in the shape of the LedgerValue table's
// JSON text: the five slots of LedgerTypes in order.
type ledgerValueText struct {
	Provider string            `json:"provider"`
	Tokens   []ledgerTokenText `json:"tokens"`
	Rough    int64             `json:"rough"`
	Sources  string            `json:"sources"`
}

// ledgerKeyIsLegacy: the legacy field is the JSON array [card, model, repo].
func ledgerKeyIsLegacy(in []byte) bool {
	t := bytes.TrimSpace(in)
	return len(t) > 0 && t[0] == '['
}

// ledgerKeyLoadLegacy reads [card, model, repo]: exactly three strings,
// anything else malformed, never a row read short.
func ledgerKeyLoadLegacy(v *ledgerKeyText, in []byte) schemaread.Report {
	var k []string
	if err := json.Unmarshal(in, &k); err != nil || len(k) != 3 {
		return schemaread.Report{Malformed: true}
	}
	*v = ledgerKeyText{Card: k[0], Model: k[1], Repo: k[2]}
	return schemaread.Report{}
}

// ledgerKeyConstraints: the key names its card, model and repo (ErrLedgerKey),
// and its day is string(10) (schemas/ledger LedgerKey).
func ledgerKeyConstraints(v *ledgerKeyText, c *schemaread.Checker) {
	c.Text("day", v.Day, 10)
	for _, f := range []struct{ name, s string }{{"card", v.Card}, {"model", v.Model}, {"repo", v.Repo}} {
		c.Required(f.name, f.s != "")
		c.Text(f.name, f.s, math.MaxInt)
	}
}

// ledgerValueIsLegacy: the legacy value is a JSON object whose tokens are
// numbers and nulls; the table's JSON text wraps each slot as an object.
func ledgerValueIsLegacy(in []byte) bool {
	t := bytes.TrimSpace(in)
	var probe struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if len(t) == 0 || t[0] != '{' || json.Unmarshal(t, &probe) != nil {
		return false
	}
	return !slices.ContainsFunc(probe.Tokens, func(m json.RawMessage) bool { return bytes.HasPrefix(bytes.TrimSpace(m), []byte("{")) })
}

// ledgerValueLoadLegacy reads the legacy value: the five token slots exactly,
// each a number or null.
func ledgerValueLoadLegacy(v *ledgerValueText, in []byte) schemaread.Report {
	var lv struct {
		Provider string   `json:"provider"`
		Tokens   []*int64 `json:"tokens"`
		Rough    int64    `json:"rough"`
		Sources  string   `json:"sources"`
	}
	if err := json.Unmarshal(in, &lv); err != nil || len(lv.Tokens) != len(LedgerTypes) {
		return schemaread.Report{Malformed: true}
	}
	*v = ledgerValueText{Provider: lv.Provider, Rough: lv.Rough, Sources: lv.Sources, Tokens: make([]ledgerTokenText, len(lv.Tokens))}
	for i, n := range lv.Tokens {
		v.Tokens[i].Amount = n
	}
	// A key the value does not name is counted as unknown, so ReadLedgerDay
	// refuses it (ledgerReaders.unknown) rather than read a damaged key's value as
	// absent.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(in, &keys); err != nil {
		return schemaread.Report{Malformed: true}
	}
	unknown := 0
	for k := range keys {
		if !slices.Contains([]string{"provider", "tokens", "rough", "sources"}, k) {
			unknown++
		}
	}
	return schemaread.Report{Unknown: unknown}
}

// ledgerValueConstraints: at most the five slots of LedgerTypes
// (schemas/ledger LedgerValue tokens [..5]), and a rough count an int holds.
func ledgerValueConstraints(v *ledgerValueText, c *schemaread.Checker) {
	c.Bound("tokens", int64(len(v.Tokens)), 0, int64(len(LedgerTypes)))
	c.Bound("rough", v.Rough, math.MinInt, math.MaxInt)
	c.Text("provider", v.Provider, math.MaxInt)
	c.Text("sources", v.Sources, math.MaxInt)
}

// ReadLedgerDay reads one day hash's fields into its rows, in field order. A
// field or value of any form that does not read is an error naming the key
// and the refusal (`LEDGER_KEY REFUSED: <reason>; run: nova-tokens ledger -h`),
// never a skipped row, and a key whose table names another day than the hash
// is refused. Each read is counted under its record and its form
// (the process's ledgerReaders).
func ReadLedgerDay(day string, fields map[string]string) ([]LedgerEntry, error) {
	return ledgerRead.readDay(day, fields)
}

// readDay is ReadLedgerDay through this set of readers.
func (r *ledgerReaders) readDay(day string, fields map[string]string) ([]LedgerEntry, error) {
	key := LedgerKey(day)
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)
	out := make([]LedgerEntry, 0, len(names))
	for _, f := range names {
		k, kr, err := r.keys.Read([]byte(f))
		if err == nil && kr.Unknown > 0 {
			err = r.unknown("LEDGER_KEY")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: field %q is not [card, model, repo]: %w", key, f, err)
		}
		if k.Day != "" && k.Day != day {
			return nil, fmt.Errorf("%s: field %q: %s REFUSED: %s (day %s, not the hash's %s); run: %s", key, f, "LEDGER_KEY", schemaread.ReasonMalformed, k.Day, day, LedgerHelp)
		}
		v, vr, err := r.values.Read([]byte(fields[f]))
		if err == nil && vr.Unknown > 0 {
			err = r.unknown("LEDGER_VALUE")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: field %s: value does not decode: %w", key, f, err)
		}
		e := LedgerEntry{Day: day, Card: k.Card, Model: k.Model, Repo: k.Repo, Provider: v.Provider, Rough: int(v.Rough), Sources: v.Sources}
		for t, n := range v.Tokens {
			if n.Amount != nil {
				e.Tokens[t], e.Known[t] = *n.Amount, true
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// unknown is the named refusal of a ledger key or value that holds a
// field no reader names. Readers switch before writers, fleet-wide (the
// owner's S4), so every reader knows every field a ledger writer writes, and
// an unknown id is damage: tolerated, it would read a damaged provider,
// tokens, rough or sources key as an absent, zero one, and a ledger day never
// reads damage as zero. Unknown fields stay tolerated by pkg/schemaread for
// the records whose older readers may meet newer data (S1).
func (r *ledgerReaders) unknown(rec string) error {
	if rec == "LEDGER_KEY" {
		r.unknownKeys.Add(1)
	} else {
		r.unknownValues.Add(1)
	}
	return &schemaread.Refusal{Name: rec, Reason: schemaread.ReasonMalformed, Field: "unknown_field", Help: LedgerHelp}
}
