package mirror

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Key is bench b's receipt hash. Only bench b's loop writes it; it has no TTL.
func Key(bench string) string { return "bench:" + bench + ":mirrors" }

// LeaseKey is the one-loop-per-bench lease, the only key here with a TTL.
func LeaseKey(bench string) string { return "lease:mirror:" + bench }

// Now is Redis TIME in ms: every at is the store's clock, never the bench's.
func Now(ctx context.Context, rdb redis.Cmdable) (int64, error) {
	t, err := rdb.Time(ctx).Result()
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

// Record writes one pass as one HSET: at, and per repo its tip, from, pulls and
// err. A refusal keeps the last good tip and sets err; an OK clears err.
func Record(ctx context.Context, rdb redis.Cmdable, bench string, at int64, results []Result) error {
	fields := []any{"at", strconv.FormatInt(at, 10)}
	for _, r := range results {
		if r.OK {
			fields = append(fields, r.Repo, r.Tip, r.Repo+":from", r.From, r.Repo+":pulls", strconv.Itoa(r.Pulls), r.Repo+":err", "")
		} else {
			fields = append(fields, r.Repo+":err", r.Reason+": "+oneLine(r.Err))
		}
	}
	return rdb.HSet(ctx, Key(bench), fields...).Err()
}

// Lease takes or renews lease:mirror:<b> for session with ttl. It reports false
// when another session holds it.
func Lease(ctx context.Context, rdb redis.Cmdable, bench, session string, ttl time.Duration) (bool, string, error) {
	pipe := rdb.Pipeline()
	set := pipe.SetNX(ctx, LeaseKey(bench), session, ttl)
	get := pipe.Get(ctx, LeaseKey(bench))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, "", err
	}
	if set.Val() {
		return true, session, nil
	}
	holder := get.Val()
	if holder != session {
		return false, holder, nil
	}
	return true, session, rdb.PExpire(ctx, LeaseKey(bench), ttl).Err()
}

// StatusOptions is `mirror status`: which repos, what they are held to, and
// how old a receipt may be before the bench is UNREACHABLE.
type StatusOptions struct {
	Repos  []string          // the repos to report, in order
	Expect map[string]string // repo -> 40-hex; missing means the source's recorded tip
	Source string            // the source bench, whose tip is expected when --expect is absent
	Stale  time.Duration     // a receipt older than this is UNREACHABLE
}

// Status prints one line per bench in the set benches (name order) per repo,
// then a total line per repo, and reports whether every line is OK. It is two
// round trips: SMEMBERS benches, then one pipeline of TIME and HGETALLs.
func Status(ctx context.Context, rdb redis.Cmdable, o StatusOptions, out io.Writer) (bool, error) {
	benches, err := rdb.SMembers(ctx, "benches").Result()
	if err != nil {
		return false, err
	}
	sort.Strings(benches)
	names := append([]string(nil), benches...)
	if o.Source != "" && !contains(names, o.Source) {
		names = append(names, o.Source)
	}
	pipe := rdb.Pipeline()
	now := pipe.Time(ctx)
	recs := make(map[string]*redis.MapStringStringCmd, len(names))
	for _, b := range names {
		recs[b] = pipe.HGetAll(ctx, Key(b))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	nowMS := now.Val().UnixMilli()
	all := true
	for _, repo := range o.Repos {
		expected := o.Expect[repo]
		if expected == "" && o.Source != "" {
			src := recs[o.Source].Val()
			if fresh(src, nowMS, o.Stale) {
				expected = src[repo]
			}
		}
		if len(expected) != 40 {
			expected = "?"
		}
		okN := 0
		for _, b := range benches {
			rec := recs[b].Val()
			observed, from, age, state := "?", "?", "?", "OK"
			at, atErr := strconv.ParseInt(rec["at"], 10, 64)
			if atErr == nil {
				age = strconv.FormatInt((nowMS-at)/1000, 10)
			}
			switch {
			case len(rec) == 0 || rec[repo] == "":
				state = "MISSING"
			case !fresh(rec, nowMS, o.Stale):
				state = "UNREACHABLE"
			default:
				if len(rec[repo]) == 40 {
					observed = rec[repo]
				}
				if rec[repo+":from"] != "" {
					from = rec[repo+":from"]
				}
				if expected == "?" || observed != expected || rec[repo+":err"] != "" {
					state = "MISMATCH"
				}
			}
			if state == "OK" {
				okN++
			} else {
				all = false
			}
			fmt.Fprintf(out, "%s %s observed=%s expected=%s %s from=%s age=%ss\n", b, repo, observed, expected, state, from, age)
		}
		pct := 0
		if len(benches) > 0 {
			pct = okN * 100 / len(benches)
		} else {
			all = false
		}
		fmt.Fprintf(out, "%s %d/%d %d%%\n", repo, okN, len(benches), pct)
	}
	return all, nil
}

func fresh(rec map[string]string, nowMS int64, stale time.Duration) bool {
	at, err := strconv.ParseInt(rec["at"], 10, 64)
	return err == nil && nowMS-at <= stale.Milliseconds()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
