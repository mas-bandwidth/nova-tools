// fleet ps (#4338) is what runs on every bench, read from the beats: no ssh.
// Each bench's beat carries a process sample (internal/nsprint/fleet/ps.go,
// taken by the bench beat at most every 10 s): the top processes by CPU, the
// nova units declared|undeclared, and the oldest processes outside every
// declared unit.
//
//	fleet ps [--bench <b>] [--stray] [--since <RFC 3339 | duration>]
//
// Per registered bench (SMEMBERS benches, sorted), ps prints the load line,
// the top processes and the undeclared units, then PS <bench> top=<n>
// units=<n> undeclared=<n>. --stray prints only the undeclared units and the
// processes outside every declared unit that started before the last play,
// then STRAY <bench> units=<n> old=<n> play=<t>; the last play is the bench's
// last deploy (bench:<b> build_at), or --since for every bench. A bench
// with no beat, no sample, a failed sample or no last play prints its line
// saying so and is never read as clean.
//
// Exit 0 every bench read (and, with --stray, nothing stray); 1 a bench
// could not be read, or --stray found a stray; 2 usage; 5 store unreachable.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/redis/go-redis/v9"
)

func runFleetPS(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetPSAt(ctx, args, out, errOut, time.Now)
}

// parseSince reads --since: an RFC 3339 time, or a duration back from now.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--since %q: want an RFC 3339 time or a positive duration (6h)", s)
	}
	return now.Add(-d), nil
}

func runFleetPSAt(ctx context.Context, args []string, out, errOut io.Writer, clock func() time.Time) int {
	fs := verbflag.New("fleet ps")
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	stray := fs.Bool("stray", false, "")
	sinceFlag := fs.String("since", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet ps", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet ps", "takes no positional arguments")
	}
	now := clock()
	var since time.Time
	if *sinceFlag != "" {
		if !*stray {
			return refuse(errOut, "fleet ps", "--since is the last play for --stray; it means nothing without it")
		}
		t, err := parseSince(*sinceFlag, now)
		if err != nil {
			return refuse(errOut, "fleet ps", err.Error())
		}
		since = t
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet ps", err.Error())
	}
	defer st.Close()

	benches, err := readPSBenches(ctx, st.Client(), *bench)
	if err != nil {
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet ps", "unregistered bench "+*bench)
		}
		return fleetRefuse(errOut, "fleet ps", err)
	}
	if len(benches) == 0 {
		return refuse(errOut, "fleet ps", "no bench is registered (SMEMBERS benches is empty)")
	}
	code := 0
	for _, b := range benches {
		if !since.IsZero() {
			b.Play, b.PlayRaw = since, ""
		}
		var lines []string
		ok := true
		if *stray {
			var n int
			lines, n, ok = fleet.StrayLines(b)
			if n > 0 {
				code = 1
			}
		} else {
			lines, ok = fleet.PSLines(b, now)
		}
		if !ok {
			code = 1
		}
		fmt.Fprintln(out, strings.Join(lines, "\n"))
	}
	return code
}

// readPSBenches is two pipelined reads: the registry, then every bench's
// beat and build_at. only names one bench, which must be registered.
func readPSBenches(ctx context.Context, c redis.Cmdable, only string) ([]fleet.PSBench, error) {
	reg := c.Pipeline()
	members := reg.SMembers(ctx, "benches")
	if _, err := reg.Exec(ctx); err != nil {
		return nil, err
	}
	names := members.Val()
	sort.Strings(names)
	if only != "" {
		found := false
		for _, n := range names {
			found = found || n == only
		}
		if !found {
			return nil, fleet.ErrUnregistered
		}
		names = []string{only}
	}
	p := c.Pipeline()
	beats := make([]*redis.MapStringStringCmd, len(names))
	plays := make([]*redis.StringCmd, len(names))
	for i, n := range names {
		beats[i] = p.HGetAll(ctx, "bench:"+n+":beat")
		plays[i] = p.HGet(ctx, "bench:"+n, "build_at")
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]fleet.PSBench, len(names))
	for i, n := range names {
		out[i] = fleet.PSBench{Bench: n, Beat: beats[i].Val()}
		if v := plays[i].Val(); v != "" {
			out[i].PlayRaw = v
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				out[i].Play = t
			}
		}
	}
	return out, nil
}
