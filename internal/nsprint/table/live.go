// live.go: the live layout of nova-sprint table (#2674), a Go port of the
// bash of record rowan-tools bin/sprint-table-redis. It reads ONLY the keys
// that script reads today and prints its layout byte for byte:
//
//	SPRINT TABLE / blocked: n / landed: x/y z% -> eta / sprint: x/y z% -> eta /
//	blank / the bench block / two blanks / the friend block.
//
// Keys (all read-only; each has one writer elsewhere):
//
//	friend:<f>            HMGET at up ready working done   (friend-row)
//	friend:<f>:down       GET, a set value prints "down"   (out-of-credits)
//	sprint:<S>:xy         the sprint line                  (sprint-xy)
//	sprint:<S>:landed     the landed line                  (sprint-landed)
//	q:blocked             ZCARD, the one blocked count     (friend-queue, #3219)
//	bench:*               SCAN COUNT 1000, then HGETALL    (bench-row, card-dealer)
//
// Bench keys are found by SCAN, as the bash does (a cursor walk; the bench
// ACL user has no KEYS and no EVAL_RO); every value is then read in ONE
// pipelined round trip.
package table

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// LiveConfig names what the bash hard-codes; nothing here is compiled in.
type LiveConfig struct {
	Friends  []string      // the roster, in display order
	Sprint   string        // sprint:<Sprint>:xy and sprint:<Sprint>:landed
	XYFile   string        // SPRINT-XY.txt fallback while the xy key is missing (until #2679)
	RowStale time.Duration // a friend row older than this prints "stale" (bash ROW_STALE_S=10)
	// BenchStale: a host row whose own at is older than this prints "stale";
	// zero is 2 x BenchBeatInterval (#3372). The bash of record used 60 s.
	BenchStale time.Duration
}

// BenchBeatInterval is the cadence a bench writes its host row at.
const BenchBeatInterval = time.Second

// FriendRow is one friend:<f> hash plus its down flag, raw.
type FriendRow struct {
	Name                         string
	At, Up, Ready, Working, Done string
	Down                         string
}

// BenchRow is one bench:<b> hash, values already sanitized the way the bash's
// awk does.
type BenchRow struct {
	Key    string // the key suffix, bench:<Key>
	Fields map[string]string
}

// LiveSnapshot is one read of the live keyspace.
type LiveSnapshot struct {
	Config      LiveConfig
	Friends     []FriendRow
	XY          string // "" when the key is missing
	Landed      string // "" when the key is missing
	Blocked     string // "" when the count did not come back
	Benches     []BenchRow
	Pool        string // bench:pool queue; PoolPresent says whether the key exists
	PoolPresent bool
	// XYFileLine / XYFileMod are the fallback file, read only when XY == "".
	XYFileLine string
	XYFileMod  time.Time
	XYFileOK   bool
	// Stale is set by the loop when this tick's read failed and the friend,
	// xy and landed values are the last good read (LastGood is when).
	Stale    bool
	LastGood time.Time
}

