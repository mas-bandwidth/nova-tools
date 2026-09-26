//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// lifeRedis is a throwaway Redis with the nova_sprint library loaded and
// ada, bo registered (no real host is reached).
func lifeRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ada", "bo"} {
		if err := client.SAdd(ctx, "friends", name).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.HSet(ctx, "friend:"+name+":desired", "slots", "4", "machine", "studio").Err(); err != nil {
			t.Fatal(err)
		}
	}
	return addr, client
}

func lifeRun(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestLifeEventPrintsId: `life event` prints one EVENT line whose id names
// the appended entry.
func TestLifeEventPrintsId(t *testing.T) {
	addr, client := lifeRedis(t)
	t.Setenv(seatEnv, "ada")
	code, out, errOut := lifeRun("life", "event", "--redis", addr, "--as", "ada", "--kind", "deliver")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := regexp.MustCompile(`^EVENT friend=ada kind=deliver id=(\d+-\d+)\n$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("stdout %q", out)
	}
	entry := client.XRange(context.Background(), life.EventsKey("ada"), m[1], m[1]).Val()
	if len(entry) != 1 || entry[0].Values["kind"] != "deliver" {
		t.Fatalf("entry %v", entry)
	}
	code, out, _ = lifeRun("life", "event", "--redis", addr, "--as", "ada", "--kind", "turn-start", "--cause", m[1])
	if code != 0 || !regexp.MustCompile(`^EVENT friend=ada kind=turn-start id=\d+-\d+ cause=`+regexp.QuoteMeta(m[1])+`\n$`).MatchString(out) {
		t.Fatalf("turn-start: exit %d %q", code, out)
	}
}

// TestLifeVerbRefusals: every refusal line and exit code of the two verbs,
// byte for byte, with nothing appended.
func TestLifeVerbRefusals(t *testing.T) {
	addr, client := lifeRedis(t)
	ctx := context.Background()
	rows := []struct {
		seat string
		args []string
		want string
	}{
		{"ada", []string{"life", "event", "--kind", "beat"}, "nova-sprint life event: --as is required; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"", []string{"life", "event", "--as", "ada", "--kind", "beat"}, "nova-sprint life event: want NOVA_FRIEND; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"bo", []string{"life", "event", "--as", "ada", "--kind", "beat"}, "nova-sprint life event: want --as equal to NOVA_FRIEND (NOVA_FRIEND=bo, --as ada); usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"bo", []string{"life", "event", "--as", "ada", "--kind", "deliver"}, "nova-sprint life event: want --as equal to NOVA_FRIEND (NOVA_FRIEND=bo, --as ada); usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"ada", []string{"life", "event", "--as", "ada"}, "nova-sprint life event: --kind is required; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"ada", []string{"life", "event", "--as", "ada", "--kind", "sleeping"}, "nova-sprint life event: --kind sleeping is not one of beat, deliver, turn-start, turn-end, turn-error, usage-limit; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"ada", []string{"life", "event", "--as", "ada", "--kind", "beat", "--cause", "1-0"}, "nova-sprint life event: --cause is only for --kind turn-start; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"zed", []string{"life", "event", "--as", "zed", "--kind", "beat"}, "nova-sprint life event: INVALID unknown friend zed; usage: nova-sprint life event --as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]\n"},
		{"ada", []string{"life", "wake-mode", "--as", "ada"}, "nova-sprint life wake-mode: --set is required; usage: nova-sprint life wake-mode --as <f> --set scheduled-model-turn\n"},
		{"ada", []string{"life", "wake-mode", "--set", "scheduled-model-turn"}, "nova-sprint life wake-mode: --as is required; usage: nova-sprint life wake-mode --as <f> --set scheduled-model-turn\n"},
		{"ada", []string{"life", "wake-mode", "--as", "ada", "--set", "active"}, "nova-sprint life wake-mode: --set active is not scheduled-model-turn; usage: nova-sprint life wake-mode --as <f> --set scheduled-model-turn\n"},
	}
	for _, r := range rows {
		t.Setenv(seatEnv, r.seat)
		args := append(append([]string{}, r.args...), "--redis", addr)
		code, out, errOut := lifeRun(args...)
		if code != 2 || out != "" || errOut != r.want {
			t.Fatalf("%v (NOVA_FRIEND=%q): exit %d stdout %q stderr %q, want exit 2 and %q", r.args, r.seat, code, out, errOut, r.want)
		}
	}
	for _, f := range []string{"ada", "zed"} {
		if n := client.XLen(ctx, life.EventsKey(f)).Val(); n != 0 {
			t.Fatalf("a refused event appended %d entries to %s", n, f)
		}
	}
	if client.Exists(ctx, life.WakeModeKey("ada")).Val() != 0 {
		t.Fatal("a refused wake-mode wrote")
	}
}

// TestLifeWakeModeLines: a turn 60 s after its delivery declares
// scheduled-model-turn; one at 121 s is REFUSED with exit 1 and no write.
func TestLifeWakeModeLines(t *testing.T) {
	t.Parallel()

	addr, client := lifeRedis(t)
	ctx := context.Background()
	st := store.New(client)
	now := time.Now()
	appendEv := func(f, kind, cause string, at time.Time) string {
		id, err := life.AppendEvent(ctx, st, f, kind, cause, at, f)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	d := appendEv("ada", life.EventDeliver, "", now.Add(-90*time.Second))
	tt := appendEv("ada", life.EventTurnStart, d, now.Add(-30*time.Second))
	code, out, errOut := lifeRun("life", "wake-mode", "--redis", addr, "--as", "ada", "--set", "scheduled-model-turn")
	want := fmt.Sprintf("WAKE-MODE friend=ada mode=scheduled-model-turn deliver=%s turn=%s lag_ms=60000\n", d, tt)
	if code != 0 || out != want {
		t.Fatalf("exit %d stdout %q stderr %q, want %q", code, out, errOut, want)
	}
	if got := client.HGet(ctx, life.WakeModeKey("ada"), "lag_ms").Val(); got != "60000" {
		t.Fatalf("stored lag %q", got)
	}

	d = appendEv("bo", life.EventDeliver, "", now.Add(-200*time.Second))
	appendEv("bo", life.EventTurnStart, d, now.Add(-79*time.Second))
	code, out, errOut = lifeRun("life", "wake-mode", "--redis", addr, "--as", "bo", "--set", "scheduled-model-turn")
	refused := "nova-sprint life wake-mode: REFUSED friend=bo no firing receipt: no deliver followed by a turn-start with its cause within 120s in the last 20 min\n"
	if code != 1 || out != "" || errOut != refused {
		t.Fatalf("121 s: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if client.Exists(ctx, life.WakeModeKey("bo")).Val() != 0 {
		t.Fatal("a refused declaration wrote")
	}
}

// TestPresenceLoopAppendsBeatEvent: one production presence cycle appends
// exactly one beat event at the cycle clock after the process beat and
// before the wake poll and the take; a failed append ends the loop with
// exit 3 and polls, takes and retries nothing.
func TestPresenceLoopAppendsBeatEvent(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*store.Store, *redis.Client, life.Presence) {
		_, client := lifeRedis(t)
		if err := client.HSet(context.Background(), "friend:ada:beat", "session", "s1", "host", "studio").Err(); err != nil {
			t.Fatal(err)
		}
		return store.New(client), client, life.Presence{Friend: "ada", Host: "studio", Session: "s1"}
	}
	t.Run("appends_after_beat", func(t *testing.T) {
		st, client, p := setup(t)
		ctx := context.Background()
		clock := time.UnixMilli(1790000000123)
		steps := livePresenceSteps(st, p, "no-sprint")
		steps.now = func() time.Time { return clock }
		var order []string
		seen := func(step string) {
			n := client.XLen(ctx, life.EventsKey("ada")).Val()
			order = append(order, step+":"+strconv.FormatInt(n, 10))
		}
		beat, poll, take := steps.beat, steps.poll, steps.take
		steps.beat = func(ctx context.Context) error { seen("beat"); return beat(ctx) }
		steps.poll = func(ctx context.Context) error { seen("poll"); return poll(ctx) }
		steps.take = func(ctx context.Context) ([]task.Claim, error) { seen("take"); return take(ctx) }
		var out, errOut bytes.Buffer
		code, done := presenceCycle(ctx, "ada", steps, &out, &errOut)
		if code != 0 || done || errOut.Len() != 0 {
			t.Fatalf("cycle: code %d done %v stderr %q", code, done, errOut.String())
		}
		if got := fmt.Sprint(order); got != "[beat:0 poll:1 take:1]" {
			t.Fatalf("order %s, want the beat, then one event, then poll and take", got)
		}
		evs := client.XRange(ctx, life.EventsKey("ada"), "-", "+").Val()
		if len(evs) != 1 || evs[0].Values["kind"] != "beat" || evs[0].Values["at"] != "1790000000123" {
			t.Fatalf("events %v, want one beat at the cycle clock", evs)
		}
	})
	t.Run("event_error_exits", func(t *testing.T) {
		st, client, p := setup(t)
		ctx := context.Background()
		steps := livePresenceSteps(st, p, "no-sprint")
		appends, later := 0, 0
		steps.event = func(context.Context, time.Time) error { appends++; return errors.New("store gone") }
		steps.poll = func(context.Context) error { later++; return nil }
		steps.take = func(context.Context) ([]task.Claim, error) {
			later++
			return []task.Claim{{Sprint: "s", ID: "x"}}, nil
		}
		var out, errOut bytes.Buffer
		code, done := presenceCycle(ctx, "ada", steps, &out, &errOut)
		if code != 3 || !done {
			t.Fatalf("code %d done %v, want 3 and the loop ended", code, done)
		}
		if errOut.String() != "friend ada beat event: store gone\n" || out.Len() != 0 {
			t.Fatalf("stderr %q stdout %q", errOut.String(), out.String())
		}
		if appends != 1 || later != 0 {
			t.Fatalf("appends %d, poll/take %d; want one attempt and no poll or take", appends, later)
		}
		if client.XLen(ctx, life.EventsKey("ada")).Val() != 0 {
			t.Fatal("an event was appended")
		}
	})
}
