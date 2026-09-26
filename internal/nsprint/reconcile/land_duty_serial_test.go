package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

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
	lines, err := d.LandPass(ctx, repo, "feedface")
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
	if lines, err = d.LandPass(ctx, repo, "feedface"); err != nil || len(lines) != 1 || lines[0].State != "waiting" || len(landed) != 1 || !landed[0].Partial || landed[0].Streams[0] != s {
		t.Fatalf("partial=1: %v %+v landed=%+v", err, lines, landed)
	}
	// A live merge card owns the stream.
	c.HSet(ctx, reconcile.LandMergeKey(s), "task", "merge-landing-alpha-1")
	c.HSet(ctx, "task:merge-landing-alpha-1", "state", "open", "kind", "merge")
	lines, err = d.LandPass(ctx, repo, "feedface")
	if err != nil || len(lines) != 1 || lines[0].State != "merge-card" || lines[0].Card != "merge-landing-alpha-1" || len(landed) != 1 {
		t.Fatalf("merge card live: %v %+v landed=%d", err, lines, len(landed))
	}
	if !strings.Contains(lines[0].String(), " state=merge-card ") || !strings.Contains(lines[0].String(), " card=merge-landing-alpha-1 ") {
		t.Fatalf("line: %s", lines[0])
	}
	// The card closed: the duty lands again.
	c.HSet(ctx, "task:merge-landing-alpha-1", "state", "closed")
	if lines, err = d.LandPass(ctx, repo, "feedface"); err != nil || len(lines) != 1 || lines[0].State != "waiting" || len(landed) != 2 {
		t.Fatalf("card closed: %v %+v landed=%d", err, lines, len(landed))
	}
}

// TestLandDutyAndWatchInterleaveOnOneClaim (nova-tools #4324): one atomic
// claim per stream, land:merge:<stream> owner. While the duty's landing
// runs (inside its Land seam, minutes in production) the watch cuts no
// merge card and a hand run or another card is refused, naming the duty;
// the duty releases at the end and the next watch pass cuts the card and
// claims the stream for it; the duty's next pass leaves the stream to the
// card (state merge-card, the landing never called); a card's child claims
// with its own id; a duty claim whose lease token is gone is dead and taken
// over.
func TestLandDutyAndWatchInterleaveOnOneClaim(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const s, repo, tok = "landing: alpha", "mas-bandwidth/nova-tools", "feedface"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "sp"})
	c.HSet(ctx, "lease:land:"+repo, "token", tok)
	head := func(n int) string { return strings.Repeat(fmt.Sprint(n), 40) }
	for n, score := range map[int]float64{1: 10, 2: 20} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: score, Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprintf("nova-tools#%d", n), "stream", s)
		c.HSet(ctx, stream.PRKey(repo, n), "repo", repo, "n", fmt.Sprint(n), "head", head(n), "base", "dev", "stream", s, "state", "open",
			"reads", fmt.Sprintf("SCORE who=emma head=%s score=9/10", head(n)))
	}
	now := time.UnixMilli(1700000000000)
	var pushed []reconcile.MergeCard
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: repo,
		Push: func(_ context.Context, m reconcile.MergeCard) (string, error) {
			pushed = append(pushed, m)
			c.HSet(ctx, "task:"+m.ID, "state", "open", "kind", m.Kind)
			return m.ID, nil
		},
		Notify:      func(context.Context, reconcile.LandWake) error { return nil },
		Coordinator: func(context.Context) (string, error) { return "rowan", nil },
		Frontier:    func(context.Context) (string, error) { return "stella", nil },
	}
	watch := func() {
		t.Helper()
		now = now.Add(time.Second)
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("watch: %v", err)
		}
	}
	owner := func() string { return c.HGet(ctx, reconcile.LandMergeKey(s), "owner").Val() }
	duty := stream.DutyOwner(repo, tok)
	var calls int
	d := &reconcile.LandDuty{Client: c, Repos: []string{repo}, Workroot: t.TempDir(), Out: io.Discard, Host: "fixture",
		GitHub:  func() (*stream.GitHub, error) { return &stream.GitHub{API: "http://127.0.0.1:1", Token: "t"}, nil },
		Request: func(context.Context, string, string, int, string) (string, error) { return "CREATED", nil },
		// The landing, mid-build: the duty holds the stream.
		Land: func(_ context.Context, _ stream.Client, o stream.RunOptions) (stream.RunReport, error) {
			calls++
			if got := owner(); got != duty {
				t.Errorf("owner during the duty's landing %q, want %q", got, duty)
			}
			watch()
			if len(pushed) != 0 {
				t.Errorf("the watch cut %+v while the duty built", pushed)
			}
			var held *stream.OwnedError
			if err := stream.Claim(ctx, c, []string{s}, stream.CardOwner("merge-x"), now, now.Add(time.Minute)); !errors.As(err, &held) || held.Owner != duty {
				t.Errorf("a card's claim during the duty's landing: %v", err)
			}
			if _, err := stream.Hold(ctx, c, []string{s}, stream.HandOwner("rowan"), time.Minute, func() time.Time { return now }); !errors.As(err, &held) || held.Owner != duty {
				t.Errorf("a hand run during the duty's landing: %v", err)
			}
			return stream.RunReport{State: "waiting"}, nil
		},
	}
	lines, err := d.LandPass(ctx, repo, tok)
	if err != nil || len(lines) != 1 || lines[0].State != "waiting" || calls != 1 {
		t.Fatalf("duty pass: %v %+v calls=%d", err, lines, calls)
	}
	if o := owner(); o != "" {
		t.Fatalf("the duty kept its claim after the pass: %q", o)
	}
	// Released: the watch cuts the card and the card holds the stream.
	watch()
	if len(pushed) != 1 || pushed[0].ID != "merge-landing-alpha-1" || owner() != "card:merge-landing-alpha-1" {
		t.Fatalf("after the duty: pushed=%+v owner=%q", pushed, owner())
	}
	if !strings.Contains(reconcile.MergeTitle(pushed[0]), "--card merge-landing-alpha-1 prints LANDED") {
		t.Fatalf("the card's title names no --card: %s", reconcile.MergeTitle(pushed[0]))
	}
	lines, err = d.LandPass(ctx, repo, tok)
	if err != nil || len(lines) != 1 || lines[0].State != "merge-card" || lines[0].Card != "merge-landing-alpha-1" || calls != 1 {
		t.Fatalf("duty with the card live: %v %+v calls=%d", err, lines, calls)
	}
	// The card's child claims as the card; a hand run is refused naming it.
	if err := stream.Claim(ctx, c, []string{s}, stream.CardOwner("merge-landing-alpha-1"), now, now); err != nil {
		t.Fatalf("the card's own claim: %v", err)
	}
	var held *stream.OwnedError
	if _, err := stream.Hold(ctx, c, []string{s}, stream.HandOwner("rowan"), time.Minute, nil); !errors.As(err, &held) || held.Owner != "card:merge-landing-alpha-1" {
		t.Fatalf("a hand run with the card live: %v", err)
	}
	// A duty claim whose lease is gone (a dead worker) is not live.
	c.HSet(ctx, reconcile.LandMergeKey(s), "owner", stream.DutyOwner(repo, "dead0"))
	if err := stream.Claim(ctx, c, []string{s}, stream.CardOwner("merge-y"), now, now.Add(time.Minute)); err != nil {
		t.Fatalf("over a dead duty claim: %v", err)
	}
}
