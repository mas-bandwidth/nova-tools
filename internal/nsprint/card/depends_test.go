package card_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

type dependsRefs struct {
	mu    sync.Mutex
	refs  map[string]deal.Ref
	asked map[string]int
}

func newDependsRefs() *dependsRefs {
	return &dependsRefs{refs: map[string]deal.Ref{}, asked: map[string]int{}}
}

func (f *dependsRefs) set(name string, ref deal.Ref) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[name] = ref
}

func (f *dependsRefs) Ref(_ context.Context, repo string, n int) (deal.Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := fmt.Sprintf("%s#%d", repo, n)
	f.asked[name]++
	ref, ok := f.refs[name]
	if !ok {
		return deal.Ref{}, errors.New("forge did not answer")
	}
	return ref, nil
}

func (f *dependsRefs) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[name]
}

func TestCardPushAcceptsEveryDependsOnForm(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"

	parent := validCard(repo)
	parent.label = "dep-parent"
	mustPush(t, ctx, client, parent.render(), "pool")
	// Task and stream records must exist at push; open ones park the card.
	client.HSet(ctx, "s:"+sprint+":stream:nova-pulse", "state", "open")
	client.HSet(ctx, "task:build-index", "state", "open")

	cases := []struct {
		label string
		dep   string
		place string
		typed string
	}{
		{label: "dep-none", dep: "none", place: "pool", typed: ""},
		{label: "dep-github", dep: "mas-bandwidth/nova-tools#3476", place: "waiting", typed: "github:mas-bandwidth/nova-tools#3476"},
		{label: "dep-stream", dep: "stream/nova-pulse", place: "waiting", typed: "stream:nova-pulse"},
		{label: "dep-task", dep: "task:build-index", place: "waiting", typed: "task:build-index"},
		{label: "dep-card", dep: "dep-parent", place: "waiting", typed: "card:dep-parent"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			f := validCard(repo)
			f.label, f.depends = tc.label, tc.dep
			mustPush(t, ctx, client, f.render(), tc.place)
			got := client.HGet(ctx, keyCard(tc.label), "depends_on_typed").Val()
			if got != tc.typed {
				t.Fatalf("depends_on_typed = %q, want %q", got, tc.typed)
			}
		})
	}
}

