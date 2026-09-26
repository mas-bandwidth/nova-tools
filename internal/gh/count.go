package gh

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// IndexKey is the set of "<verb> <endpoint>" members with a calls hash.
const IndexKey = "gh:calls"

// RateKey is the last rate record: remaining, limit, reset, at, verb, endpoint.
const RateKey = "gh:rate"

// TotalKey is every verb's calls by minute, the one HGETALL the sprint
// table's tick reads for its gh n/h.
const TotalKey = IndexKey + ":all"

// Window is what `gh budget` and the status line count over.
const Window = time.Hour

// Keep is how long a minute bucket stays before the budget read prunes it.
const Keep = 24 * time.Hour

// CallsKey is the minute-bucket hash of one verb and endpoint.
func CallsKey(verb, endpoint string) string { return IndexKey + ":" + verb + ":" + endpoint }

// count records one request: HINCRBY the minute bucket, SADD the index,
// and HSET the rate record when the reply carried the header. A Redis
// failure prints one line and never fails the call.
func (c *Client) count(ctx context.Context, endpoint string, r reply) {
	if c.Redis == nil {
		return
	}
	now := c.now()
	verb := c.verb()
	minute := strconv.FormatInt(now.Unix()/60, 10)
	p := c.Redis.Pipeline()
	p.HIncrBy(ctx, CallsKey(verb, endpoint), minute, 1)
	p.HIncrBy(ctx, TotalKey, minute, 1)
	p.SAdd(ctx, IndexKey, verb+" "+endpoint)
	if r.Remaining >= 0 {
		p.HSet(ctx, RateKey, map[string]any{
			"remaining": r.Remaining, "limit": r.limit, "reset": r.reset,
			"at": strconv.FormatInt(now.Unix(), 10), "verb": verb, "endpoint": endpoint,
		})
	}
	if _, err := p.Exec(ctx); err != nil {
		fmt.Fprintf(c.log(), "gh: count %s %s not recorded: %v\n", verb, endpoint, err)
	}
}

// Row is one verb and endpoint's calls: Hour in the window, Total in every
// kept bucket.
type Row struct {
	Verb     string
	Endpoint string
	Hour     int64
	Total    int64
}

// Rate is the last X-RateLimit record.
type Rate struct {
	Remaining int
	Limit     int
	Reset     time.Time
	At        time.Time
	Verb      string
	Endpoint  string
	Found     bool
}

// Report is what `gh budget` prints.
type Report struct {
	Hour int64 // every call in the window
	Rows []Row // by hour desc, then verb, endpoint
	Rate Rate
}

// Budget reads the hour's calls per verb and endpoint and the rate record.
// One SMEMBERS, one pipeline of HGETALLs, one HGETALL; buckets older than
// Keep are deleted in a second pipeline (the read is the pruner, no TTL).
func Budget(ctx context.Context, rdb redis.Cmdable, now time.Time) (Report, error) {
	var rep Report
	members, err := rdb.SMembers(ctx, IndexKey).Result()
	if err != nil {
		return rep, fmt.Errorf("SMEMBERS %s: %w", IndexKey, err)
	}
	sort.Strings(members)
	p := rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(members))
	for i, m := range members {
		verb, endpoint, _ := strings.Cut(m, " ")
		cmds[i] = p.HGetAll(ctx, CallsKey(verb, endpoint))
	}
	rate := p.HGetAll(ctx, RateKey)
	all := p.HGetAll(ctx, TotalKey)
	if _, err := p.Exec(ctx); err != nil {
		return rep, fmt.Errorf("gh budget read: %w", err)
	}
	since := now.Add(-Window).Unix() / 60
	oldest := now.Add(-Keep).Unix() / 60
	prune := rdb.Pipeline()
	pruning := false
	if stale := staleBuckets(all.Val(), oldest); len(stale) > 0 {
		prune.HDel(ctx, TotalKey, stale...)
		pruning = true
	}
	for i, m := range members {
		verb, endpoint, _ := strings.Cut(m, " ")
		row := Row{Verb: verb, Endpoint: endpoint}
		var stale []string
		for f, v := range cmds[i].Val() {
			minute, err := strconv.ParseInt(f, 10, 64)
			n, err2 := strconv.ParseInt(v, 10, 64)
			if err != nil || err2 != nil {
				continue
			}
			if minute < oldest {
				stale = append(stale, f)
				continue
			}
			row.Total += n
			if minute >= since {
				row.Hour += n
			}
		}
		if len(stale) > 0 {
			prune.HDel(ctx, CallsKey(verb, endpoint), stale...)
			pruning = true
		}
		rep.Hour += row.Hour
		rep.Rows = append(rep.Rows, row)
	}
	if pruning {
		_, _ = prune.Exec(ctx)
	}
	sort.SliceStable(rep.Rows, func(i, j int) bool {
		if rep.Rows[i].Hour != rep.Rows[j].Hour {
			return rep.Rows[i].Hour > rep.Rows[j].Hour
		}
		if rep.Rows[i].Verb != rep.Rows[j].Verb {
			return rep.Rows[i].Verb < rep.Rows[j].Verb
		}
		return rep.Rows[i].Endpoint < rep.Rows[j].Endpoint
	})
	rep.Rate = parseRate(rate.Val())
	return rep, nil
}

