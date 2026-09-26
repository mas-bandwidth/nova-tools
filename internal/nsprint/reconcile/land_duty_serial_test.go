package reconcile_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestLandDutyRefusesSerialByDefaultPassesPartialAndYieldsToTheMergeCard
// (nova-tools #4324), on an in-process store with no clone and no forge:
// a stream with two read members and one unread is refused LAND-SERIAL on
// its LAND-DUTY line by default; with cfg:land partial=1 the duty runs the
// landing with Partial set; while the stream's merge card is live the duty
// leaves the stream to it (state merge-card, the landing never called); when
// the card closes the duty lands again.
func TestLandDutyRefusesSerialByDefaultPassesPartialAndYieldsToTheMergeCard(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const s, repo = "landing: alpha", "mas-bandwidth/nova-tools"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	head := func(n int) string { return strings.Repeat(fmt.Sprint(n), 40) }
	for n, score := range map[int]float64{1: 10, 2: 20, 3: 30} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: score, Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprintf("nova-tools#%d", n), "stream", s)
		fields := map[string]string{"repo": repo, "n": fmt.Sprint(n), "head": head(n), "base": "dev", "stream": s, "state": "open"}
		if n != 3 {
			fields["reads"] = fmt.Sprintf("SCORE who=emma head=%s score=9/10", head(n))
		}
		c.HSet(ctx, stream.PRKey(repo, n), fields)
	}
	var landed []stream.RunOptions
	d := &reconcile.LandDuty{Client: c, Repos: []string{repo}, Workroot: t.TempDir(), Out: io.Discard, Host: "fixture",
		// Never reached: the refusal and the seam come first.
		GitHub:  func() (*stream.GitHub, error) { return &stream.GitHub{API: "http://127.0.0.1:1", Token: "t"}, nil },
		Request: func(context.Context, string, string, int, string) (string, error) { return "CREATED", nil },
	}
	lines, err := d.LandPass(ctx, repo)
	if err != nil || len(lines) != 1 {
		t.Fatalf("pass: %v %+v", err, lines)
	}
	if l := lines[0]; l.Stream != s || !strings.Contains(l.Err, "REFUSED LAND-SERIAL stream=landing:\\x20alpha carrying=2 merging=3 left_out=#3:no-read-at-head") {
		t.Fatalf("default: %s", l)
	}
	// cfg:land partial=1: the landing runs with Partial (the seam records
	// the options; nothing clones).
	c.HSet(ctx, "cfg:land", "partial", "1")
	d.Land = func(_ context.Context, _ stream.Client, o stream.RunOptions) (stream.RunReport, error) {
		landed = append(landed, o)
		return stream.RunReport{State: "waiting"}, nil
	}
	if lines, err = d.LandPass(ctx, repo); err != nil || len(lines) != 1 || lines[0].State != "waiting" || len(landed) != 1 || !landed[0].Partial || landed[0].Streams[0] != s {
		t.Fatalf("partial=1: %v %+v landed=%+v", err, lines, landed)
	}
	// A live merge card owns the stream.
	c.HSet(ctx, reconcile.LandMergeKey(s), "task", "merge-landing-alpha-1")
	c.HSet(ctx, "task:merge-landing-alpha-1", "state", "open", "kind", "merge")
	lines, err = d.LandPass(ctx, repo)
	if err != nil || len(lines) != 1 || lines[0].State != "merge-card" || lines[0].Card != "merge-landing-alpha-1" || len(landed) != 1 {
		t.Fatalf("merge card live: %v %+v landed=%d", err, lines, len(landed))
	}
	if !strings.Contains(lines[0].String(), " state=merge-card ") || !strings.Contains(lines[0].String(), " card=merge-landing-alpha-1 ") {
		t.Fatalf("line: %s", lines[0])
	}
	// The card closed: the duty lands again.
	c.HSet(ctx, "task:merge-landing-alpha-1", "state", "closed")
	if lines, err = d.LandPass(ctx, repo); err != nil || len(lines) != 1 || lines[0].State != "waiting" || len(landed) != 2 {
		t.Fatalf("card closed: %v %+v landed=%d", err, lines, len(landed))
	}
}