// ReadLive reads the live keyspace: the SCAN cursor walk for bench:*, then
// one pipeline for every value.
func ReadLive(ctx context.Context, client redis.UniversalClient, cfg LiveConfig) (*LiveSnapshot, error) {
	var keys []string
	var cursor uint64
	for {
		page, next, err := client.Scan(ctx, cursor, "bench:*", 1000).Result()
		if err != nil {
			return nil, fmt.Errorf("scan bench:*: %w", err)
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	benchKeys := dedupSorted(keys)

	pipe := client.Pipeline()
	type friendCmds struct {
		row  *redis.SliceCmd
		down *redis.StringCmd
		// #3206 PR A: ns_friend_down writes a HASH {reason, actor, at}; the
		// GET above fails WRONGTYPE on it (tolerated), so its reason and
		// existence are read beside the legacy string.
		downReason *redis.StringCmd
		downExists *redis.IntCmd
	}
	fc := make([]friendCmds, len(cfg.Friends))
	for i, name := range cfg.Friends {
		fc[i].row = pipe.HMGet(ctx, "friend:"+name, "at", "up", "ready", "working", "done")
		fc[i].down = pipe.Get(ctx, "friend:"+name+":down")
		fc[i].downReason = pipe.HGet(ctx, "friend:"+name+":down", "reason")
		fc[i].downExists = pipe.Exists(ctx, "friend:"+name+":down")
	}
	mget := pipe.MGet(ctx, "sprint:"+cfg.Sprint+":xy", "sprint:"+cfg.Sprint+":landed")
	blocked := pipe.ZCard(ctx, "q:blocked")
	hashes := make([]*redis.MapStringStringCmd, len(benchKeys))
	for i, key := range benchKeys {
		hashes[i] = pipe.HGetAll(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, fmt.Errorf("pipeline: %w", err)
	}
	// The friend block is all-or-nothing, as in the bash: a failed MGET
	// means the connection is not answering.
	vals, err := mget.Result()
	if err != nil || len(vals) != 2 {
		return nil, fmt.Errorf("mget xy/landed: %v", err)
	}
	snap := &LiveSnapshot{Config: cfg, XY: pipeValue(vals[0]), Landed: pipeValue(vals[1])}
	for i, name := range cfg.Friends {
		row := FriendRow{Name: name}
		if got, err := fc[i].row.Result(); err == nil && len(got) == 5 {
			row.At, row.Up, row.Ready, row.Working, row.Done = pipeValue(got[0]), pipeValue(got[1]), pipeValue(got[2]), pipeValue(got[3]), pipeValue(got[4])
		}
		if got, err := fc[i].down.Result(); err == nil {
			row.Down = flatten(got)
		} else if fc[i].downExists.Val() == 1 {
			row.Down = flatten(fc[i].downReason.Val())
			if row.Down == "" {
				row.Down = "down"
			}
		}
		snap.Friends = append(snap.Friends, row)
	}
	if n, err := blocked.Result(); err == nil {
		snap.Blocked = strconv.FormatInt(n, 10)
	}
	for i, key := range benchKeys {
		h, err := hashes[i].Result()
		if key == "bench:pool" {
			snap.PoolPresent = true
			snap.Pool = "0"
			if err == nil && sanitize(h["queue"]) != "" {
				snap.Pool = sanitize(h["queue"])
			}
			continue
		}
		if err != nil || len(h) == 0 {
			continue
		}
		fields := make(map[string]string, len(h))
		for f, v := range h {
			fields[f] = sanitize(v)
		}
		snap.Benches = append(snap.Benches, BenchRow{Key: strings.TrimPrefix(key, "bench:"), Fields: fields})
	}
	if snap.XY == "" && cfg.XYFile != "" {
		snap.XYFileLine, snap.XYFileMod, snap.XYFileOK = readXYFile(cfg.XYFile)
	}
	return snap, nil
}

// FailedLive is the tick whose read failed: the friend, xy and landed values
// of last (nil when there never was a good read), no bench rows, blocked "?",
// and a stale line (the bash's LAST_FROWS path).
func FailedLive(cfg LiveConfig, last *LiveSnapshot) *LiveSnapshot {
	snap := &LiveSnapshot{Config: cfg, Stale: true}
	if last != nil {
		snap.Friends, snap.XY, snap.Landed, snap.LastGood = last.Friends, last.XY, last.Landed, last.LastGood
	}
	if snap.XY == "" && cfg.XYFile != "" {
		snap.XYFileLine, snap.XYFileMod, snap.XYFileOK = readXYFile(cfg.XYFile)
	}
	return snap
}

const liveBenchRule = "-----------+-------+---------+-------+-------+-------+------+-------\n"
const liveFriendRule = "-----------+-------+---------+-------+------------\n"

// RenderLive prints the bash layout at now.
func (s *LiveSnapshot) RenderLive(now time.Time) string {
	var b strings.Builder
	b.WriteString("SPRINT TABLE\n")
	if s.Blocked == "" || strings.Trim(s.Blocked, "0123456789") != "" {
		b.WriteString("blocked: ?\n")
	} else {
		b.WriteString("blocked: " + s.Blocked + "\n")
	}
	b.WriteString(FormatLanded(s.Landed, now) + "\n")
	if s.XY != "" {
		b.WriteString("sprint: " + s.XY + "\n")
	} else if s.XYFileOK {
		b.WriteString("sprint: " + s.XYFileLine + "\n")
	} else {
		b.WriteString("sprint: SPRINT ? (sprint-xy has not written yet)\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n", "host", "ready", "working", "done", "ok", "fail", "ok%", "load")
	b.WriteString(liveBenchRule)
	var tq, tw, td, to, tf int64
	for _, row := range s.Benches {
		f := row.Fields
		if f["host"] != row.Key {
			continue // a hash without its OWN host field (expired, partial) is not a row
		}
		if s.benchRowStale(f, now) {
			fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n", f["host"], "stale", "?", "?", "?", "?", "?", "?")
			continue
		}
		queue := strconv.FormatInt(awkInt(f["queue"]), 10)
		if dq, dat := f["dealer_queue"], f["dealer_at"]; dq != "" && dat != "" {
			if at, ok := parseUTC(dat); ok {
				if age := now.Unix() - at.Unix(); age >= 0 && age <= 30 {
					queue = dq
				}
			}
		}
		working, done, ok, fail := awkInt(f["working"]), awkInt(f["done"]), awkInt(f["ok"]), awkInt(f["fail"])
		load := f["load1"]
		if load == "" {
			load = "-"
		}
		var pct int64
		if done > 0 {
			pct = 100 * ok / done
		}
		fmt.Fprintf(&b, "%-10s | %5s | %7d | %5d | %5d | %5d | %3d%% | %6s\n", f["host"], queue, working, done, ok, fail, pct, load)
		qn, _ := strconv.ParseInt(queue, 10, 64)
		tq, tw, td, to, tf = tq+qn, tw+working, td+done, to+ok, tf+fail
	}
	b.WriteString(liveBenchRule)
	var tpct int64
	if td > 0 {
		tpct = 100 * to / td
	}
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5d | %5d | %5d | %3d%% |\n", "total", tq, tw, td, to, tf, tpct)
	if s.PoolPresent {
		fmt.Fprintf(&b, "pool: %s undealt\n", s.Pool)
	}
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", "friend", "ready", "working", "done", "status")
	b.WriteString(liveFriendRule)
	var fq, fw, fd int64
	if len(s.Friends) == 0 {
		for _, name := range s.Config.Friends {
			fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", name, "-", "-", "-", "?")
		}
	}
	for _, row := range s.Friends {
		q, w, d := orDash(row.Ready), orDash(row.Working), orDash(row.Done)
		fq, fw, fd = fq+digitsOnly(q), fw+digitsOnly(w), fd+digitsOnly(d)
		fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", row.Name, q, w, d, s.friendStatus(row, now))
	}
	b.WriteString(liveFriendRule)
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5d |\n", "total", fq, fw, fd)
	if s.Stale {
		if !s.LastGood.IsZero() {
			fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
		} else {
			b.WriteString("stale: never read (Redis did not answer since start)\n")
		}
	}
	if s.XY == "" {
		if s.XYFileOK {
			fmt.Fprintf(&b, "stale: %ds (xy key missing: sprint-xy has not written for >180s; the x/y above is SPRINT-XY.txt)\n", now.Unix()-s.XYFileMod.Unix())
		} else {
			b.WriteString("stale: xy key missing and no SPRINT-XY.txt (sprint-xy is not running)\n")
		}
	}
	return b.String()
}

// benchRowStale is the host row's age rule (#3372): a row whose own at is
// older than Config.BenchStale (2 beat intervals by default) prints "stale"
// with no numbers and no share of the totals; it never vanishes and never
// shows old numbers. An unparseable at shows the row; an at ahead of now is
// clock skew and counts as fresh.
func (s *LiveSnapshot) benchRowStale(f map[string]string, now time.Time) bool {
	at, ok := parseUTC(f["at"])
	if !ok {
		return false
	}
	limit := s.Config.BenchStale
	if limit <= 0 {
		limit = 2 * BenchBeatInterval
	}
	return now.Sub(at) > limit
}

// friendStatus: down (the down flag) | ? (no row) | stale (older than
// RowStale) | down (up != 1) | up. No ages (Glenn 2026-09-23 3:30 PM ET).
func (s *LiveSnapshot) friendStatus(row FriendRow, now time.Time) string {
	if row.Down != "" {
		return "down"
	}
	digits := keepDigits(row.At)
	if len(digits) > 14 {
		digits = digits[:14]
	}
	if len(digits) != 14 {
		return "?"
	}
	at, err := time.ParseInLocation("20060102150405", digits, time.UTC)
	if err != nil {
		return "?"
	}
	age := now.Unix() - at.Unix()
	if age < 0 {
		age = 0
	}
	stale := s.Config.RowStale
	if stale <= 0 {
		stale = 10 * time.Second
	}
	if age > int64(stale/time.Second) {
		return "stale"
	}
	if row.Up != "1" {
		return "down"
	}
	return "up"
}

var (
	landedWithETA = regexp.MustCompile(`^([0-9]+/[0-9]+) landed ([0-9]+%) \(.*\) eta=([^ ]*) at=(.*)$`)
	landedNoETA   = regexp.MustCompile(`^([0-9]+/[0-9]+) landed ([0-9]+%) \(.*\) at=(.*)$`)
)

// FormatLanded reshapes sprint-landed's value into the table line; "landed: ?"
// when it is missing, unparseable or its own at= is older than 180 s.
func FormatLanded(raw string, now time.Time) string {
	if raw == "" || raw == "-" || raw == "(nil)" {
		return "landed: ?"
	}
	var xy, pct, eta, at string
	if m := landedWithETA.FindStringSubmatch(raw); m != nil {
		xy, pct, eta, at = m[1], m[2], m[3], m[4]
	} else if m := landedNoETA.FindStringSubmatch(raw); m != nil {
		xy, pct, eta, at = m[1], m[2], "?", m[3]
	} else {
		return "landed: ?"
	}
	if t, ok := parseUTC(at); ok && now.Unix()-t.Unix() > 180 {
		return "landed: ?"
	}
	if eta == "" {
		eta = "?"
	}
	return "landed: " + xy + " " + pct + " -> " + eta
}

func readXYFile(path string) (string, time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", info.ModTime(), false
	}
	defer file.Close()
	line, _ := bufio.NewReader(file).ReadString('\n')
	return strings.TrimSuffix(line, "\n"), info.ModTime(), true
}

func parseUTC(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	return t, err == nil
}

func dedupSorted(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	var out []string
	for _, k := range keys {
		if k == "bench:stuck_done" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// pipeValue is redis-pipe.bash's rendering of one reply: nil -> "", CR/LF -> space.
func pipeValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return flatten(t)
	default:
		return flatten(fmt.Sprint(t))
	}
}

func flatten(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func orDash(v string) string {
	if v == "" || v == "(nil)" {
		return "-"
	}
	return v
}

// sanitize keeps what the bash's awk keeps: [A-Za-z0-9_.:T-].
func sanitize(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == ':' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func keepDigits(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// digitsOnly is the friend total's rule: only an all-digit cell adds.
func digitsOnly(v string) int64 {
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// awkInt is awk's printf %d of a string: its leading number, truncated; 0 if none.
func awkInt(v string) int64 {
	end := 0
	for end < len(v) && (v[end] >= '0' && v[end] <= '9' || v[end] == '.' || (end == 0 && (v[end] == '-' || v[end] == '+'))) {
		end++
	}
	f, err := strconv.ParseFloat(v[:end], 64)
	for err != nil && end > 0 {
		end--
		f, err = strconv.ParseFloat(v[:end], 64)
	}
	if err != nil {
		return 0
	}
	return int64(f)
}
