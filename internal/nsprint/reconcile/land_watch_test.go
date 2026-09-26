package reconcile_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestLandWatchAlarmsMergeCardAndEscalation (nova-tools #4324): on an
// injected clock, one stream with two merging members gets merging_at
// stamped and ONE merge card (members in the ws score order, to the
// frontier friend) on the first pass; nothing at five minutes; LAND-SLOW
// every pass past ten minutes with ONE wake note for the episode; LAND-WALL
// and stalled=1 past thirty; a merge card closed cross-stream cuts one
// escalation to the coordinator and no new merge card until it closes; when
// merging empties both records go.
func TestLandWatchAlarmsMergeCardAndEscalation(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	t0 := time.UnixMilli(1700000000000)
	now := t0
	const s = "swarm: cards"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 20, Member: "t2"}, redis.Z{Score: 10, Member: "t1"})
	c.HSet(ctx, "task:t2", "pr", "nova-tools#2", "paths", "b c")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "sp"})
	c.RPush(ctx, "ws:"+s+":notes", "MERGE-NOTE by=rowan at=1 the moves API changed")
	var pushed []reconcile.MergeCard
	var wakes []reconcile.LandWake
	var out bytes.Buffer
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Out: &out, Repo: "mas-bandwidth/nova-tools",
		Push: func(_ context.Context, m reconcile.MergeCard) (string, error) {
			pushed = append(pushed, m)
			c.HSet(ctx, "task:"+m.ID, "state", "open", "kind", m.Kind)
			return m.ID, nil
		},
		Notify:      func(_ context.Context, wk reconcile.LandWake) error { wakes = append(wakes, wk); return nil },
		Coordinator: func(context.Context) (string, error) { return "rowan", nil },
		Frontier:    func(context.Context) (string, error) { return "stella", nil },
	}
	pass := func(at time.Duration) string {
		t.Helper()
		now = t0.Add(at)
		out.Reset()
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("pass at %s: %v", at, err)
		}
		return out.String()
	}

	// Pass 1: merging_at stamped, one merge card, no alarm.
	o := pass(0)
	if at, _ := c.HGet(ctx, reconcile.LandMergingKey(s), "t1").Result(); at != "1700000000000" {
		t.Fatalf("first seen %q", at)
	}
	if n, _ := c.Exists(ctx, "task:t1").Result(); n != 0 {
		// One writer of the task record (#3778): the watch never touches it.
		t.Fatal("the watch wrote task:t1")
	}
	if len(pushed) != 1 || pushed[0].ID != "merge-swarm-cards-1" || pushed[0].Kind != "merge" || pushed[0].To != "stella" ||
		len(pushed[0].Members) != 2 || pushed[0].Members[0].Task != "t1" || pushed[0].Members[1].Task != "t2" || pushed[0].Paths != "b c" {
		t.Fatalf("merge card: %+v", pushed)
	}
	if !strings.Contains(o, "MERGE-CARD ") || strings.Contains(o, "LAND-SLOW") || len(wakes) != 0 {
		t.Fatalf("pass 1 out:\n%s wakes=%d", o, len(wakes))
	}
	if v, _ := c.HGet(ctx, reconcile.LandMergeKey(s), "task").Result(); v != "merge-swarm-cards-1" {
		t.Fatalf("land:merge task %q", v)
	}
	body, _ := c.Get(ctx, reconcile.LandBriefKey("merge-swarm-cards-1")).Result()
	for _, want := range []string{"1. t1 pr=no-pr order=10", "2. t2 pr=nova-tools#2 order=20", "--dry-run", "MERGE-NOTE by=rowan at=1 the moves API changed", "BLOCKED cross-stream"} {
		if !strings.Contains(body, want) {
			t.Fatalf("brief lacks %q:\n%s", want, body)
		}
	}
	title := reconcile.MergeTitle(pushed[0])
	if !strings.Contains(title, "STREAM: swarm: cards | land stream swarm: cards: 2 members in work order into dev as one PR | PATHS: b c | BASE: dev | DONE-WHEN: nova-sprint land") {
		t.Fatalf("title %q", title)
	}

	// Pass 2 at five minutes: the same card, no second one, no alarm. The
	// move's own merging_at on t2 (two minutes in) is what the brief shows.
	c.HSet(ctx, "task:t2", "merging_at", "1700000120000")
	if o := pass(5 * time.Minute); len(pushed) != 1 || strings.Contains(o, "LAND-SLOW") {
		t.Fatalf("pass 2: pushed=%d out=%s", len(pushed), o)
	}
	if body, _ := c.Get(ctx, reconcile.LandBriefKey("merge-swarm-cards-1")).Result(); !strings.Contains(body, "2. t2 pr=nova-tools#2 order=20 merging_for=3m0s") {
		t.Fatalf("brief after the move's stamp:\n%s", body)
	}
	// Pass 3 at eleven: LAND-SLOW, one note; pass 4 at twelve: the line
	// again, still one note.
	o = pass(11 * time.Minute)
	if !strings.Contains(o, "LAND-SLOW swarm:\\x20cards oldest=t1 age=11m0s max=10m0s\n") || len(wakes) != 1 || wakes[0].Oldest != "t1" || wakes[0].Wall {
		t.Fatalf("pass 3:\n%s wakes=%+v", o, wakes)
	}
	if rec, _ := c.HGetAll(ctx, reconcile.LandSlowKey(s)).Result(); rec["oldest"] != "t1" || rec["stalled"] != "0" || rec["noted"] != "t1@1700000000000" {
		t.Fatalf("land:slow %v", rec)
	}
	// Pass 4 at twelve: nothing changed, so nothing prints; the record
	// moved; still one note.
	if o := pass(12 * time.Minute); o != "" || len(wakes) != 1 {
		t.Fatalf("pass 4:\n%s wakes=%d", o, len(wakes))
	}
	if v, _ := c.HGet(ctx, reconcile.LandSlowKey(s), "age_ms").Result(); v != "720000" {
		t.Fatalf("age_ms %q", v)
	}
	// Pass 5 at thirty-one: LAND-WALL prints (the word changed), stalled,
	// and the wall's own note: two wakes for the episode.
	o = pass(31 * time.Minute)
	if !strings.Contains(o, "LAND-WALL swarm:\\x20cards oldest=t1 age=31m0s max=30m0s\n") || len(wakes) != 2 || !wakes[1].Wall || wakes[1].Max != 30*time.Minute {
		t.Fatalf("pass 5:\n%s wakes=%+v", o, wakes)
	}
	if v, _ := c.HGet(ctx, reconcile.LandSlowKey(s), "stalled").Result(); v != "1" {
		t.Fatalf("stalled %q", v)
	}
	if o := pass(32*time.Minute + 30*time.Second); o != "" || len(wakes) != 2 {
		t.Fatalf("pass 5b (no change):\n%s wakes=%d", o, len(wakes))
	}
	// The merge card ends cross-stream: one escalation to the coordinator,
	// once; no new merge card while it is open; one when it closes.
	c.HSet(ctx, "task:merge-swarm-cards-1", "state", "closed", "reason", "BLOCKED cross-stream paths=internal/y.go")
	o = pass(33 * time.Minute)
	if len(pushed) != 2 || pushed[1].ID != "cross-swarm-cards-1" || pushed[1].Kind != "work" || pushed[1].To != "rowan" || !strings.Contains(pushed[1].Reason, "cross-stream") {
		t.Fatalf("escalation: %+v", pushed)
	}
	if !strings.Contains(o, "LAND-CROSS swarm:\\x20cards card=merge-swarm-cards-1 escalation=cross-swarm-cards-1 to=rowan") {
		t.Fatalf("pass 6:\n%s", o)
	}
	if !strings.Contains(reconcile.MergeTitle(pushed[1]), "cross-stream landing of swarm: cards: BLOCKED cross-stream paths=internal/y.go") {
		t.Fatalf("escalation title %q", reconcile.MergeTitle(pushed[1]))
	}
	if pass(34 * time.Minute); len(pushed) != 2 {
		t.Fatalf("escalated twice or a new merge card: %+v", pushed)
	}
	c.HSet(ctx, "task:cross-swarm-cards-1", "state", "closed")
	if pass(35 * time.Minute); len(pushed) != 3 || pushed[2].ID != "merge-swarm-cards-2" || pushed[2].Kind != "merge" {
		t.Fatalf("after the escalation closed: %+v", pushed)
	}
	// The re-cut dropped the closed card's brief and wrote the new one.
	if n, _ := c.Exists(ctx, reconcile.LandBriefKey("merge-swarm-cards-1")).Result(); n != 0 {
		t.Fatal("the closed card's brief is still there")
	}
	if n, _ := c.Exists(ctx, reconcile.LandBriefKey("merge-swarm-cards-2")).Result(); n != 1 {
		t.Fatal("no brief for the new card")
	}
	// The landing moved the members: the card fields, the brief, the slow
	// and first-seen records go; seq stays so the next episode's id is new.
	c.ZRem(ctx, "ws:"+s+":merging", "t1", "t2")
	if o := pass(36 * time.Minute); o != "" || len(pushed) != 3 {
		t.Fatalf("empty stream: %q pushed=%d", o, len(pushed))
	}
	if n, _ := c.Exists(ctx, reconcile.LandSlowKey(s), reconcile.LandMergingKey(s), reconcile.LandBriefKey("merge-swarm-cards-2")).Result(); n != 0 {
		t.Fatalf("records left: %d", n)
	}
	if rec, _ := c.HGetAll(ctx, reconcile.LandMergeKey(s)).Result(); len(rec) != 1 || rec["seq"] != "2" {
		t.Fatalf("land:merge after the episode: %v", rec)
	}
	// A new episode: the next id, never merge-swarm-cards-1 again.
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 30, Member: "t3"})
	if o := pass(37 * time.Minute); len(pushed) != 4 || pushed[3].ID != "merge-swarm-cards-3" || !strings.Contains(o, "MERGE-CARD ") {
		t.Fatalf("second episode: %+v\n%s", pushed, o)
	}
}