func parseRate(m map[string]string) Rate {
	if len(m) == 0 {
		return Rate{}
	}
	r := Rate{Found: true, Verb: m["verb"], Endpoint: m["endpoint"]}
	r.Remaining, _ = strconv.Atoi(m["remaining"])
	r.Limit, _ = strconv.Atoi(m["limit"])
	if n, err := strconv.ParseInt(m["reset"], 10, 64); err == nil && n > 0 {
		r.Reset = time.Unix(n, 0).UTC()
	}
	if n, err := strconv.ParseInt(m["at"], 10, 64); err == nil && n > 0 {
		r.At = time.Unix(n, 0).UTC()
	}
	return r
}

// HourCalls is the window's calls, for the sprint status line's gh n/h:
// one HGETALL of TotalKey.
func HourCalls(ctx context.Context, rdb redis.Cmdable, now time.Time) (int64, error) {
	m, err := rdb.HGetAll(ctx, TotalKey).Result()
	if err != nil {
		return 0, fmt.Errorf("HGETALL %s: %w", TotalKey, err)
	}
	return HourOf(m, now), nil
}

// HourOf sums a minute-bucket hash over the window before now.
func HourOf(m map[string]string, now time.Time) int64 {
	since := now.Add(-Window).Unix() / 60
	var n int64
	for f, v := range m {
		minute, err := strconv.ParseInt(f, 10, 64)
		c, err2 := strconv.ParseInt(v, 10, 64)
		if err == nil && err2 == nil && minute >= since {
			n += c
		}
	}
	return n
}

func staleBuckets(m map[string]string, oldest int64) []string {
	var out []string
	for f := range m {
		if minute, err := strconv.ParseInt(f, 10, 64); err == nil && minute < oldest {
			out = append(out, f)
		}
	}
	return out
}

// Lines is the budget as `gh budget` prints it: the GH BUDGET headline,
// then one GH line per verb and endpoint with a call in the hour.
func (r Report) Lines() []string {
	head := fmt.Sprintf("GH BUDGET hour=%d", r.Hour)
	if r.Rate.Found {
		head += fmt.Sprintf(" remaining=%d/%d reset=%s at=%s last=%s", r.Rate.Remaining, r.Rate.Limit,
			r.Rate.Reset.Format(time.RFC3339), r.Rate.At.Format(time.RFC3339), r.Rate.Verb)
	} else {
		head += " remaining=- (no reply with X-RateLimit-Remaining yet)"
	}
	out := []string{head}
	for _, row := range r.Rows {
		if row.Hour == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("GH %s %s hour=%d total=%d", row.Verb, row.Endpoint, row.Hour, row.Total))
	}
	return out
}
