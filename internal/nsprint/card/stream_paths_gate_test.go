//go:build functional

package card_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// pathsCard is a card file on stream with PATHS paths.
func pathsCard(srvURL, label, stream, paths string) []byte {
	f := validCard(srvURL + "/acme/public.git")
	f.label, f.paths = label, paths
	return withHeader(f, "STREAM: "+stream)
}

// assertDisjoint: ws check's recompute finds no two open streams sharing a
// path and no stale record.
func assertDisjoint(t *testing.T, ctx context.Context, client *redis.Client) {
	t.Helper()
	live, stored, _, err := ws.LivePaths(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if ov := live.Overlaps(); len(ov) != 0 {
		t.Fatalf("PATHS OVERLAP after the gate: %v", ov)
	}
	if s := ws.Stale(live, stored); len(s) != 0 {
		t.Fatalf("PATHS STALE after the gate: %v", s)
	}
}

// TestPathsGateRaceOneWins is the gate's atomicity (#4322, owed item 1):
// 50 trials of two pushes run concurrently, each on its own stream, their
// PATHS overlapping (one names the other's parent dir). Even trials race
// two card pushes (ns_card_push), odd trials a card push against a task
// push (ns_tcard_push). Every trial exactly one wins; the other is refused
// with the typed refusal (card push: the REFUSED PATHS overlap receipt,
// exit 2; task push: the Refused why ws.ParseRefusal reads) and wrote
// nothing; and ws check's recompute finds no overlap and no stale record.
// The Go gate this replaces let both through in 4 of 20 such trials.
func TestPathsGateRaceOneWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	const trials = 50
	for i := 0; i < trials; i++ {
		a, b := fmt.Sprintf("race-a-%d", i), fmt.Sprintf("race-b-%d", i)
		sa, sb := fmt.Sprintf("race a%d", i), fmt.Sprintf("race-b%d", i)
		pa, pb := fmt.Sprintf("race/%d/x.go", i), fmt.Sprintf("race/%d", i)
		var won [2]bool
		var why [2]string
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			res := card.Push(ctx, client, sprint, pathsCard(srv.URL, a, sa, pa))
			won[0], why[0] = res.Code == 0, res.Stdout+res.Stderr
		}()
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				res := card.Push(ctx, client, sprint, pathsCard(srv.URL, b, sb, pb))
				won[1], why[1] = res.Code == 0, res.Stdout+res.Stderr
				return
			}
			_, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: b, Stream: sb, Title: "race", By: "test",
				Fields: []string{"paths", pb}})
			won[1] = err == nil
			if err != nil {
				why[1] = err.Error()
			}
		}()
		close(start)
		wg.Wait()
		if won[0] == won[1] {
			t.Fatalf("trial %d: card %v task-or-card %v; want exactly one to win (%q | %q)", i, won[0], won[1], why[0], why[1])
		}
		loser, other, lostWhy := b, sa, why[1]
		if !won[0] {
			loser, other, lostWhy = a, sb, why[0]
		}
		wantWhy := "REFUSED PATHS overlap paths=" + pb + "," + pa + " stream=" + other
		if loser == a || i%2 == 0 {
			wantWhy = (&ws.PathsRefusal{Stream: other, Paths: []string{pb, pa}}).Receipt() + " card=" + loser + "\n"
		}
		if !strings.HasPrefix(lostWhy, wantWhy) {
			t.Fatalf("trial %d: loser %s said %q; want prefix %q", i, loser, lostWhy, wantWhy)
		}
		if client.Exists(ctx, keyCard(loser), "task:"+loser).Val() != 0 {
			t.Fatalf("trial %d: the refused %s wrote its record", i, loser)
		}
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsGateRefusesUnbuilt is owed item 3 (#4322): on a store shaped
// like the live one before ws check --repair (card records with no
// stream_paths, ws:paths deleted), every push with PATHS is refused with
// PATHS unbuilt and the repair remedy, the overlapping one and a disjoint
// one, card and task, and nothing is written; a landing on the unbuilt
// stream does not build it; a stream whose only live card names no path is
// unbuilt too. After ws check --repair (LivePaths, RepairPaths) the
// overlapping push is refused as an overlap and the disjoint one passes.
func TestPathsGateRefusesUnbuilt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, c := range [][3]string{{"ub-a", "work", "internal/nsprint/ws"}, {"ub-b", "work", "internal/nsprint/deal"}} {
		if res := card.Push(ctx, client, sprint, pathsCard(srv.URL, c[0], c[1], c[2])); res.Code != 0 {
			t.Fatalf("push %s: exit %d %q %q", c[0], res.Code, res.Stdout, res.Stderr)
		}
	}
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "ub-t", Stream: "bare", Title: "t", By: "test"}); err != nil {
		t.Fatal(err)
	}
	// the store before --repair: no record, no card's stream_paths
	client.Del(ctx, ws.PathsKey)
	client.HDel(ctx, keyCard("ub-a"), ws.PathsField)
	client.HDel(ctx, keyCard("ub-b"), ws.PathsField)

	unbuilt := `REFUSED PATHS unbuilt stream=bare remedy="nova-sprint ws check --repair"`
	for _, c := range [][3]string{{"ub-c", "ci", "internal/nsprint/ws/check.go"}, {"ub-d", "ci", "cmd/nova-sprint/ci.go"}} {
		res := card.Push(ctx, client, sprint, pathsCard(srv.URL, c[0], c[1], c[2]))
		if res.Code != 2 || res.Stdout != unbuilt+" card="+c[0]+"\n" {
			t.Fatalf("%s on an unbuilt store: exit %d stdout %q; want %q", c[0], res.Code, res.Stdout, unbuilt)
		}
		assertAbsent(t, ctx, client, c[0])
	}
	_, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "ub-u", Stream: "ci", Title: "u", By: "test",
		Fields: []string{"paths", "docs/x.md"}})
	if why, _ := taskcard.IsRefused(err); why != "PATHS unbuilt stream=bare" || client.Exists(ctx, "task:ub-u").Val() != 0 {
		t.Fatalf("task push on an unbuilt store: %v", err)
	}
	// the landing of one of work's cards leaves work unbuilt: ub-b is live
	mustLand(t, ctx, client, "ub-a", strings.Repeat("c", 40))
	if client.HExists(ctx, ws.PathsKey, "work").Val() {
		t.Fatalf("a landing built the unbuilt stream work: %q", client.HGet(ctx, ws.PathsKey, "work").Val())
	}

	live, stored, cards, err := ws.LivePaths(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ws.Stale(live, stored), ","); got != "bare,work" {
		t.Fatalf("stale = %q; want bare,work (bare holds a path-less live task and no field)", got)
	}
	if _, err := ws.RepairPaths(ctx, client, cards); err != nil {
		t.Fatal(err)
	}
	if v, err := client.HGet(ctx, ws.PathsKey, "bare").Result(); err != nil || v != "" {
		t.Fatalf("repaired bare = %q %v; want the empty field", v, err)
	}
	res := card.Push(ctx, client, sprint, pathsCard(srv.URL, "ub-c", "ci", "internal/nsprint/deal/deal.go"))
	want := `REFUSED PATHS overlap stream=work paths=internal/nsprint/deal,internal/nsprint/deal/deal.go remedy="--join work" card=ub-c` + "\n"
	if res.Code != 2 || res.Stdout != want {
		t.Fatalf("overlap after repair: exit %d stdout %q; want %q", res.Code, res.Stdout, want)
	}
	if res := card.Push(ctx, client, sprint, pathsCard(srv.URL, "ub-d", "ci", "cmd/nova-sprint/ci.go")); res.Code != 0 {
		t.Fatalf("disjoint after repair: exit %d %q %q", res.Code, res.Stdout, res.Stderr)
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsGateUnparkAndMove is owed item 2 (#4322): the gate holds on the
// two moves that put a task into a stream. Unpark: a parked stream's paths
// are free, another stream takes one of them, and scope unpark is refused
// whole (ns_ws_unpark_stream, the typed refusal naming that stream) with
// nothing leaving parked; once the other stream's task is cancelled, it
// unparks.
// task move --to-stream: a task whose paths a sibling still holds is
// refused when it would carry them to another stream, and moves when it
// holds them alone (its own stream's record is read without it); --join on
// a task push takes the overlapping stream.
func TestPathsGateUnparkAndMove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	push := func(id, stream, paths, join string) error {
		_, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: id, Stream: stream, Title: id, By: "test",
			Fields: []string{"paths", paths}, Join: join})
		return err
	}
	if err := push("up-1", "parked one", "unpark/a", ""); err != nil {
		t.Fatal(err)
	}
	if n, err := ws.ParkStream(ctx, client, "parked one", "test", "park"); err != nil || n != 1 {
		t.Fatalf("park: %d %v", n, err)
	}
	if err := push("up-2", "taker", "unpark/a/b.go", ""); err != nil {
		t.Fatalf("a parked stream holds no paths: %v", err)
	}
	_, err := ws.UnparkStream(ctx, client, "parked one", "test", "unpark")
	var r *ws.Refused
	no, ok := (*ws.PathsRefusal)(nil), false
	if errors.As(err, &r) {
		no, ok = ws.ParseRefusal(r.Why)
	}
	if !ok || no.Stream != "taker" || ws.JoinPaths(no.Paths) != "unpark/a,unpark/a/b.go" {
		t.Fatalf("unpark over taker: %v; want the PATHS overlap refusal naming taker", err)
	}
	if w := client.HGet(ctx, "task:up-1", "where").Val(); w != "parked" {
		t.Fatalf("up-1 is %s after the refused unpark; want parked", w)
	}
	if _, err := taskcard.Cancel(ctx, client, "up-2", "test", "free the paths"); err != nil {
		t.Fatal(err)
	}
	if n, err := ws.UnparkStream(ctx, client, "parked one", "test", "unpark"); err != nil || n != 1 {
		t.Fatalf("unpark after taker's task was cancelled: %d %v", n, err)
	}

	// move --to-stream: mv-1 and mv-2 share mover's paths; mv-3 is elsewhere
	for _, c := range [][3]string{{"mv-1", "mover", "move/x"}, {"mv-2", "mover", "move/x/y.go"}, {"mv-3", "dest", "move/z"}} {
		if err := push(c[0], c[1], c[2], ""); err != nil {
			t.Fatalf("push %s: %v", c[0], err)
		}
	}
	_, err = taskcard.Move(ctx, client, "mv-1", "ready", taskcard.Opts{By: "test", Stream: "dest", SetStream: true})
	if why, _ := taskcard.IsRefused(err); why != "PATHS overlap paths=move/x,move/x/y.go stream=mover" {
		t.Fatalf("move mv-1 to dest while mv-2 holds move/x/y.go on mover: %v", err)
	}
	if s := client.HGet(ctx, "task:mv-1", "stream").Val(); s != "mover" {
		t.Fatalf("the refused move wrote stream %q", s)
	}
	if _, err := taskcard.Move(ctx, client, "mv-2", "ready", taskcard.Opts{By: "test", Stream: "dest", SetStream: true}); err == nil {
		t.Fatal("mv-2 moved to dest while mv-1 holds move/x on mover")
	}
	if _, err := taskcard.Move(ctx, client, "mv-3", "ready", taskcard.Opts{By: "test", Stream: "third", SetStream: true}); err != nil {
		t.Fatalf("mv-3 alone on dest moves to third: %v", err)
	}
	if err := push("mv-4", "elsewhere", "move/x/y.go", ""); err == nil {
		t.Fatal("mv-4 pushed onto elsewhere over mover's paths")
	}
	if err := push("mv-4", "elsewhere", "move/x/y.go", "mover"); err != nil {
		t.Fatalf("task push --join mover: %v", err)
	}
	if s := client.HGet(ctx, "task:mv-4", "stream").Val(); s != "mover" {
		t.Fatalf("joined task stream = %q; want mover", s)
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsGateJoinOpenOnly is probe 5 (#4322): --join names an open
// stream, one holding a live card. A card or task push joining a parked
// stream, a stream whose cards all landed, or an unknown one is refused
// (PATHS notopen, remedy nova-sprint stream ls) and writes nothing, even
// where its paths overlap nothing; joining the open stream is taken.
func TestPathsGateJoinOpenOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	task := func(id, stream, paths, join string) error {
		_, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: id, Stream: stream, Title: id, By: "test",
			Fields: []string{"paths", paths}, Join: join})
		return err
	}
	for _, c := range [][3]string{{"jo-p", "parked", "join/p"}, {"jo-l", "landed", "join/l"}, {"jo-o", "open", "join/o"}} {
		if err := task(c[0], c[1], c[2], ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ws.ParkStream(ctx, client, "parked", "test", "park"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, client, "jo-l", "working", taskcard.Opts{By: "test", As: "f"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, client, "jo-l", "landed", taskcard.Opts{By: "test", Sha: strings.Repeat("e", 40)}); err != nil {
		t.Fatal(err)
	}
	for _, join := range []string{"parked", "landed", "nowhere"} {
		want := `REFUSED PATHS notopen stream=` + join + ` remedy="nova-sprint stream ls" card=jc-` + join + "\n"
		res := card.PushWith(ctx, client, sprint, pathsCard(srv.URL, "jc-"+join, "mine", "join/"+join[:1]+"/x.go"), card.PushOptions{Join: join})
		if res.Code != 2 || res.Stdout != want {
			t.Fatalf("card --join %s: exit %d stdout %q; want %q", join, res.Code, res.Stdout, want)
		}
		assertAbsent(t, ctx, client, "jc-"+join)
		err := task("jt-"+join, "mine", "elsewhere/"+join, join)
		if why, _ := taskcard.IsRefused(err); why != "PATHS notopen stream="+join || client.Exists(ctx, "task:jt-"+join).Val() != 0 {
			t.Fatalf("task --join %s: %v", join, err)
		}
	}
	res := card.PushWith(ctx, client, sprint, pathsCard(srv.URL, "jc-open", "mine", "join/o/x.go"), card.PushOptions{Join: "open"})
	if res.Code != 0 || client.HGet(ctx, keyCard("jc-open"), "stream").Val() != "open" {
		t.Fatalf("card --join open: exit %d %q %q", res.Code, res.Stdout, res.Stderr)
	}
	assertDisjoint(t, ctx, client)
}

// TestPathsGateSameStreamShares is probe 6 (#4322): the rule is between
// streams. Two cards and a task of the SAME stream naming one path (equal,
// and a file under the other's dir) are all accepted, and the stream's
// record holds the union.
func TestPathsGateSameStreamShares(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, c := range [][2]string{{"ss-1", "share/a"}, {"ss-2", "share/a"}} {
		if res := card.Push(ctx, client, sprint, pathsCard(srv.URL, c[0], "one", c[1])); res.Code != 0 {
			t.Fatalf("push %s: exit %d %q %q", c[0], res.Code, res.Stdout, res.Stderr)
		}
	}
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "ss-3", Stream: "one", Title: "t", By: "test",
		Fields: []string{"paths", "share/a/b.go"}}); err != nil {
		t.Fatalf("task on the same stream: %v", err)
	}
	if got := client.HGet(ctx, ws.PathsKey, "one").Val(); got != "share/a,share/a/b.go" {
		t.Fatalf("ws:paths one = %q", got)
	}
	assertDisjoint(t, ctx, client)
}
