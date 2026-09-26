//go:build functional

package stream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const slug = "landing-streams-lander"
	for _, n := range []int{1, 2} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, WSKey(strm, "merging"), redis.Z{Score: float64(n), Member: id})
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
