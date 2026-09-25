package card_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// TestWrapperWallCapEndsTheCard is the DONE-WHEN control of #3653: a harness
// that runs past the card's wall cap (EST x 1.5, here one second) with live
// beats is killed, and the card ends FAILED reason=wall within 3 s, with the
// end record, the wrapper line naming the cap, the partial out/ kept for the
// read, and wall_max_s on the card hash. The cap comes from the card's est
// field, and from cfg:card wall_max_min when the card carries no EST.
func TestWrapperWallCapEndsTheCard(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// 1/90 of a minute x 1.5 is one second.
	const oneSecondEst = "0.011111111111"
	for i, tc := range []struct {
		name string
		seed func(ctx context.Context, client *redis.Client, id card.Identity) error
	}{
		{"est", func(ctx context.Context, client *redis.Client, id card.Identity) error {
			return client.HSet(ctx, card.CardKey(id.Sprint, id.Label), "est", oneSecondEst).Err()
		}},
		{"cfg-default", func(ctx context.Context, client *redis.Client, id card.Identity) error {
			return client.HSet(ctx, card.CardConfigKey, "wall_max_min", oneSecondEst).Err()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "control-wall", Label: "card-wall-" + tc.name, BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, fmt.Sprintf("%032x", 0x3653+i))
			seedCard(t, ctx, client, id, "dealt", token)
			if err := tc.seed(ctx, client, id); err != nil {
				t.Fatal(err)
			}
			t.Setenv(fakeHarnessEnv, "hang")
			t.Setenv(fakeGateEnv, filepath.Join(t.TempDir(), "never"))

			h := newHarnessRun(t, id, self)
			h.cfg.WallAfter = nil // the real timer: the harness really runs past it
			ledger := &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}
			start := time.Now()
			got := make(chan card.WrapperReport, 1)
			go func() { got <- card.RunWrapper(ctx, h.cfg, ledger) }()
			// The event is asserted, not the clock (internal/ci's wait rules):
			// the harness never exits on its own and no card clock or beat
			// tick is sent, so only the wall cap can end this card.
			rep := h.report(got)
			t.Logf("ended after %s with a 1 s wall cap", time.Since(start).Round(time.Millisecond))
			if rep.Code != card.WrapperExitEnded || rep.Outcome != "FAILED" || rep.Reason != "wall" || rep.Exit != -1 {
				t.Fatalf("report %s why=%q; want FAILED reason=wall exit=-1 code=0", rep.Line(), rep.Why)
			}
			if !strings.Contains(rep.Why, "wall_max_s=1") {
				t.Fatalf("why %q does not name the cap", rep.Why)
			}
			hash := hashOf(t, ctx, client, id.Sprint, id.Label)
			if hash["state"] != "ended" || hash["outcome"] != "FAILED" || hash["reason"] != "wall" || hash["exit"] != "-1" {
				t.Fatalf("card hash %v, want ended FAILED wall exit -1", hash)
			}
			if hash["wall_max_s"] != "1" {
				t.Fatalf("card hash wall_max_s %q, want 1", hash["wall_max_s"])
			}
			log := logBy(t, ctx, client, id.Sprint)
			if n := len(log["ended"]); n != 1 || log["ended"][0]["reason"] != "wall" {
				t.Fatalf("end records %v, want one with reason wall", log["ended"])
			}
			rec, err := card.ReadEndRecord(h.results)
			if err != nil || rec.Identity != id || rec.Outcome != "FAILED" || rec.Reason != "wall" || rec.ExitCode != -1 {
				t.Fatalf("end record %+v err %v, want FAILED wall exit -1", rec, err)
			}
			line, err := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"outcome=FAILED", "reason=wall", "exit=-1", "wall_max_s=1"} {
				if !strings.Contains(string(line), want) {
					t.Fatalf("wrapper.line %q lacks %s", line, want)
				}
			}
			// The partial out/ is kept for the read.
			if b, err := os.ReadFile(filepath.Join(h.results, "RESULT.md")); err != nil || !strings.HasPrefix(string(b), "RESULT: ") {
				t.Fatalf("partial out/ RESULT.md not kept: %q %v", b, err)
			}
			h.assertNoJobDir()
		})
	}
}

// TestWallMaxIsEstTimesOneAndAHalf pins the cap's arithmetic and its defaults.
func TestWallMaxIsEstTimesOneAndAHalf(t *testing.T) {
	for _, tc := range []struct {
		est, def float64
		want     time.Duration
	}{
		{20, 0, 30 * time.Minute},
		{20, 40, 30 * time.Minute},
		{0, 40, 60 * time.Minute},
		{0, 0, 45 * time.Minute},
		{0.0001, 0, time.Second},
	} {
		if got := card.WallMax(tc.est, tc.def); got != tc.want {
			t.Fatalf("WallMax(%v, %v) = %s, want %s", tc.est, tc.def, got, tc.want)
		}
	}
}

// TestCardPushStoresEst: lint reads EST: into the card hash's est field in
// minutes; prose or an absent line stores nothing and is not refused.
func TestCardPushStoresEst(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label string
		lines []string
		want  string
	}{
		{"est-plain", []string{"EST: 20"}, "20"},
		{"est-min", []string{"EST: 60 min"}, "60"},
		{"est-m", []string{"EST: 90m"}, "90"},
		{"est-hours", []string{"EST: 5 h"}, "300"},
		{"est-prose", []string{"EST: 1 read, ~15 min"}, ""},
		{"est-absent", nil, ""},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		res := card.Push(ctx, client, sprint, withHeader(f, tc.lines...))
		if res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q, want exit 0", tc.label, res.Code, res.Stderr)
		}
		got, err := client.HGet(ctx, keyCard(tc.label), "est").Result()
		if tc.want == "" {
			if err != redis.Nil {
				t.Fatalf("%s: est %q (%v), want no field", tc.label, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s: est %q (%v), want %q", tc.label, got, err, tc.want)
		}
	}
}
