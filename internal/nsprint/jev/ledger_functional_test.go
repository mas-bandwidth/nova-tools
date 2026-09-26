//go:build functional

package jev_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// scripted is Jev in a functional test: one answer per type, never the real
// provider.
type scripted map[string]string

func (s scripted) Decide(_ context.Context, _ string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	out := map[string]decide.Answer{}
	for k := range qs {
		a, ok := s[k]
		if !ok {
			return nil, decide.Usage{}, errors.New("decide: provider error: status 503")
		}
		out[k] = decide.Answer{Type: "choice", Choice: a, Confidence: 0.8}
	}
	return out, decide.Usage{InputTokens: 500, HasInput: true, HasOutput: true}, nil
}

// TestLedgerFromTheMoveLogToTheReport is #4316's DONE-WHEN on a real Redis
// with the library loaded: a primary pushed with ROUTE pro and TYPE verb, its
// bench copy failing to review, and the coordinator's redeal become, through
// jev sync alone, a tier row (rules pro), a worktype row (outcome verb, from
// the card) and a review row (rules: the REVIEW-JEV suggest; outcome: the
// posted verdict); jev ask puts Jev's shadow answer on each row, one typed
// call per row, and a provider failure goes back to pending; the report
// prints agreement per type, source and version; Record and Join are the one
// call a new decision point makes.
func TestLedgerFromTheMoveLogToTheReport(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")
	k := taskcard.Consumer{Kind: "bench", Name: "b"}
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, k.DesiredKey(), "slots", "2")
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "nova-tools-4316", Where: "waiting", Stream: "swarm: cards",
		Sprint: "jev-s", Kind: "build", Ref: "nova-tools#4316", Origin: "issue:nova-tools#4316", Title: "the ledger",
		Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", strings.Repeat("a", 40), "paths", "internal/nsprint/jev",
			"done_when", "jev report prints agreement", "route", "pro", "type", "verb"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{"nova-tools-4316"}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	if _, err := taskcard.Work(ctx, c, k, "rowan", 0, false, d[0].Copy); err != nil {
		t.Fatal(err)
	}
	if e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, Why: "child exit 1", By: "b",
		Fields: []string{"exit", "1", "model", "kimi-k3"}}); err != nil || e[0].To != "review" {
		t.Fatalf("end --fail %v %v", e, err)
	}

	sync := func() jev.SyncResult {
		t.Helper()
		r, err := jev.Sync(ctx, c, 1000)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := sync()
	if r.Decisions != 3 || r.Outcomes != 1 || r.Cursor == "" || r.Gap != "" {
		t.Fatalf("first sync %+v; want tier, worktype and review rows and the worktype outcome", r)
	}
	at := c.HGet(ctx, "task:nova-tools-4316", "review_at").Val()
	row := func(typ, s string) map[string]string { return c.HGetAll(ctx, jev.RowKey(typ, s)).Val() }
	if h := row(jev.TypeTier, "nova-tools-4316"); h["rules"] != "pro" || !strings.Contains(h["state"], "PATHS: internal/nsprint/jev") {
		t.Errorf("tier row %v", h)
	}
	if h := row(jev.TypeWorkType, "nova-tools-4316"); h["outcome"] != "verb" || h["outcome_by"] != "card" {
		t.Errorf("worktype row %v", h)
	}
	if h := row(jev.TypeReview, "nova-tools-4316@"+at); h["rules"] == "" || !strings.Contains(h["state"], "EXIT: 1") {
		t.Errorf("review row %v", h)
	}
	if again := sync(); again.Events != 0 || again.Decisions != 0 {
		t.Errorf("a second sync with no new move wrote %+v", again)
	}

	if _, err := taskcard.Review(ctx, c, "nova-tools-4316", "recut", "the card is two cards", "rowan"); err != nil {
		t.Fatal(err)
	}
	if r := sync(); r.Outcomes != 1 {
		t.Errorf("verdict sync %+v", r)
	}
	if h := row(jev.TypeReview, "nova-tools-4316@"+at); h["outcome"] != "recut" || h["outcome_by"] != "rowan" {
		t.Errorf("review outcome %v", h)
	}

	if n := c.SCard(ctx, jev.KeyPending).Val(); n != 3 {
		t.Fatalf("pending %d, want 3", n)
	}
	res, err := jev.AskPending(ctx, c, scripted{jev.TypeTier: "pro", jev.TypeReview: "recut"}, 16)
	if err != nil || len(res) != 3 {
		t.Fatalf("ask %+v %v", res, err)
	}
	if n := c.SMembers(ctx, jev.KeyPending).Val(); len(n) != 1 || n[0] != jev.TypeWorkType+" nova-tools-4316" {
		t.Errorf("after a provider failure pending is %v, want the worktype row back", n)
	}
	if h := row(jev.TypeTier, "nova-tools-4316"); h["jev"] != "pro" || h["prompt_version"] != "tier-v1" || h["tokens"] != "500" {
		t.Errorf("tier row after ask %v", h)
	}

	// the hook: one call records a decision point another stream builds, one
	// call joins its outcome; an outcome with no decision is refused
	if err := jev.Record(ctx, c, jev.Decision{Type: jev.TypeOrder, Subject: "nova-tools-1|nova-tools-2", State: "PAIR",
		Rules: "before", Ask: true, Prompt: &jev.Prompt{Version: "order-v1", Instructions: "which first?",
			Options: map[string]string{"before": "1 first", "after": "2 first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := jev.Join(ctx, c, jev.Outcome{Type: jev.TypeOrder, Subject: "nova-tools-1|nova-tools-2", Outcome: "after",
		By: "land", Why: "order miss"}); err != nil {
		t.Fatal(err)
	}
	if err := jev.Join(ctx, c, jev.Outcome{Type: jev.TypeOrder, Subject: "none", Outcome: "after", By: "x", Why: "y"}); !errors.Is(err, jev.ErrNoRow) {
		t.Errorf("an outcome with no decision: %v", err)
	}

	rows, pending, err := jev.LoadRows(ctx, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	jev.Print(&b, rows, pending, "", "")
	out := b.String()
	if strings.Contains(out, "JEV type=worktype") {
		t.Errorf("the worktype has no rules answer and no Jev answer yet, and printed a line:\n%s", out)
	}
	for _, want := range []string{
		"JEV REPORT rows=4 pending=2 lines=5\n",
		"JEV type=tier source=rules version=rules answers=1 outcomes=0 agree=0 agreement=- open=1",
		"JEV type=tier source=jev version=tier-v1 answers=1 outcomes=0 agree=0 agreement=- open=1 overrides=0 override_agreement=- cost=$0.000021 ms=",
		"JEV type=review source=jev version=review-v1 answers=1 outcomes=1 agree=1 agreement=100% open=0 overrides=1 override_agreement=100%",
		"JEV type=order source=rules version=rules answers=1 outcomes=1 agree=0 agreement=0%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if n := c.XLen(ctx, jev.KeyDecisions).Val(); n < 9 {
		t.Errorf("jev:decisions has %d entries; every decision, answer and outcome is one", n)
	}
}