// TestLandWatchNoteGoesToTheOutbox: the default note is two friend:outbox
// notices, the declared channel (cfg:land notify, default bus:To:glenn) and
// the coordinator's bus channel, carrying the LAND-SLOW line; a second pass
// in the same episode adds none.
func TestLandWatchNoteGoesToTheOutbox(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	t0 := time.UnixMilli(1700000000000)
	now := t0
	const s = "quack"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 1, Member: "q1"})
	c.HSet(ctx, "cfg:land", "slow", "120", "wall", "300")
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: "o/r",
		Push:        func(_ context.Context, m reconcile.MergeCard) (string, error) { return m.ID, nil },
		Coordinator: func(context.Context) (string, error) { return "rowan", nil },
		Frontier:    func(context.Context) (string, error) { return "rowan", nil },
	}
	for _, at := range []time.Duration{0, 3 * time.Minute, 4 * time.Minute} {
		now = t0.Add(at)
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("pass at %s: %v", at, err)
		}
	}
	msgs, err := c.XRange(ctx, "friend:outbox", "-", "+").Result()
	if err != nil || len(msgs) != 2 {
		t.Fatalf("outbox: %v %v", msgs, err)
	}
	channels := map[string]bool{}
	for _, m := range msgs {
		channels[m.Values["channel"].(string)] = true
		if m.Values["kind"] != "notice" || m.Values["friend"] != "rowan" || !strings.HasPrefix(m.Values["detail"].(string), "LAND-SLOW quack oldest=q1 age=3m0s max=2m0s") {
			t.Fatalf("notice %v", m.Values)
		}
	}
	if !channels["bus:To:glenn"] || !channels["bus:To:rowan"] {
		t.Fatalf("channels %v", channels)
	}
}
