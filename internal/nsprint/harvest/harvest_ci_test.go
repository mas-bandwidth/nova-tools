package harvest_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// #3717 DONE-WHEN: a harvested fixture card leaves ci:pool holding its head
// and the request record pending, through ci.Request (the function `ci
// request` calls), idempotent on the sha and with no GitHub call; a head
// already requested is EXISTS and its record is left as it was.
func TestHarvestRequestsCIForTheHead(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	bench := "ctl-ci"
	c.HSet(ctx, "bench:"+bench+":beat", "host", bench+".tailnet", "user", "nova")
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	forge := newForge()
	fresh, known := "ci-card-fresh", "ci-card-known"
	for _, label := range []string{fresh, known} {
		seedEnded(t, c, bench, label, "model", "DONE", sha(label))
		forge.heads["nova/"+sprint+"/"+label+"-a1"] = sha(label)
	}
	// The known head was requested before the harvest and a bench is on it.
	if r, err := ci.Request(ctx, st, ci.RequestRequest{Repo: "nova-tools", SHA: sha(known)}); err != nil || r.Status != "CREATED" {
		t.Fatalf("seed request = %+v, %v", r, err)
	}
	c.HSet(ctx, ci.RecordKey("nova-tools", sha(known)), "attempt", "1", "bench", "hulk")
	readsBefore := forge.reads

	res := byBench(harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{bench},
		Clock: 2 * time.Second, Instance: "ci-run-1", Forge: forge, Pusher: &fixturePusher{pushes: map[string]int{}}}))
	r := res[bench]
	if r.Err != nil || len(r.Failed) != 0 || len(r.Cards) != 2 {
		t.Fatalf("pass = %+v; want both cards harvested", r)
	}
	word := map[string]string{}
	pr := map[string]int{}
	for _, cr := range r.Cards {
		word[cr.Label], pr[cr.Label] = cr.CI, cr.PR
	}
	if word[fresh] != "CREATED" || word[known] != "EXISTS" {
		t.Fatalf("ci words = %v; want %s CREATED and %s EXISTS", word, fresh, known)
	}
	for _, label := range []string{fresh, known} {
		if _, err := c.ZScore(ctx, ci.PoolKey, "nova-tools:"+sha(label)).Result(); err != nil {
			t.Fatalf("ci:pool has no nova-tools:%s for %s: %v", sha(label), label, err)
		}
	}
	rec := c.HGetAll(ctx, ci.RecordKey("nova-tools", sha(fresh))).Val()
	if rec["ci"] != "pending" || rec["sha"] != sha(fresh) || rec["repo"] != "nova-tools" ||
		rec["pr"] != strconv.Itoa(pr[fresh]) || rec["attempt"] != "0" || rec["checks"] == "" {
		t.Fatalf("%s = %v; want pending, attempt 0, the PR and the declared checks", ci.RecordKey("nova-tools", sha(fresh)), rec)
	}
	for _, d := range ci.Defaults["nova-tools"] {
		if rec["cmd:"+d.Name] != d.Argv {
			t.Fatalf("record cmd:%s = %q, want %q (the checks ci request declares)", d.Name, rec["cmd:"+d.Name], d.Argv)
		}
	}
	old := c.HGetAll(ctx, ci.RecordKey("nova-tools", sha(known))).Val()
	if old["attempt"] != "1" || old["bench"] != "hulk" || old["pr"] != "" {
		t.Fatalf("known head record = %v; want it untouched (idempotent on the sha)", old)
	}
	if forge.reads != readsBefore {
		t.Fatalf("GitHub read %d times for the CI request; want none", forge.reads-readsBefore)
	}
}

// A refused CI request (cfg:ci:<repo> names a check with no argv) leaves the
// card unharvested with err=ci-request; once the config is fixed the next
// pass finishes it from the PR record (no second PR, no GitHub) and requests
// the head then.
func TestHarvestCIRefusalLeavesCardForNextPass(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	bench := "ctl-ci"
	label := "ci-card-cfg"
	c.HSet(ctx, "bench:"+bench+":beat", "host", bench+".tailnet", "user", "nova")
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	seedEnded(t, c, bench, label, "model", "DONE", sha(label))
	forge := newForge()
	forge.heads["nova/"+sprint+"/"+label+"-a1"] = sha(label)
	c.HSet(ctx, ci.ConfigKey("nova-tools"), "checks", "go-build")
	opt := harvest.Options{Sprint: sprint, Benches: []string{bench}, Clock: 2 * time.Second,
		Instance: "ci-run-1", Forge: forge, Pusher: &fixturePusher{pushes: map[string]int{}}}

	r := byBench(harvest.Run(ctx, st, opt))[bench]
	if len(r.Cards) != 0 || len(r.Failed) != 1 || r.Failed[0].Code != harvest.CodeCIRequest {
		t.Fatalf("pass = %+v; want one err=%s failure and nothing harvested", r, harvest.CodeCIRequest)
	}
	if s := c.HGet(ctx, "s:"+sprint+":card:"+label, "state").Val(); s != "ended" {
		t.Fatalf("state %s, want ended (not harvested without its CI request)", s)
	}
	if n := c.ZCard(ctx, ci.PoolKey).Val(); n != 0 {
		t.Fatalf("ci:pool holds %d after a refusal, want 0", n)
	}

	c.HSet(ctx, ci.ConfigKey("nova-tools"), "check:go-build", "go build ./...")
	opensBefore, findsBefore := forge.opens, forge.finds
	opt.Instance = "ci-run-2"
	r = byBench(harvest.Run(ctx, st, opt))[bench]
	if len(r.Failed) != 0 || len(r.Cards) != 1 || r.Cards[0].Via != "record" || r.Cards[0].CI != "CREATED" {
		t.Fatalf("re-run = %+v; want the card harvested from its PR record with ci CREATED", r)
	}
	if forge.opens != opensBefore || forge.finds != findsBefore {
		t.Fatalf("re-run opened %d and searched %d; want no GitHub", forge.opens-opensBefore, forge.finds-findsBefore)
	}
	rec := c.HGetAll(ctx, ci.RecordKey("nova-tools", sha(label))).Val()
	if rec["ci"] != "pending" || rec["checks"] != "go-build" || rec["cmd:go-build"] != "go build ./..." {
		t.Fatalf("record = %v; want pending with the declared go-build", rec)
	}
}
