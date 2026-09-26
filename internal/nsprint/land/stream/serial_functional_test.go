//go:build functional

package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestLandStreamSerialRecordHoldsTheParkedAndKeepsThePR (nova-tools #4324),
// a real build on a local fixture (real git, so the functional tier): a stream with an open stream PR #900
// whose rebuild parks the red member is refused LAND-SERIAL after the
// build; the serial record keeps PR #900 and names the parked member, and
// the PR's own record is untouched (nothing was pushed). The next run,
// with the parked member still in working, is refused before any build
// (not-back:working). Once the member is gone from the stream's live sets
// the stream lands whole again and reuses #900: no second PR is opened on
// the branch (GitHub would answer 422).
func TestLandStreamSerialRecordHoldsTheParkedAndKeepsThePR(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := newRedis(t)
	ctx := context.Background()
	const slug = "landing-streams-lander"
	for _, n := range []int{1, 2} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: float64(n), Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprintf("nova-tools#%d", n), "stream", strm, "state", "merging")
		c.HSet(ctx, PRKey(repo, n), "repo", repo, "n", fmt.Sprint(n), "head", f.Head[n], "base", "dev", "stream", strm, "state", "open",
			"reads", score("emma", f.Head[n], 9))
	}
	old := strings.Repeat("0", 40)
	c.HSet(ctx, LandKey(repo, slug), "repo", repo, "slug", slug, "streams", strm, "base", "dev", "branch", "stream/"+slug,
		"head", old, "state", "open", "pr", "900")
	c.HSet(ctx, PRKey(repo, 900), "repo", repo, "n", "900", "head", old, "kind", "stream", "state", "open", "ci", "green")
	dir := t.TempDir()
	opts := func(n int) Options {
		return Options{Repo: repo, Streams: []string{strm}, Base: "dev", Remote: f.URL, Mirror: "", Workdir: filepath.Join(dir, fmt.Sprintf("w%d", n)),
			Test: testCmd, TestTimeout: time.Minute, MinScore: -1, By: "lander", Author: "Rowan <rowan@example.invalid>",
			ParkConflicts: true, GH: &GitHub{API: "http://127.0.0.1:1", Token: "t"}}
	}
	// Run 1: the rebuild parks #2 red; refused after the build.
	_, err := LandStream(ctx, c, opts(1))
	var ref *Refusal
	if !errors.As(err, &ref) || !strings.Contains(ref.Why, "carrying=1 merging=2 left_out=#2:red:batch-test (after the build)") {
		t.Fatalf("run 1: %v", err)
	}
	l, _, _ := LoadLanding(ctx, c, repo, slug)
	if l.State != "serial" || l.PR != 900 || len(l.SerialLeft) != 1 || l.SerialLeft[0].Task != "t2" || l.SerialLeft[0].N != 2 {
		t.Fatalf("serial record: state=%s pr=%d left=%+v", l.State, l.PR, l.SerialLeft)
	}
	if h := c.HGetAll(ctx, PRKey(repo, 900)).Val(); h["head"] != old || h["ci"] != "green" {
		t.Fatalf("the stream PR's record moved on a refused build: %v", h)
	}
	if st := c.HGet(ctx, "task:t2", "state").Val(); st != "working" {
		t.Fatalf("t2 %q after the park", st)
	}
	// Run 2: t2 is not back; refused before the build (no clone).
	_, err = LandStream(ctx, c, opts(2))
	if !errors.As(err, &ref) || !strings.Contains(ref.Why, "carrying=1 merging=2 left_out=#2:not-back:working") || strings.Contains(ref.Why, "after the build") {
		t.Fatalf("run 2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "w2")); !os.IsNotExist(err) {
		t.Fatalf("run 2 cloned: %v", err)
	}
	// t2 leaves the stream (landed elsewhere): the rest lands whole, on #900.
	c.HSet(ctx, "task:t2", "state", "landed")
	rep, err := LandStream(ctx, c, opts(3))
	if err != nil || rep.State != "open" || rep.PR != 900 || !rep.Reused || rep.Serial != "" {
		t.Fatalf("run 3: %+v %v", rep, err)
	}
	if l, _, _ := LoadLanding(ctx, c, repo, slug); l.State != "open" || l.PR != 900 || len(l.SerialLeft) != 0 {
		t.Fatalf("landing after run 3: %+v", l)
	}
}

// TestLandStreamRefusesSerial (nova-tools #4324): a stream with three
// members in merging of which one has no read at head: land stream refuses
// LAND-SERIAL naming the one left out before it clones or opens anything;
// --dry-run prints the plan with the same line; --partial passes the gate
// (the next refusal is the GitHub client) and prints the line as allowed.
func TestLandStreamRefusesSerial(t *testing.T) {
	t.Parallel()
	// the members are read through ns_ws_zrange (the library, nova-tools#4238)
	c := newRedis(t)
	ctx := context.Background()
	const repo, s = "mas-bandwidth/nova-tools", "swarm: cards"
	head := func(n int) string { return strings.Repeat(fmt.Sprint(n), 40) }
	for n, score := range map[int]float64{1: 10, 2: 20, 3: 30} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, WSKeyAt(0, s, "merging"), redis.Z{Score: score, Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprintf("nova-tools#%d", n), "stream", s)
		fields := map[string]string{"repo": repo, "n": fmt.Sprint(n), "head": head(n), "base": "dev", "stream": s, "state": "open"}
		if n != 3 {
			fields["reads"] = fmt.Sprintf("SCORE who=emma head=%s score=9/10", head(n))
		}
		c.HSet(ctx, PRKey(repo, n), fields)
	}
	opts := Options{Repo: repo, Streams: []string{s}, Base: "dev"}
	_, err := LandStream(ctx, c, opts)
	var ref *Refusal
	if !errors.As(err, &ref) || !strings.HasPrefix(ref.Why, "LAND-SERIAL stream=swarm:\\x20cards carrying=2 merging=3 left_out=#3:no-read-at-head") {
		t.Fatalf("refusal: %v", err)
	}
	if !strings.Contains(ref.Remedy, "--partial") {
		t.Fatalf("remedy: %q", ref.Remedy)
	}
	// The plan names the order (the ZSET score) and the members left out.
	opts.DryRun = true
	rep, err := LandStream(ctx, c, opts)
	if err != nil || rep.State != "dry-run" || len(rep.Members) != 2 || rep.Members[0].N != 1 || rep.Members[1].N != 2 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	if len(rep.LeftOut) != 1 || rep.LeftOut[0].N != 3 || rep.Serial != ref.Why {
		t.Fatalf("plan: left_out=%+v serial=%q", rep.LeftOut, rep.Serial)
	}
	// Partial: the gate passes with the line in the log.
	var log bytes.Buffer
	opts.DryRun, opts.Partial, opts.Log = false, true, &log
	_, err = LandStream(ctx, c, opts)
	if !errors.As(err, &ref) || ref.Why != "no GitHub client" {
		t.Fatalf("partial: %v", err)
	}
	if !strings.Contains(log.String(), "LAND-SERIAL stream=swarm:\\x20cards carrying=2 merging=3 left_out=#3:no-read-at-head allowed=partial\n") {
		t.Fatalf("log: %q", log.String())
	}
	// A stream whose members can all land has no line.
	c.HSet(ctx, PRKey(repo, 3), "reads", fmt.Sprintf("SCORE who=emma head=%s score=10/10", head(3)))
	opts.Partial, opts.DryRun = false, true
	if rep, err := LandStream(ctx, c, opts); err != nil || rep.Serial != "" || len(rep.Members) != 3 {
		t.Fatalf("all read: %+v %v", rep, err)
	}
}
