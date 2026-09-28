package deal

import (
	"context"
	"errors"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePRs is the forge in a map: <repo>#<n> -> Ref. A missing number is an
// error, as a forge that did not answer is. It counts the questions asked.
type fakePRs struct {
	mu    sync.Mutex
	refs  map[string]Ref
	asked map[string]int
}

func newFakePRs() *fakePRs { return &fakePRs{refs: map[string]Ref{}, asked: map[string]int{}} }

func (f *fakePRs) set(ref string, r Ref) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[ref] = r
}

func (f *fakePRs) Ref(_ context.Context, repo string, n int) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fmt.Sprintf("%s#%d", repo, n)
	f.asked[k]++
	r, ok := f.refs[k]
	if !ok {
		return Ref{}, errors.New("forge did not answer")
	}
	return r, nil
}

// TestReadyNamesEveryEntry holds the gate's reading of each DEPENDS-ON form:
// only a merge into the card's base, a closed issue, a landed card or a card
// closed with nothing to land releases it; everything else is named.
func TestReadyNamesEveryEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	prs := newFakePRs()
	prs.set("o/r#1", Ref{IsPR: true, Merged: true, State: "closed", Base: "dev"})
	prs.set("o/r#2", Ref{IsPR: true, Merged: true, State: "closed", Base: "main"})
	prs.set("o/r#3", Ref{IsPR: true, State: "closed", Base: "dev"})
	prs.set("o/r#4", Ref{State: "closed"})
	prs.set("o/r#5", Ref{State: "open"})
	prs.set("o/r#6", Ref{IsPR: true, State: "open", Base: "dev"})
	// A merged PR whose forge answer names no base: unknown, not landed
	// (Stella's HOLD2 on #3080: a missing base must not release the card).
	prs.set("o/r#7", Ref{IsPR: true, Merged: true, State: "closed", Base: ""})
	card := func(label string, deps ...string) Card {
		return Card{Sprint: "s", Label: label, Repo: "o/r", Base: "dev", DependsOn: deps}
	}
	in := Input{
		Now: time.Now(),
		Sprints: []Sprint{{Name: "s", Pool: []Card{
			card("none", "-"),
			card("empty"),
			card("pr-merged", "o/r#1"),
			card("pr-other-base", "o/r#2"),
			card("pr-closed", "o/r#3"),
			card("pr-base-unknown", "o/r#7"),
			{Sprint: "s", Label: "dep-base-unknown", Repo: "o/r", Base: "", DependsOn: []string{"o/r#1"}},
			card("issue-closed", "o/r#4"),
			card("issue-open", "o/r#5"),
			card("unanswered", "o/r#99"),
			card("landed", "dep-landed"),
			card("no-pr-done", "dep-no-pr"),
			card("cancelled", "dep-cancelled"),
			card("missing", "dep-gone"),
			card("dep-pr-open", "dep-open"),
			card("dep-pr-is-issue", "dep-issue"),
			card("two", "o/r#1", "o/r#6"),
		}, Waiting: []Card{
			{Sprint: "s", Label: "was-waiting", Repo: "o/r", Base: "dev", DependsOn: []string{"o/r#1"}, WaitWhy: "o/r#1 open"},
		}}},
		Deps: map[string]DepCard{
			"s/dep-landed":    {Found: true, State: "landed", PR: 7, Repo: "o/r"},
			"s/dep-no-pr":     {Found: true, State: "ended", Outcome: "DONE"},
			"s/dep-cancelled": {Found: true, State: "cancelled"},
			"s/dep-open":      {Found: true, State: "review-ready", PR: 6},
			// A card whose PR number answers as a closed issue (pulls/<n> 404
			// fell back to issues/<n>): not landed (Stella's hold 7 on #3080).
			"s/dep-issue": {Found: true, State: "review-ready", PR: 4},
		},
	}
	out, moves, blocked := Ready(ctx, in, prs)
	var pool []string
	for _, c := range out.Sprints[0].Pool {
		pool = append(pool, c.Label)
	}
	if got := strings.Join(pool, ","); got != "none,empty,pr-merged,issue-closed,landed,no-pr-done,was-waiting" {
		t.Fatalf("ready pool %s", got)
	}
	why := map[string]string{}
	for _, b := range blocked {
		why[b.Label] = b.Why
	}
	for label, want := range map[string]string{
		"pr-other-base":    "o/r#2 merged into main, not dev",
		"pr-closed":        "o/r#3 can no longer land: closed without merge",
		"issue-open":       "o/r#5 open",
		"unanswered":       "o/r#99 unknown: forge did not answer",
		"cancelled":        "dep-cancelled can no longer land: card cancelled without a PR",
		"missing":          "dep-gone can no longer land: no such card in sprint s",
		"dep-pr-open":      "dep-open (o/r#6) open",
		"two":              "o/r#6 open",
		"pr-base-unknown":  "o/r#7 unknown: base-unresolved",
		"dep-base-unknown": "o/r#1 unknown: base-unresolved",
		"dep-pr-is-issue":  "dep-issue (o/r#4) unknown: not a PR (issue closed)",
	} {
		if why[label] != want {
			t.Errorf("%s: why %q, want %q", label, why[label], want)
		}
	}
	if len(blocked) != 11 {
		t.Errorf("%d blocked, want 11: %+v", len(blocked), blocked)
	}
	var released int
	for _, m := range moves["s"] {
		if m.Verb == GateRelease {
			released++
			if m.Label != "was-waiting" {
				t.Errorf("released %s", m.Label)
			}
		}
	}
	if released != 1 {
		t.Errorf("released %d, want 1", released)
	}
	// One question per number per pass, however many cards name it.
	if n := prs.asked["o/r#1"]; n != 1 {
		t.Errorf("o/r#1 asked %d times in one pass, want 1", n)
	}
	// No forge seam: a card that waits on a PR is never dealt.
	if out, _, blocked := Ready(ctx, Input{Sprints: []Sprint{{Name: "s", Pool: []Card{card("x", "o/r#1")}}}}, nil); len(out.Sprints[0].Pool) != 0 || len(blocked) != 1 {
		t.Fatalf("with no forge seam the pool is %+v", out.Sprints[0].Pool)
	}
}

