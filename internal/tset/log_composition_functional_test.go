//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// These gates exercise only the composed Redis profile. The L1 Mem model has
// no log, history list, or page cursor and is not a substitute for this source.
func composedHistoryFixture(t *testing.T) (*tsetFixture, *RedisStore) {
	t.Helper()
	fx := newComposedTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.Activate(t)
	store := NewRedis(fx.Client)
	// Epoch zero's initial row must be replayable from its own log.
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space,
		Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}})
	return fx, store
}

func composedWrite(t *testing.T, store *RedisStore, step Step) Reply {
	t.Helper()
	reply, err := store.Step(context.Background(), step)
	if err != nil || reply.Status != "ok" || reply.Replay {
		t.Fatalf("composed step: reply=%+v err=%v", reply, err)
	}
	return reply
}

func composedNote(about string) Note {
	return Note{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{}`)}, About: []string{about}}
}

func composedNamedNotes(space string, epoch Decimal, op string, notes ...Note) Step {
	intent := "composed-log/" + op
	return Step{Epoch: epoch, Space: space, Op: &op, Intent: &intent,
		Entries: []Entry{}, Notes: notes}
}

func composedPage(t *testing.T, store *RedisStore, plan ReadPlan) ReadReply {
	t.Helper()
	reply, err := store.Read(context.Background(), plan)
	if err != nil || reply.Status != "page" {
		t.Fatalf("composed page: reply=%+v err=%v", reply, err)
	}
	return reply
}

func composedCursor(t *testing.T, raw json.RawMessage) CardCursor {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		t.Fatal("nonexhausted cardlines page omitted next cursor")
	}
	var cursor CardCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatalf("decode next cursor %s: %v", raw, err)
	}
	return cursor
}

func composedCursorRefusal(t *testing.T, fx *tsetFixture, plan ReadPlan) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	raw := readLuaRaw(t, fx, plan)
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	var refused Refusal
	if err := json.Unmarshal(raw, &refused); err != nil ||
		refused.Status != "refused" || refused.Code != "CURSOR" {
		t.Fatalf("raw=%s err=%v, want CURSOR", raw, err)
	}
	if _, leaked := response["answers"]; leaked {
		t.Fatalf("CURSOR refusal leaked answers: %s", raw)
	}
	if _, leaked := response["items"]; leaked {
		t.Fatalf("CURSOR refusal leaked items: %s", raw)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("CURSOR refusal changed the complete Redis TYPE/DUMP image")
	}
}

func TestComposedRefuseLOGID(t *testing.T) {
	t.Parallel()
	fx, store := composedHistoryFixture(t)
	seed := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"},
		About: []string{"p"}}}}
	if reply := composedWrite(t, store, seed); reply.FirstSeq != "2" || reply.LastSeq != "2" {
		t.Fatalf("seed log sequence=%s..%s, want 2 after public rows step", reply.FirstSeq, reply.LastSeq)
	}
	ctx := context.Background()
	logKey := fixtureLogKey(fx.Space, "0")
	autoID, err := fx.Client.XAdd(ctx, &redis.XAddArgs{Stream: logKey, ID: "*",
		Values: map[string]any{"n": "0", "d": `{"k":"n","ms":"1","about":["p"]}`}}).Result()
	if err != nil || autoID == "3-0" {
		t.Fatalf("plant auto stream ID: id=%q err=%v", autoID, err)
	}
	metadata, err := fx.Client.XInfoStream(ctx, logKey).Result()
	if err != nil || metadata.EntriesAdded != 3 || metadata.LastGeneratedID != autoID {
		t.Fatalf("corrupt stream metadata: %+v err=%v, want entries-added=3 last=%s",
			metadata, err, autoID)
	}
	before := commitProbeImage(t, fx.Client)
	write := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:c", IDs: []string{"must-not-exist"}, Scores: []string{"2"},
		About: []string{"must-not-exist"}}}}
	reply, err := store.Step(ctx, write)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "LOGID" || reply.Status != "" {
		t.Fatalf("corrupt log write: reply=%+v err=%v, want LOGID", reply, err)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("LOGID write refusal changed the complete Redis TYPE/DUMP image")
	}
	if n, err := fx.Client.Exists(ctx, fixtureRecordKey(fx.Space, "work", "must-not-exist")).Result(); err != nil || n != 0 {
		t.Fatalf("refused member exists=%d err=%v", n, err)
	}
	read, err := store.Read(ctx, ReadPlan{Epoch: "0", Space: fx.Space, Mode: "atomic",
		Queries: []ReadQuery{{Kind: "lines", AfterSeq: "0", Limit: 10}}})
	if !errors.As(err, &refusal) || refusal.Code != "LOGID" || len(read.Answers) != 0 {
		t.Fatalf("corrupt log read: reply=%+v err=%v, want empty LOGID", read, err)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("LOGID read refusal changed the complete Redis TYPE/DUMP image")
	}
}

func TestComposedRefuseCURSOR(t *testing.T) {
	t.Parallel()
	fx, store := composedHistoryFixture(t)
	composedWrite(t, store, composedNamedNotes(fx.Space, "0", "cursor-notes",
		composedNote("p"), composedNote("p"), composedNote("q")))
	query := ReadQuery{Kind: "cardlines", Abouts: []string{"p", "q"},
		Fields: []string{}, Limit: 1}
	first := composedPage(t, store, ReadPlan{Epoch: "0", Space: fx.Space,
		Mode: "page", Queries: []ReadQuery{query}})
	if first.Exhausted {
		t.Fatal("one-line first page unexpectedly exhausted three history lines")
	}
	good := composedCursor(t, first.Next)
	if len(good.Positions) != 2 {
		t.Fatalf("cursor positions=%+v, want two primary slots", good.Positions)
	}
	clone := func() CardCursor {
		c := good
		c.Fields = append([]string{}, good.Fields...)
		c.Positions = append([]CardCursorPosition(nil), good.Positions...)
		return c
	}
	cases := []struct {
		name   string
		change func(*ReadPlan)
	}{
		{"wrong epoch", func(p *ReadPlan) { c := clone(); c.Epoch = "1"; p.Queries[0].Cursor = &c }},
		{"projection mismatch", func(p *ReadPlan) { c := clone(); p.Queries[0].Fields = []string{"brief"}; p.Queries[0].Cursor = &c }},
		{"metadata mismatch", func(p *ReadPlan) { c := clone(); p.Queries[0].IncludeMeta = true; p.Queries[0].Cursor = &c }},
		{"about order mismatch", func(p *ReadPlan) { c := clone(); p.Queries[0].Abouts = []string{"q", "p"}; p.Queries[0].Cursor = &c }},
		{"duplicate position", func(p *ReadPlan) { c := clone(); c.Positions[1].About = "p"; p.Queries[0].Cursor = &c }},
		{"future position", func(p *ReadPlan) {
			c := clone()
			c.Positions[0].NextIndex = c.Positions[0].ThroughIndex + 2
			p.Queries[0].Cursor = &c
		}},
		{"future high-water", func(p *ReadPlan) { c := clone(); c.Positions[0].ThroughIndex += 100; p.Queries[0].Cursor = &c }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "page", Queries: []ReadQuery{query}}
			tc.change(&plan)
			composedCursorRefusal(t, fx, plan)
		})
	}
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space,
		Entries: []Entry{{Kind: "advance", AdvanceFrom: "0"}}})
	stale := ReadPlan{Epoch: "1", Space: fx.Space, Mode: "page", Queries: []ReadQuery{query}}
	c := clone()
	stale.Queries[0].Cursor = &c
	composedCursorRefusal(t, fx, stale)
}

func TestComposedHistoryCursorCoverage(t *testing.T) {
	t.Parallel()
	fx, store := composedHistoryFixture(t)
	create := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:c", IDs: []string{"p", "q"}, Scores: []string{"1", "2"},
		About: []string{"p", "q"}, Each: []map[string]string{{"brief": "alpha"}, {"brief": "beta"}}}}}
	if reply := composedWrite(t, store, create); reply.FirstSeq != "2" || reply.LastSeq != "2" {
		t.Fatalf("shared create sequence=%s..%s, want 2 after public rows step", reply.FirstSeq, reply.LastSeq)
	}
	if reply := composedWrite(t, store, composedNamedNotes(fx.Space, "0", "initial-notes",
		composedNote("p"), composedNote("q"), composedNote("p"))); reply.FirstSeq != "3" || reply.LastSeq != "5" {
		t.Fatalf("initial note sequence=%s..%s, want 3..5", reply.FirstSeq, reply.LastSeq)
	}
	ctx := context.Background()
	for about, want := range map[string][]string{"p": {"2", "3", "5"}, "q": {"2", "4"}} {
		got, err := fx.Client.LRange(ctx, fixtureHistoryKey(fx.Space, "0", about), 0, -1).Result()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("history %s=%v err=%v, want %v", about, got, err, want)
		}
	}
	query := ReadQuery{Kind: "cardlines", Abouts: []string{"p", "q"},
		Fields: []string{"brief"}, Limit: 2}
	plan := ReadPlan{Epoch: "0", Space: fx.Space, Mode: "page", Queries: []ReadQuery{query}}
	page := composedPage(t, store, plan)
	if len(page.Through) == 0 || string(page.Through) == "null" || page.Exhausted {
		t.Fatalf("first page omitted frozen high-water or exhausted early: %+v", page)
	}
	firstCursor := composedCursor(t, page.Next)
	if firstCursor.Epoch != "0" || !reflect.DeepEqual(firstCursor.Fields, query.Fields) ||
		firstCursor.IncludeMeta || len(firstCursor.Positions) != 2 ||
		firstCursor.Positions[0].About != "p" || firstCursor.Positions[0].NextIndex != 2 ||
		firstCursor.Positions[0].ThroughIndex != 2 || firstCursor.Positions[1].About != "q" ||
		firstCursor.Positions[1].NextIndex != 0 || firstCursor.Positions[1].ThroughIndex != 1 {
		t.Fatalf("first page did not freeze both high-waters: %+v", firstCursor)
	}
	if reply := composedWrite(t, store, composedNamedNotes(fx.Space, "0", "later-notes",
		composedNote("p"), composedNote("q"))); reply.FirstSeq != "6" || reply.LastSeq != "7" {
		t.Fatalf("later append sequence=%s..%s, want 6..7", reply.FirstSeq, reply.LastSeq)
	}
	advance := composedNamedNotes(fx.Space, "0", "advance-note", composedNote("p"))
	advance.Entries = []Entry{{Kind: "advance", AdvanceFrom: "0"}}
	if reply := composedWrite(t, store, advance); reply.EpochAfter != "1" ||
		reply.FirstSeq != "1" || reply.LastSeq != "2" {
		t.Fatalf("advance/new-epoch log: %+v", reply)
	}
	seen := map[string][]string{"p": {}, "q": {}}
	for turn := 0; turn < 8; turn++ {
		wantActive := Decimal("1")
		if turn == 0 {
			wantActive = "0"
		}
		if page.Epoch != "0" || page.ActiveEpoch != wantActive || len(page.Items) != 2 {
			t.Fatalf("historical page envelope/items: %+v", page)
		}
		for i, raw := range page.Items {
			var slot struct {
				About string            `json:"about"`
				Lines []json.RawMessage `json:"lines"`
			}
			if err := json.Unmarshal(raw, &slot); err != nil || slot.About != query.Abouts[i] {
				t.Fatalf("page slot %d=%s err=%v", i, raw, err)
			}
			for _, line := range slot.Lines {
				var event struct {
					Seq  Decimal `json:"seq"`
					Kind string  `json:"kind"`
				}
				if err := json.Unmarshal(line, &event); err != nil || event.Seq == "" || event.Kind == "" {
					t.Fatalf("projected line %s: %+v err=%v", line, event, err)
				}
				if event.Seq == "6" || event.Seq == "7" {
					t.Fatalf("post-high-water event leaked into fixed page chain: %s", line)
				}
				if slot.About == "p" && strings.Contains(string(line), `"beta"`) ||
					slot.About == "q" && strings.Contains(string(line), `"alpha"`) {
					t.Fatalf("foreign primary field leaked into %s: %s", slot.About, line)
				}
				seen[slot.About] = append(seen[slot.About], string(event.Seq))
			}
		}
		if page.Exhausted {
			break
		}
		cursor := composedCursor(t, page.Next)
		if len(cursor.Positions) != 2 || cursor.Positions[0].ThroughIndex != 2 ||
			cursor.Positions[1].ThroughIndex != 1 {
			t.Fatalf("page high-water drifted: %+v", cursor)
		}
		query.Cursor = &cursor
		plan.Queries = []ReadQuery{query}
		page = composedPage(t, store, plan)
	}
	if !page.Exhausted || !reflect.DeepEqual(seen["p"], []string{"2", "3", "5"}) ||
		!reflect.DeepEqual(seen["q"], []string{"2", "4"}) {
		t.Fatalf("historical page chain incomplete or duplicated: exhausted=%v seen=%v", page.Exhausted, seen)
	}
	current := composedPage(t, store, ReadPlan{Epoch: "1", Space: fx.Space,
		Mode: "page", Queries: []ReadQuery{{Kind: "cardlines", Abouts: []string{"p"},
			Fields: []string{}, Limit: 2}}})
	if current.Epoch != "1" || current.ActiveEpoch != "1" ||
		len(current.Items) != 1 || !current.Exhausted ||
		!strings.Contains(string(current.Items[0]), `"seq":"2"`) ||
		strings.Contains(string(current.Items[0]), `"seq":"1"`) {
		t.Fatalf("current epoch primary history crossed epoch boundary: %+v", current)
	}
}