func TestCardReleaseFreesOnMergedPR(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	const refName = "mas-bandwidth/nova-tools#3476"
	for _, label := range []string{"wait-pr-a", "wait-pr-b"} {
		f := validCard(repo)
		f.label, f.depends = label, refName
		mustPush(t, ctx, client, f.render(), "waiting")
	}

	refs := newDependsRefs()
	refs.set(refName, deal.Ref{IsPR: true, State: "open", Base: "dev"})
	res := card.ReleaseWith(ctx, client, sprint, refs)
	if res.Code != 0 || !strings.Contains(res.Stdout, "moved=0 waiting=2") {
		t.Fatalf("open PR release: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if got := refs.count(refName); got != 1 {
		t.Fatalf("open PR read %d times in one pass, want 1", got)
	}

	refs.set(refName, deal.Ref{IsPR: true, Merged: true, State: "closed", Base: "dev"})
	res = card.ReleaseWith(ctx, client, sprint, refs)
	if res.Code != 0 || !strings.Contains(res.Stdout, "moved=2 waiting=0") {
		t.Fatalf("merged PR release: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if got := refs.count(refName); got != 2 {
		t.Fatalf("PR read total %d after two passes, want one per pass", got)
	}
	for _, label := range []string{"wait-pr-a", "wait-pr-b"} {
		assertPlace(t, ctx, client, label, true, false)
	}
}

func TestCardReleaseFreesOnDoneTask(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	client.HSet(ctx, "task:build-index", "state", "open")
	f := validCard(srv.URL + "/acme/public.git")
	f.label, f.depends = "wait-task", "task:build-index"
	mustPush(t, ctx, client, f.render(), "waiting")

	refs := newDependsRefs()
	res := card.ReleaseWith(ctx, client, sprint, refs)
	if res.Code != 0 || !strings.Contains(res.Stdout, "moved=0 waiting=1") {
		t.Fatalf("open task release: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	client.HSet(ctx, "task:build-index", "state", "closed")
	res = card.ReleaseWith(ctx, client, sprint, refs)
	if res.Code != 0 || !strings.Contains(res.Stdout, "moved=1 waiting=0") {
		t.Fatalf("done task release: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	assertPlace(t, ctx, client, f.label, true, false)
}

func TestCardReleaseFreesOnClosedIssueLandedStreamAndDoneCard(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	client.HSet(ctx, "s:"+sprint+":stream:nova-pulse", "state", "open")

	for _, tc := range []struct {
		label string
		dep   string
	}{
		{label: "wait-issue", dep: "mas-bandwidth/nova-tools#3503"},
		{label: "wait-stream", dep: "stream/nova-pulse"},
		{label: "wait-card-done", dep: "done-without-pr"},
	} {
		if tc.label == "wait-card-done" {
			parent := validCard(repo)
			parent.label = tc.dep
			mustPush(t, ctx, client, parent.render(), "pool")
		}
		f := validCard(repo)
		f.label, f.depends = tc.label, tc.dep
		mustPush(t, ctx, client, f.render(), "waiting")
	}
	client.HSet(ctx, "s:"+sprint+":stream:nova-pulse", "state", "landed")
	client.HSet(ctx, keyCard("done-without-pr"), "state", "ended", "outcome", "DONE", "pushed_sha", "")
	refs := newDependsRefs()
	refs.set("mas-bandwidth/nova-tools#3503", deal.Ref{State: "closed"})

	res := card.ReleaseWith(ctx, client, sprint, refs)
	if res.Code != 0 || !strings.Contains(res.Stdout, "moved=3 waiting=0") {
		t.Fatalf("typed release: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	for _, label := range []string{"wait-issue", "wait-stream", "wait-card-done"} {
		assertPlace(t, ctx, client, label, true, false)
	}
}

func TestCardPushRefusesUnknownCardId(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label, f.depends = "unknown-child", "missing-parent"
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "DEPENDS-ON: missing-parent is not a card in sprint "+sprint) {
		t.Fatalf("exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	assertAbsent(t, ctx, client, f.label)
}

// A task or stream dependency whose record does not exist in the sprint is
// refused at push, naming the record: nothing may park in waiting on a record
// that nothing will ever write (#3503 bullet 2).
func TestCardPushRefusesUnknownTaskAndStream(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	for _, tc := range []struct {
		label, dep, want string
		seed             string // task id seeded with state cancelled before push
	}{
		{label: "cancelled-task-child", dep: "task:cancelled-task", seed: "cancelled-task",
			want: "DEPENDS-ON: task:cancelled-task is cancelled in sprint " + sprint + " and will never finish"},
		{label: "unknown-task-child", dep: "task:missing-task",
			want: "DEPENDS-ON: task:missing-task has no record task:missing-task in sprint " + sprint},
		{label: "unknown-stream-child", dep: "stream/missing-stream",
			want: "DEPENDS-ON: stream/missing-stream has no record s:" + sprint + ":stream:missing-stream in sprint " + sprint},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if tc.seed != "" {
				if err := client.HSet(ctx, "task:"+tc.seed, "state", "cancelled").Err(); err != nil {
					t.Fatal(err)
				}
			}
			f := validCard(repo)
			f.label, f.depends = tc.label, tc.dep
			res := card.Push(ctx, client, sprint, f.render())
			if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, tc.want) {
				t.Fatalf("exit %d stdout %q stderr %q, want refusal %q", res.Code, res.Stdout, res.Stderr, tc.want)
			}
			assertAbsent(t, ctx, client, f.label)
		})
	}
}
