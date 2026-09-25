package update

// `nova-update report --store <host:port>` (#3880): the fleet's nova-sprint
// versions read from the bench beats, not from ssh or a bus note. Every bench
// already stamps its beat: ns_bench_beat writes bench:<b>:beat build = the
// nova-sprint version line (buildinfo.Line). This reads the benches registry
// and every registered bench's build in two pipelined round trips (SMEMBERS,
// then one HGET per bench), no SCAN, and prints one DRIFT line per beating
// bench whose build is not the newest build beating, then one receipt line.
// A registered bench with no live beat says nothing and is counted, never
// drift: the beat's TTL is the evidence, and no evidence is not a stale build.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// fleetRegistry is the set of registered benches (ns_bench_register), the same
// key internal/fleet/state reads.
const fleetRegistry = "benches"

func fleetBeatKey(bench string) string { return "bench:" + bench + ":beat" }

// benchBuild is one registered bench as its beat states it.
type benchBuild struct {
	Bench   string
	Beating bool
	Build   string // the version identity (field two of the version line), "" when the beat has none
}

// readFleetBuilds reads the registry and every bench's beat build.
func readFleetBuilds(ctx context.Context, c *redis.Client) ([]benchBuild, error) {
	names, err := c.SMembers(ctx, fleetRegistry).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	exists := make([]*redis.IntCmd, len(names))
	builds := make([]*redis.StringCmd, len(names))
	for i, b := range names {
		exists[i] = pipe.Exists(ctx, fleetBeatKey(b))
		builds[i] = pipe.HGet(ctx, fleetBeatKey(b), "build")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]benchBuild, len(names))
	for i, b := range names {
		out[i] = benchBuild{Bench: b, Beating: exists[i].Val() == 1, Build: buildIdentity(builds[i].Val())}
	}
	return out, nil
}

// buildIdentity is the version identity a beat's build carries: field two of a
// version line, or the whole value trimmed when it is not one.
func buildIdentity(build string) string {
	if f, ok := buildinfo.Parse(build); ok {
		return f.Version
	}
	return strings.TrimSpace(build)
}

// stampTime is the 14-digit UTC commit time a vcs stamp opens with
// (buildinfo.Resolve: <yyyymmddhhmmss>-<12 hex>[-dirty]), "" otherwise.
func stampTime(v string) string {
	t, _, ok := strings.Cut(v, "-")
	if !ok || len(t) != 14 || strings.Trim(t, "0123456789") != "" {
		return ""
	}
	return t
}

// newestBuild is the build the fleet should be on: the latest commit time
// among the stamps that carry one (a clean build before a dirty one of the
// same time); with no dated stamp, the build most benches beat, ties to the
// greatest.
func newestBuild(builds []string) string {
	best := ""
	for _, v := range builds {
		if stampTime(v) == "" {
			continue
		}
		if best == "" || newer(v, best) {
			best = v
		}
	}
	if best != "" {
		return best
	}
	count := map[string]int{}
	for _, v := range builds {
		count[v]++
		if best == "" || count[v] > count[best] || (count[v] == count[best] && v > best) {
			best = v
		}
	}
	return best
}

func newer(a, b string) bool {
	ta, tb := stampTime(a), stampTime(b)
	if ta != tb {
		return ta > tb
	}
	da, db := strings.HasSuffix(a, "-dirty"), strings.HasSuffix(b, "-dirty")
	if da != db {
		return db
	}
	return a > b
}

// fleetReport is the verb body: one DRIFT line per stale bench, one receipt.
// Exit 0 when every beating bench is on the newest build, 1 on drift, 2 when
// the store cannot be read.
func fleetReport(addr string, timeout time.Duration, started time.Time, out, errs io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return refusal(errs, "REPORT", fmt.Errorf("%w (supply a reachable --store host:port)", err))
	}
	defer st.Close()
	fleet, err := readFleetBuilds(ctx, st.Client())
	if err != nil {
		return refusal(errs, "REPORT", fmt.Errorf("fleet beats at %s: %w", addr, err))
	}
	var stamped []string
	beating, unknown := 0, 0
	for _, b := range fleet {
		if !b.Beating {
			continue
		}
		beating++
		if b.Build == "" {
			unknown++
			continue
		}
		stamped = append(stamped, b.Build)
	}
	want := newestBuild(stamped)
	current, drift := 0, 0
	for _, b := range fleet {
		if !b.Beating || b.Build == "" {
			continue
		}
		if b.Build == want {
			current++
			continue
		}
		drift++
		fmt.Fprintf(out, "REPORT DRIFT bench=%s tool=nova-sprint build=%s want=%s\n", field(b.Bench), field(b.Build), field(want))
	}
	result, code, w := "OK", 0, out
	if drift > 0 {
		result, code, w = "FAIL", 1, errs
	}
	fmt.Fprintf(w, "REPORT %s benches=%d beating=%d current=%d drift=%d unknown=%d want=%s took=%s store=%s\n", result, len(fleet), beating, current, drift, unknown, field(want), time.Since(started).Round(time.Millisecond), field(addr))
	return code
}
