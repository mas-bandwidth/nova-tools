package card_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/redis/go-redis/v9"
)

// cmdRecorder is a go-redis hook that records every command the client
// sends: singles one by one, pipelines as one entry each.
type cmdRecorder struct {
	mu      sync.Mutex
	singles []string
	pipes   [][]string
}

func (r *cmdRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *cmdRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.mu.Lock()
		r.singles = append(r.singles, strings.ToLower(fmt.Sprint(cmd.Args()...)))
		r.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (r *cmdRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		names := make([]string, len(cmds))
		for i, c := range cmds {
			names[i] = strings.ToLower(c.Name())
		}
		r.mu.Lock()
		r.pipes = append(r.pipes, names)
		r.mu.Unlock()
		return next(ctx, cmds)
	}
}

// functionCalls counts FUNCTION commands (LOAD, LIST, ...) sent singly or piped.
func (r *cmdRecorder) functionCalls() int {
	n := 0
	for _, s := range r.singles {
		if strings.HasPrefix(s, "function") {
			n++
		}
	}
	for _, p := range r.pipes {
		for _, c := range p {
			if c == "function" {
				n++
			}
		}
	}
	return n
}

func recorded(t *testing.T, client *redis.Client) *cmdRecorder {
	t.Helper()
	rec := &cmdRecorder{}
	client.AddHook(rec)
	return rec
}

// TestCardPushStoresLeg is the DONE-WHEN of #3255 (store side): the LEG line
// is stored lower case as the card's leg field; a card with no LEG line has
// none; an empty or multi-leg LEG line is refused before any write.
func TestCardPushStoresLeg(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label, line, want string
	}{
		{"leg-rust", "LEG: rust", "rust"},
		{"leg-go-upper", "LEG: Go", "go"},
		{"leg-absent", "", ""},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		var body []byte
		if tc.line == "" {
			body = f.render()
		} else {
			body = withHeader(f, tc.line)
		}
		if res := card.Push(ctx, client, sprint, body); res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q", tc.label, res.Code, res.Stderr)
		}
		got, err := client.HGet(ctx, keyCard(tc.label), "leg").Result()
		if tc.want == "" {
			if err != redis.Nil {
				t.Fatalf("%s: leg %q (%v), want no leg field", tc.label, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s: HGET leg = %q (%v), want %q", tc.label, got, err, tc.want)
		}
	}
	// LEG (argument 19), TEST (16), STREAM (17) and ORIGIN (18) are all
	// stored through CARD.create: no argument shadows another.
	both := validCard(srv.URL + "/acme/public.git")
	both.label = "leg-and-test"
	if res := card.Push(ctx, client, sprint, withHeader(both, "LEG: sbcl", "TEST: ./internal/x TestY", "STREAM: swarm-cards", "ORIGIN: acme/public#7")); res.Code != 0 {
		t.Fatalf("leg-and-test: exit %d stderr %q", res.Code, res.Stderr)
	}
	want := "[sbcl ./internal/x TestY swarm-cards acme/public#7]"
	if got := client.HMGet(ctx, keyCard("leg-and-test"), "leg", "test", "stream", "origin").Val(); fmt.Sprint(got) != want {
		t.Fatalf("leg-and-test: leg, test, stream, origin = %v, want %s", got, want)
	}
	for i, line := range []string{"LEG:", "LEG: go, rust", "LEG: go rust"} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = fmt.Sprintf("bad-leg-%d", i)
		res := card.Push(ctx, client, sprint, withHeader(f, line))
		if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "LEG") {
			t.Fatalf("%q: exit %d stdout %q stderr %q, want exit 2 naming LEG", line, res.Code, res.Stdout, res.Stderr)
		}
		assertAbsent(t, ctx, client, f.label)
	}
}

