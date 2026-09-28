//go:build functional

package card_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// snapshot is every key's value a refused call must leave as it was.
func snapshot(t *testing.T, ctx context.Context, client *redis.Client, keys ...string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, k := range keys {
		switch client.Type(ctx, k).Val() {
		case "hash":
			out[k] = client.HGetAll(ctx, k).Val()
		case "zset":
			out[k] = client.ZRangeWithScores(ctx, k, 0, -1).Val()
		case "none":
			out[k] = nil
		default:
			t.Fatalf("snapshot %s: type %s", k, client.Type(ctx, k).Val())
		}
	}
	return out
}

// TestPathsMoveRefusesPathsFields is probe (a) of the #4405 fix round
// (#4322): the cold reader's exact call, FCALL ns_tcard_move 0 m1 waiting
// rowan probe stream zeta stream_paths pkg/evil, was MOVED because the gate
// read the stored field before the move's HSET wrote the new one. Now a
// move carrying stream_paths or paths is REFUSED and writes nothing (the
// record, its sets, zeta's and ws:paths as they were), in TK.move and in
// card_move (driven through a test function appended to the library, as no
// door hands card_move a paths field); a card end's result still writes
// paths (TM.finish's result flag).
func TestPathsMoveRefusesPathsFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, c := range [][3]string{{"m1", "mine", "pkg/good"}, {"ev", "evil", "pkg/evil"}} {
		if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: c[0], Stream: c[1], Title: c[0], By: "test",
			Fields: []string{"paths", c[2]}}); err != nil {
			t.Fatal(err)
		}
	}
	keys := []string{"task:m1", "ws:mine:waiting", "ws:zeta:waiting", ws.PathsKey}
	before := snapshot(t, ctx, client, keys...)
	for _, f := range []string{"stream_paths", "paths"} {
		got, err := client.FCall(ctx, "ns_tcard_move", nil, "m1", "waiting", "rowan", "probe", "stream", "zeta", f, "pkg/evil").Text()
		if err != nil || !strings.HasPrefix(got, "REFUSED FIELD "+f+" ") {
			t.Fatalf("ns_tcard_move ... %s pkg/evil = %q %v; want REFUSED FIELD %s", f, got, err, f)
		}
		if after := snapshot(t, ctx, client, keys...); !reflect.DeepEqual(before, after) {
			t.Fatalf("the refused move with %s wrote: %v -> %v", f, before, after)
		}
	}

	// card_move: the same refusal, through NS.card.move
	src, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	src += "\ndo\nredis.register_function('ns_test_card_move', function(keys, args)\n" +
		"  return NS.card.move(args[1], args[2], { by = 'test', why = 'probe', fields = { args[3], args[4] } }) or 'MOVED'\n" +
		"end)\nend\n"
	if err := client.FunctionLoadReplace(ctx, src).Err(); err != nil {
		t.Fatal(err)
	}
	if res := card.Push(ctx, client, sprint, pathsCard(srv.URL, "cm1", "mine", "pkg/good/c.go")); res.Code != 0 {
		t.Fatalf("push cm1: %d %q %q", res.Code, res.Stdout, res.Stderr)
	}
	ckeys := []string{keyCard("cm1"), "ws:mine:waiting", "ws:mine:ready", ws.PathsKey}
	cbefore := snapshot(t, ctx, client, ckeys...)
	for _, f := range []string{"stream_paths", "paths"} {
		got, err := client.FCall(ctx, "ns_test_card_move", nil, keyCard("cm1"), "waiting", f, "pkg/evil").Text()
		if err != nil || !strings.HasPrefix(got, "FIELD "+f+" ") {
			t.Fatalf("card_move with %s = %q %v; want FIELD %s", f, got, err, f)
		}
		if after := snapshot(t, ctx, client, ckeys...); !reflect.DeepEqual(cbefore, after) {
			t.Fatalf("the refused card move with %s wrote", f)
		}
	}
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsRepairRacesPush is probe (b) of the #4405 fix round (#4322):
// ws check --repair runs in one FCALL (SP.repair) over the live sets as
// they are then. First the reader's interleaving made deterministic: the
// repair's read, then a push onto the stream it will rewrite, then the
// repair's call with that read; the pushed path stays. Then 40 trials of
// the repair racing a push: each trial adds a record cut before #4322 (no
// stream_paths) to stream hot so the repair rewrites hot, and pushes a
// task with race/<i> onto hot beside it. No trial drops the pushed path
// (ws:paths hot holds it and the legacy record's), and a push of
// race/<i>/x.go onto another stream is refused every time (no overlap
// admitted).
func TestPathsRepairRacesPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	push := func(id, stream, paths string) error {
		_, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: id, Stream: stream, Title: id, By: "test",
			Fields: []string{"paths", paths}})
		return err
	}
	legacy := func(id, paths string) {
		client.HSet(ctx, "task:"+id, "where", "waiting", "where_ok", "-", "state", "waiting", "stream", "hot",
			"title", id, "paths", paths, "created_at", "1")
		client.ZAdd(ctx, "ws:hot:waiting", redis.Z{Score: 1, Member: id})
	}
	holds := func(want ...string) error {
		got := ws.ParsePaths(client.HGet(ctx, ws.PathsKey, "hot").Val())
		for _, w := range want {
			found := false
			for _, g := range got {
				found = found || g == w
			}
			if !found {
				return fmt.Errorf("ws:paths hot = %v lacks %s", got, w)
			}
		}
		return nil
	}
	repair := func() (ws.Repair, error) {
		_, _, cards, err := ws.LivePaths(ctx, client)
		if err != nil {
			return ws.Repair{}, err
		}
		return ws.RepairPaths(ctx, client, cards)
	}
	if err := push("h0", "hot", "hot/base"); err != nil {
		t.Fatal(err)
	}

	legacy("leg-s", "legacy/s")
	_, _, cards, err := ws.LivePaths(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := push("p-s", "hot", "race/s"); err != nil {
		t.Fatal(err)
	}
	if r, err := ws.RepairPaths(ctx, client, cards); err != nil || r.Records != 1 || len(r.Refused) != 0 {
		t.Fatalf("repair with the read taken before the push: %+v %v", r, err)
	}
	if err := holds("hot/base", "legacy/s", "race/s"); err != nil {
		t.Fatalf("interleaved: %v", err)
	}

	const trials = 40
	dropped, admitted := 0, 0
	for i := 0; i < trials; i++ {
		legacy(fmt.Sprintf("leg-%d", i), fmt.Sprintf("legacy/%d", i))
		var wg sync.WaitGroup
		var rerr, perr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, rerr = repair()
		}()
		go func() {
			defer wg.Done()
			<-start
			perr = push(fmt.Sprintf("p-%d", i), "hot", fmt.Sprintf("race/%d", i))
		}()
		close(start)
		wg.Wait()
		if rerr != nil || perr != nil {
			t.Fatalf("trial %d: repair %v push %v", i, rerr, perr)
		}
		if _, err := repair(); err != nil { // the legacy record the race's read may have missed
			t.Fatal(err)
		}
		if err := holds(fmt.Sprintf("race/%d", i), fmt.Sprintf("legacy/%d", i)); err != nil {
			dropped++
			t.Errorf("trial %d: %v", i, err)
		}
		err := push(fmt.Sprintf("q-%d", i), "cold", fmt.Sprintf("race/%d/x.go", i))
		if why, _ := taskcard.IsRefused(err); !strings.HasPrefix(why, "PATHS overlap ") || !strings.HasSuffix(why, " stream=hot") {
			admitted++
			t.Errorf("trial %d: the overlapping push onto cold: %v", i, err)
		}
	}
	if dropped+admitted != 0 {
		t.Fatalf("%d of %d trials dropped the pushed path, %d admitted an overlap", dropped, trials, admitted)
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsAdoptReapRelinkGated is probe (c) of the #4405 fix round
// (#4322): the writes that link a record into a stream's live set outside
// the one move are gated (SP.relink, SP.gate on the record's stored
// stream_paths) and refused on an overlap, nothing linked. TK.adopt (a
// task record that predates the where field, adopted by its first move):
// REFUSED with the PATHS overlap, the record unplaced; a disjoint one is
// adopted and its paths join its stream. TK.reap (a stray in a friend's
// working set whose record says ready but is missing from its stream's
// ready set): the stray leaves the friend's set, its stream view is not
// relinked, and the ws:log receipt names the refusal. cm_adopt (a card
// record that predates the model) and card fsck --repair's relink of a
// card's stream view, the doors the card's list missed: refused the same
// way. TK.place is removed: task migrate's placer had no caller.
func TestPathsAdoptReapRelinkGated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	push := func(id, stream, paths string) {
		t.Helper()
		if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: id, Stream: stream, Title: id, By: "test",
			Fields: []string{"paths", paths}}); err != nil {
			t.Fatal(err)
		}
	}
	push("h-1", "holder", "adopt/p")

	// TK.adopt
	for _, c := range [][2]string{{"ad-1", "adopt/p/x.go"}, {"ad-2", "adopt/q"}} {
		client.HSet(ctx, "task:"+c[0], "state", "open", "stream", "adopter", "title", c[0], "paths", c[1],
			ws.PathsField, c[1], "created_at", "1")
	}
	got, err := client.FCall(ctx, "ns_tcard_move", nil, "ad-1", "ready", "test", "adopt").Text()
	if err != nil || got != "REFUSED PATHS overlap paths=adopt/p,adopt/p/x.go stream=holder" {
		t.Fatalf("adopt ad-1 over holder: %q %v", got, err)
	}
	if client.HExists(ctx, "task:ad-1", "where").Val() || client.ZScore(ctx, "ws:adopter:ready", "ad-1").Err() != redis.Nil {
		t.Fatal("the refused adoption placed ad-1")
	}
	if got, err := client.FCall(ctx, "ns_tcard_move", nil, "ad-2", "ready", "test", "adopt").Text(); err != nil || !strings.HasPrefix(got, "SAME") && !strings.HasPrefix(got, "MOVED") {
		t.Fatalf("adopt ad-2: %q %v", got, err)
	}
	if v := client.HGet(ctx, ws.PathsKey, "adopter").Val(); v != "adopt/q" {
		t.Fatalf("ws:paths adopter = %q after ad-2's adoption; want adopt/q", v)
	}

	// TK.reap
	push("rp-1", "reaped", "reap/p")
	if _, err := taskcard.Move(ctx, client, "rp-1", "ready", taskcard.Opts{By: "test"}); err != nil {
		t.Fatal(err)
	}
	client.ZRem(ctx, "ws:reaped:ready", "rp-1") // the stream view lost; its paths released with it
	client.HSet(ctx, ws.PathsKey, "reaped", "")
	push("rp-2", "taker", "reap/p/x.go")
	client.SAdd(ctx, "friends", "reaper")
	client.ZAdd(ctx, "friend:reaper:cards:working", redis.Z{Score: 1, Member: "rp-1"})
	if _, err := client.FCall(ctx, "ns_tcard_expire", nil, "test", "reaper").Result(); err != nil {
		t.Fatal(err)
	}
	if client.ZScore(ctx, "ws:reaped:ready", "rp-1").Err() != redis.Nil || client.ZScore(ctx, "friend:reaper:cards:working", "rp-1").Err() != redis.Nil {
		t.Fatal("reap relinked rp-1 into reaped's ready set over taker's paths, or left the stray")
	}
	last := client.XRevRangeN(ctx, "ws:log", "+", "-", 1).Val()
	if len(last) != 1 || !strings.Contains(fmt.Sprint(last[0].Values["why"]), "views not relinked: PATHS overlap paths=reap/p,reap/p/x.go stream=taker") {
		t.Fatalf("reap's receipt: %v", last)
	}

	// cm_adopt: a card record that predates the model
	ca := keyCard("ca-1")
	client.HSet(ctx, ca, "state", "queued", "stream", "cadopt", "bench", "", "paths", "adopt/p", ws.PathsField, "adopt/p",
		"created_at", "1")
	if got, err := client.FCall(ctx, "ns_card_move", nil, ca, "ready", "", "test", "adopt").Text(); err != nil || got != "REFUSED PATHS overlap paths=adopt/p stream=holder" {
		t.Fatalf("card adopt over holder: %q %v", got, err)
	}
	if client.ZScore(ctx, "ws:cadopt:waiting", ca).Err() != redis.Nil || client.ZScore(ctx, "sprint:"+sprint+":cards", ca).Err() != redis.Nil {
		t.Fatal("the refused card adoption linked ca-1")
	}

	// card fsck --repair: a card's lost stream view is not relinked over
	// another stream's paths
	srv := repoServer(t)
	if res := card.Push(ctx, client, sprint, pathsCard(srv.URL, "fx-1", "fixed", "fsck/p")); res.Code != 0 {
		t.Fatalf("push fx-1: %d %q", res.Code, res.Stdout)
	}
	fxw := client.HGet(ctx, keyCard("fx-1"), "where").Val()
	client.ZRem(ctx, "ws:fixed:"+fxw, keyCard("fx-1"))
	client.HSet(ctx, ws.PathsKey, "fixed", "")
	push("fx-2", "fixer", "fsck/p/x.go")
	out, err := client.FCall(ctx, "ns_card_repair", nil, sprint).StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(out, "\n"), "repair refused PATHS overlap paths=fsck/p,fsck/p/x.go stream=fixer") {
		t.Fatalf("card repair: %q; want the relink refused", out)
	}
	if client.ZScore(ctx, "ws:fixed:"+fxw, keyCard("fx-1")).Err() != redis.Nil {
		t.Fatal("card fsck --repair relinked fx-1 over fixer's paths")
	}

	src, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	for _, dead := range []string{"TK.place", "NS.task.place", "W.migrate_one", "ns_ws_migrate"} {
		if strings.Contains(src, dead) {
			t.Fatalf("the library still names %s", dead)
		}
	}
}
