package reconcile_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

const (
	redSHA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	greenSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type countingPusher struct{ tasks []reconcile.FixTask }

func (p *countingPusher) push(_ context.Context, t reconcile.FixTask) (string, error) {
	p.tasks = append(p.tasks, t)
	return t.ID, nil
}

func devredFixture(t *testing.T) (context.Context, *redis.Client, *countingPusher, *reconcile.DevRed) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	p := &countingPusher{}
	d := &reconcile.DevRed{
		Client: c, To: "rowan", Push: p.push,
		Bases: []land.RepoBase{{Repo: "nova-tools", Base: "dev"}},
		Now:   func() time.Time { return time.UnixMilli(1000) },
	}
	return context.Background(), c, p, d
}

// TestDevRedPushesOneTaskOnceAndHolds is the DONE-WHEN control: a red tip
// pushes exactly one fix task naming the failing check across many passes,
// sets land:<repo>:<base>:red, the lander's gate reads it, and a green tip
// clears it.
func TestDevRedPushesOneTaskOnceAndHolds(t *testing.T) {
	t.Parallel()

	ctx, c, p, d := devredFixture(t)
	c.HSet(ctx, civerdict.TipKey("nova-tools", "dev"), "sha", redSHA)
	c.HSet(ctx, reconcile.CIRecordKey("nova-tools", redSHA), "verdict", "FAIL", "check", "test-packages/internal/ci")

	outs, err := d.Pass(ctx)
	if err != nil || len(outs) != 1 || outs[0].Err != nil {
		t.Fatalf("pass: %v %+v", err, outs)
	}
	if outs[0].Action != "HELD" || outs[0].Task != "dev-red-nova-tools-dev-aaaaaaaa" {
		t.Fatalf("first pass: %s", outs[0].Line())
	}
	for i := 0; i < 5; i++ {
		outs, _ = d.Pass(ctx)
		if outs[0].Action != "HOLDING" {
			t.Fatalf("pass %d: %s", i+2, outs[0].Line())
		}
	}
	if len(p.tasks) != 1 {
		t.Fatalf("pushed %d tasks, want 1", len(p.tasks))
	}
	if p.tasks[0].To != "rowan" || p.tasks[0].Check != "test-packages/internal/ci" || !strings.Contains(p.tasks[0].Title, "test-packages/internal/ci") {
		t.Fatalf("task %+v", p.tasks[0])
	}
	red, ok, err := land.ReadRed(ctx, c, "nova-tools", "dev")
	if err != nil || !ok || red.SHA != redSHA || red.Task != "dev-red-nova-tools-dev-aaaaaaaa" || red.Check != "test-packages/internal/ci" {
		t.Fatalf("red record: %+v %v %v", red, ok, err)
	}
	if !strings.HasPrefix(red.Line(), "RED test-packages/internal/ci "+redSHA) {
		t.Fatalf("status line %q", red.Line())
	}
	why, err := land.RedBlocked(ctx, c, "nova-tools", "dev")
	if err != nil || !strings.Contains(why, "dev-red: test-packages/internal/ci red at aaaaaaaa") {
		t.Fatalf("lander gate: %q %v", why, err)
	}

	// A record with no verdict on a later tip is no evidence: the hold stays.
	c.HSet(ctx, civerdict.TipKey("nova-tools", "dev"), "sha", greenSHA)
	outs, _ = d.Pass(ctx)
	if outs[0].Action != "HOLDING" {
		t.Fatalf("no evidence: %s", outs[0].Line())
	}

	// Green at the tip clears the hold and the gate.
	c.HSet(ctx, reconcile.CIRecordKey("nova-tools", greenSHA), "verdict", "OK")
	outs, _ = d.Pass(ctx)
	if outs[0].Action != "CLEARED" || outs[0].Task != "dev-red-nova-tools-dev-aaaaaaaa" {
		t.Fatalf("green: %s", outs[0].Line())
	}
	if why, _ := land.RedBlocked(ctx, c, "nova-tools", "dev"); why != "" {
		t.Fatalf("gate still set: %q", why)
	}
	outs, _ = d.Pass(ctx)
	if outs[0].Action != "GREEN" {
		t.Fatalf("steady green: %s", outs[0].Line())
	}
	if len(p.tasks) != 1 {
		t.Fatalf("pushed %d tasks over the episode, want 1", len(p.tasks))
	}
}

// TestDevRedReadsTheGitHubLeg: with no plain record, the GitHub leg
// ci:<repo>:<sha>:gh the webhook consumer writes (#3597) is the evidence: red
// holds the base naming the failing check, once across passes, and nothing is
// read from GitHub.
func TestDevRedReadsTheGitHubLeg(t *testing.T) {
	t.Parallel()

	ctx, c, p, d := devredFixture(t)
	c.HSet(ctx, civerdict.TipKey("nova-tools", "dev"), "sha", redSHA)
	c.HSet(ctx, webhook.Key("mas-bandwidth/nova-tools", redSHA), "gh", "red", "gh_fail", "check:lint",
		"check:lint", "red 9 2026-09-25T12:00:00Z")
	for i := 0; i < 4; i++ {
		outs, err := d.Pass(ctx)
		if err != nil || outs[0].Err != nil {
			t.Fatal(err, outs)
		}
	}
	if len(p.tasks) != 1 || p.tasks[0].Check != "lint" {
		t.Fatalf("tasks %d (%+v); want 1 naming lint", len(p.tasks), p.tasks)
	}
	c.HSet(ctx, webhook.Key("nova-tools", redSHA), "gh", "pending")
	outs, err := d.Pass(ctx)
	if err != nil || outs[0].Action == "CLEARED" {
		t.Fatalf("pending is no evidence, the hold stays: %v %s", err, outs[0].Line())
	}
}

// TestDevRedWatchesTheBasesSet: with no Bases, the set devred:bases names
// the pairs; an empty set is a pass that does nothing.
func TestDevRedWatchesTheBasesSet(t *testing.T) {
	t.Parallel()

	ctx, c, _, d := devredFixture(t)
	d.Bases = nil
	outs, err := d.Pass(ctx)
	if err != nil || len(outs) != 0 {
		t.Fatalf("empty set: %v %+v", err, outs)
	}
	c.SAdd(ctx, reconcile.BasesKey, "rowan-tools/main")
	outs, err = d.Pass(ctx)
	if err != nil || len(outs) != 1 || outs[0].Action != "NOTIP" || outs[0].Repo != "rowan-tools" {
		t.Fatalf("set: %v %+v", err, outs)
	}
}