// TestDealLegFilterReadsPushedLeg is the DONE-WHEN of #3255 (deal side): a
// card pushed with LEG: rust is read back by the deal's RedisSource with its
// leg, and a bench whose profile is go leaves it in the pool while it deals
// the LEG: go card beside it.
func TestDealLegFilterReadsPushedLeg(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	rust := validCard(srv.URL + "/acme/public.git")
	rust.label = "deal-rust"
	goCard := validCard(srv.URL + "/acme/public.git")
	goCard.label = "deal-go"
	for _, res := range card.PushBatch(ctx, client, sprint, []card.CardFile{
		{Name: "rust.md", Body: withHeader(rust, "LEG: rust")},
		{Name: "go.md", Body: withHeader(goCard, "LEG: go")},
	}, card.PushOptions{}) {
		if res.Code != 0 || !strings.Contains(res.Stdout, "place=pool") {
			t.Fatalf("push: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
		}
	}
	client.SAdd(ctx, "benches", "ctl-go")
	client.HSet(ctx, "bench:ctl-go:desired", "slots", "8", "legs", "go")
	client.HSet(ctx, "bench:ctl-go:beat", "host", "ctl-go.tail")
	client.HSet(ctx, "bench:ctl-go:state", "state", "UP", "at", "1")
	client.SAdd(ctx, "sprints", sprint)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	client.HSet(ctx, "s:"+sprint, "status", "open")

	in, err := deal.RedisSource{Client: client}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legs := map[string]string{}
	for _, s := range in.Sprints {
		for _, c := range s.Pool {
			legs[c.Label] = c.Leg
		}
	}
	if legs["deal-rust"] != "rust" || legs["deal-go"] != "go" {
		t.Fatalf("deal read legs %v, want deal-rust=rust deal-go=go", legs)
	}
	plan := deal.Plan(in, time.Minute)
	var dealt []string
	for _, b := range plan {
		for _, c := range b.Cards {
			dealt = append(dealt, b.Bench.Name+"/"+c.Label)
		}
	}
	if strings.Join(dealt, ",") != "ctl-go/deal-go" {
		t.Fatalf("plan dealt %v, want only ctl-go/deal-go (the rust card stays in the pool)", dealt)
	}
}

// TestCardPushBatchIsOnePipeline is the DONE-WHEN of #3266: pushing 12 cards
// with a loaded library sends no FUNCTION command and exactly one round trip,
// one pipeline holding the 12 FCALLs.
func TestCardPushBatchIsOnePipeline(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	files := make([]card.CardFile, 12)
	for i := range files {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = fmt.Sprintf("batch-%02d", i)
		files[i] = card.CardFile{Name: f.label + ".md", Body: withHeader(f, "LEG: go")}
	}
	rec := recorded(t, client)
	results := card.PushBatch(ctx, client, sprint, files, card.PushOptions{})
	if len(results) != 12 {
		t.Fatalf("%d results, want 12: %+v", len(results), results)
	}
	for i, res := range results {
		want := fmt.Sprintf("label=batch-%02d place=pool\n", i)
		if res.Code != 0 || !strings.HasSuffix(res.Stdout, want) {
			t.Fatalf("card %d: exit %d stdout %q stderr %q", i, res.Code, res.Stdout, res.Stderr)
		}
	}
	if n := rec.functionCalls(); n != 0 {
		t.Fatalf("card push sent %d FUNCTION commands, want 0 (singles %v, pipes %v)", n, rec.singles, rec.pipes)
	}
	if len(rec.singles) != 0 || len(rec.pipes) != 1 {
		t.Fatalf("card push of 12 made %d single calls and %d pipelines, want 0 and 1: singles %v pipes %v", len(rec.singles), len(rec.pipes), rec.singles, rec.pipes)
	}
	if got := rec.pipes[0]; len(got) != 12 || got[0] != "fcall" || got[11] != "fcall" {
		t.Fatalf("pipeline %v, want 12 fcall", got)
	}
	for i := range files {
		if leg := client.HGet(ctx, keyCard(fmt.Sprintf("batch-%02d", i)), "leg").Val(); leg != "go" {
			t.Fatalf("batch-%02d leg %q, want go", i, leg)
		}
	}
}

// TestCardPushBatchDependsOnEarlierCard: a card that depends on a card pushed
// earlier in the same batch waits, as it would have pushed one at a time; the
// dependency reads are one more pipeline, not one per card.
func TestCardPushBatchDependsOnEarlierCard(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	parent := validCard(srv.URL + "/acme/public.git")
	parent.label = "batch-parent"
	child := validCard(srv.URL + "/acme/public.git")
	child.label = "batch-child"
	child.depends = "batch-parent"
	rec := recorded(t, client)
	results := card.PushBatch(ctx, client, sprint, []card.CardFile{{Body: parent.render()}, {Body: child.render()}}, card.PushOptions{})
	if len(results) != 2 || !strings.Contains(results[0].Stdout, "place=pool") || !strings.Contains(results[1].Stdout, "place=waiting") {
		t.Fatalf("results %+v, want parent pool and child waiting", results)
	}
	if len(rec.singles) != 0 || len(rec.pipes) != 2 {
		t.Fatalf("%d singles and %d pipelines, want 0 and 2: %v %v", len(rec.singles), len(rec.pipes), rec.singles, rec.pipes)
	}
	assertPlace(t, ctx, client, "batch-child", false, true)

	// A dependency on a card later in the batch has no record at push time:
	// the whole batch is refused before any write.
	early := validCard(srv.URL + "/acme/public.git")
	early.label = "batch-early"
	early.depends = "batch-late"
	late := validCard(srv.URL + "/acme/public.git")
	late.label = "batch-late"
	results = card.PushBatch(ctx, client, sprint, []card.CardFile{{Name: "early.md", Body: early.render()}, {Name: "late.md", Body: late.render()}}, card.PushOptions{})
	if len(results) != 1 || results[0].Code != 2 || !strings.Contains(results[0].Stderr, "early.md: DEPENDS-ON: batch-late is not a card") {
		t.Fatalf("results %+v, want one refusal naming early.md and batch-late", results)
	}
	assertAbsent(t, ctx, client, "batch-early")
	assertAbsent(t, ctx, client, "batch-late")
}

// TestCardPushNeverLoadsTheLibrary: on a server with no nova_sprint library,
// card push sends no FUNCTION command, stores nothing, and refuses naming the
// remedy nova-sprint fn load.
func TestCardPushNeverLoadsTheLibrary(t *testing.T) {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: startPushRedis(t)})
	t.Cleanup(func() { _ = client.Close() })
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "no-library"
	rec := recorded(t, client)
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 2 || !strings.Contains(res.Stderr, "nova-sprint fn load") {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 2 naming nova-sprint fn load", res.Code, res.Stdout, res.Stderr)
	}
	if n := rec.functionCalls(); n != 0 {
		t.Fatalf("card push sent %d FUNCTION commands on a server without the library", n)
	}
	assertAbsent(t, ctx, client, f.label)
}
