//go:build functional

package reconcile_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/redis/go-redis/v9"
)

// fakeCloser is the forge: it records every close and never reaches a host.
type fakeCloser struct{ calls []string }

func (f *fakeCloser) CloseIssue(_ context.Context, repo string, n int, comment string) error {
	f.calls = append(f.calls, repo+"#"+strconv.Itoa(n)+" "+comment)
	return nil
}

// doneAlreadyMirror is a bare mirror whose dev holds the landed commit, with
// a second commit on a branch that never reached dev.
func doneAlreadyMirror(t *testing.T) (mirror, landed, stray string) {
	t.Helper()
	dir := t.TempDir()
	git := func(in string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	mirror = filepath.Join(dir, "nova-tools.git")
	git(dir, "init", "-q", "--bare", mirror)
	seed := filepath.Join(dir, "seed")
	git(dir, "init", "-q", seed)
	git(seed, "commit", "-q", "--allow-empty", "-m", "base")
	git(seed, "commit", "-q", "--allow-empty", "-m", "the card's work, landed via stream i")
	landed = git(seed, "rev-parse", "HEAD")
	git(seed, "push", "-q", mirror, "HEAD:refs/heads/dev")
	git(seed, "checkout", "-q", "-b", "side")
	git(seed, "commit", "-q", "--allow-empty", "-m", "never on dev")
	stray = git(seed, "rev-parse", "HEAD")
	git(seed, "push", "-q", mirror, "HEAD:refs/heads/side")
	return mirror, landed, stray
}

// TestAbstainDoneAlreadyClosesIssue is the DONE-WHEN control of
// nova-tools#3919 (reconciler half): a card ended `ABSTAIN done-already
// <sha>` whose sha is on its base in the mirror has its origin issue closed
// once, by the fake forge, with the evidence comment; its record says
// done_already=closed, it leaves the queue, and the sprint task naming the
// issue moves to landed. A second pass does nothing. A sha known to the
// mirror and not on the base is refused on the record with no close; a sha
// the mirror does not know yet stays queued; a stale fence reaches no forge.
func TestAbstainDoneAlreadyClosesIssue(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	_, client := newSprint(t)
	const S = "swarm-0925a"
	must(t, client.SAdd(ctx, "sprints", S).Err())
	must(t, client.HSet(ctx, "s:"+S, "status", "open").Err())
	mirror, landed, stray := doneAlreadyMirror(t)
	origin := "https://forge.test/mas-bandwidth/nova-tools/issues/3804"
	for label, sha := range map[string]string{"i008": landed[:8], "i009": stray, "i010": strings.Repeat("e", 40)} {
		must(t, client.HSet(ctx, card.CardKey(S, label), map[string]string{
			"state": "ended", "outcome": "ABSTAIN", "where": "done", "where_ok": "abstain", "bench": "b1",
			"repo": "mas-bandwidth/nova-tools", "base": "dev", "origin": origin,
			"result_line2": "ABSTAIN done-already " + sha,
		}).Err())
		must(t, client.ZAdd(ctx, reconcile.DoneAlreadyKey(S), redis.Z{Score: 1, Member: label}).Err())
	}
	// The sprint task that names the issue (build-<n>-<slug>), working.
	must(t, client.HSet(ctx, "task:build-3804-ci-nomirror", "stream", "swarm", "state", "working", "repo", "mas-bandwidth/nova-tools").Err())
	must(t, client.ZAdd(ctx, "ws:swarm:working", redis.Z{Score: 5, Member: "build-3804-ci-nomirror"}).Err())
	must(t, client.SAdd(ctx, "ref:nova-tools#3804:tasks", "build-3804-ci-nomirror").Err())

	forge := &fakeCloser{}
	d := &reconcile.DoneAlready{Client: client, Forge: forge,
		Mirror: func(repo string) string {
			if repo != "mas-bandwidth/nova-tools" {
				t.Errorf("mirror asked for %q", repo)
			}
			return mirror
		}}

	// A deposed instance reaches no forge and writes nothing.
	if _, err := d.Pass(ctx, "rc-0.old"); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("stale fence pass: %v, want FENCED", err)
	}
	if len(forge.calls) != 0 {
		t.Fatalf("a stale fence closed %v", forge.calls)
	}
	for _, l := range []string{"i008", "i009", "i010"} {
		must(t, client.Del(ctx, reconcile.DoneAlreadyTryKey(S, l)).Err())
	}

	out, err := d.Pass(ctx, fence)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range out {
		got[o.Label] = o.Action
	}
	if got["i008"] != "CLOSED" || got["i009"] != "REFUSED" || got["i010"] != "WAIT" {
		t.Fatalf("first pass %v, want i008 CLOSED, i009 REFUSED, i010 WAIT", out)
	}
	want := "mas-bandwidth/nova-tools#3804 DONE-ALREADY: " + landed[:8] + " on dev (card i008, bench b1)"
	if len(forge.calls) != 1 || forge.calls[0] != want {
		t.Fatalf("forge calls %q, want exactly %q", forge.calls, want)
	}
	h := hashOf(t, ctx, client, S, "i008")
	if h["done_already"] != "closed" || h["done_already_sha"] != landed[:8] || h["done_already_why"] != strings.TrimPrefix(want, "mas-bandwidth/nova-tools#3804 ") {
		t.Fatalf("i008 record %v", h)
	}
	if st, _ := client.HGet(ctx, "task:build-3804-ci-nomirror", "state").Result(); st != "landed" {
		t.Fatalf("task naming the issue is %q, want landed", st)
	}
	if h := hashOf(t, ctx, client, S, "i009"); h["done_already"] != "refused" || !strings.Contains(h["done_already_why"], "not on dev") {
		t.Fatalf("i009 record %v, want refused: not on dev", h)
	}
	queue, err := client.ZRange(ctx, reconcile.DoneAlreadyKey(S), 0, -1).Result()
	if err != nil || len(queue) != 1 || queue[0] != "i010" {
		t.Fatalf("queue %v %v, want only i010 (the mirror does not know its sha yet)", queue, err)
	}

	// A second pass does nothing: i008 is off the queue, whatever its budget.
	must(t, client.Del(ctx, reconcile.DoneAlreadyTryKey(S, "i008")).Err())
	before := hashOf(t, ctx, client, S, "i008")
	out, err = d.Pass(ctx, fence)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Label != "i010" || o.Action != "BUDGET" {
			t.Fatalf("second pass acted: %v", out)
		}
	}
	if len(forge.calls) != 1 {
		t.Fatalf("second pass reached the forge: %q", forge.calls)
	}
	if after := hashOf(t, ctx, client, S, "i008"); after["done_already_at"] != before["done_already_at"] {
		t.Fatalf("second pass rewrote the record: %v -> %v", before, after)
	}
}