// TestGHRefReadsPullsThenIssues runs GH against an httptest forge through
// the one client: a PR answers from pulls/<n>; a 404 there reads
// issues/<n>; a forge error stays an error. With a store, the lander's own
// PR record answers first and every answer is copied, so the second read
// of a reference makes no call (the reconciler never polls, #4343).
func TestGHRefReadsPullsThenIssues(t *testing.T) {
	t.Parallel()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/repos/o/r/pulls/1":
			_, _ = w.Write([]byte(`{"merged":true,"state":"closed","base":{"ref":"dev"}}`))
		case "/repos/o/r/pulls/2":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case "/repos/o/r/issues/2":
			_, _ = w.Write([]byte(`{"state":"closed"}`))
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	t.Cleanup(srv.Close)
	g := GH{Client: &gh.Client{API: srv.URL, Token: "t", HTTP: srv.Client()}}
	ctx := context.Background()
	if r, err := g.Ref(ctx, "o/r", 1); err != nil || !r.IsPR || !r.Merged || r.Base != "dev" {
		t.Fatalf("pulls/1: %+v %v", r, err)
	}
	if r, err := g.Ref(ctx, "o/r", 2); err != nil || r.IsPR || r.State != "closed" {
		t.Fatalf("issue 2: %+v %v", r, err)
	}
	if _, err := g.Ref(ctx, "o/r", 3); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("a forge error must stay an error, got %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	g = GH{Client: &gh.Client{API: srv.URL, Token: "t", HTTP: srv.Client(), Redis: rdb}, Redis: rdb}
	calls = 0
	for range 2 {
		if r, err := g.Ref(ctx, "o/r", 1); err != nil || !r.Merged {
			t.Fatalf("copied: %+v %v", r, err)
		}
	}
	if calls != 1 {
		t.Fatalf("two reads of one reference made %d calls, want 1 (the copy answers the second)", calls)
	}
	// The lander's own record answers before any call.
	if err := rdb.HSet(ctx, prkey.Key("o/r", 4), "state", "landed", "base", "dev").Err(); err != nil {
		t.Fatal(err)
	}
	if r, err := g.Ref(ctx, "o/r", 4); err != nil || !r.IsPR || !r.Merged || r.Base != "dev" || calls != 1 {
		t.Fatalf("record: %+v %v calls=%d", r, err, calls)
	}
}
