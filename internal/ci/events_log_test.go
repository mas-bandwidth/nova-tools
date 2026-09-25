package ci

// events_log_test.go is the red-test contract of SPEC-LOGS.md Part 2 for the event
// bridge: every event nova-work events PUBLISHES on the bus is also EMITTED as one
// structured JSON line, so the fleet's Loki holds the same history the bus carried and
// nobody has to ssh to a bench to see what the bridge did.
//
// The promises, one test each: one line per published event and never two; the fixed
// fields SPEC-LOGS names, with the four labels the queries select on (source, verb,
// bench, event); a quiet poll that emits nothing, the same silence it publishes; no sink
// configured means no lines, so every test that predates this slice keeps its bytes; and
// a secret value that never reaches the writer whatever field carried it.
//
// No test here opens a socket: miniredis and the fake forge, as in events_test.go.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/redis/go-redis/v9"
)

// emitted parses the sink into one map per line, failing on anything that is not one JSON
// object per line: the whole point of the shape is that `| json` never guesses.
func emitted(t *testing.T, b *bytes.Buffer) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("an emitted line is not one JSON object: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// testProducer is a producer with the emitter wired to a buffer and a fixed clock and
// guid, so every assertion below is deterministic and nothing reads /proc or time.Now.
func testProducer(t *testing.T, rdb *redis.Client, forge Forge) (*Producer, *bytes.Buffer) {
	t.Helper()
	var sink bytes.Buffer
	p := NewProducer(rdb, forge, "events", nil)
	p.Events = &sink
	p.Bench = "hulk"
	p.Clock = func() time.Time { return time.Date(2026, 9, 18, 4, 5, 6, 0, time.UTC) }
	p.GUID = func() string { return "guid-under-test" }
	return p, &sink
}

// want checks one emitted line's fixed fields.
func wantFields(t *testing.T, m map[string]interface{}, event string) {
	t.Helper()
	for k, v := range map[string]string{
		"source": "nova-work",
		"verb":   "events",
		"bench":  "hulk",
		"event":  event,
		"level":  "INFO",
	} {
		if got, _ := m[k].(string); got != v {
			t.Errorf("%s = %q, want %q; line=%v", k, got, v, m)
		}
	}
	if ts, _ := m["ts"].(string); ts == "" {
		t.Errorf("the line carries no ts: %v", m)
	}
	if g, _ := m["guid"].(string); g != "guid-under-test" {
		t.Errorf("guid = %q, want the injected one; line=%v", g, m)
	}
	if _, ok := m["msg"]; !ok {
		t.Errorf("the line carries no msg: %v", m)
	}
}

// 1. A card republished on card-done is one emitted line carrying the card.
func TestEmitterWritesOneLinePerCardDone(t *testing.T) {
	_, rdb, ctx := newBus(t)
	p, sink := testProducer(t, rdb, &fakeForge{})
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	sink.Reset()
	if err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamCardsDone,
		Values: map[string]interface{}{"card": "card-9348", "label": "9348"},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := p.PublishCardsDone(ctx); err != nil || n != 1 {
		t.Fatalf("PublishCardsDone = %d, %v", n, err)
	}
	lines := emitted(t, sink)
	if len(lines) != 1 {
		t.Fatalf("one published card-done emitted %d lines, want 1: %s", len(lines), sink.String())
	}
	wantFields(t, lines[0], "card-done")
	if got, _ := lines[0]["card"].(string); got != "card-9348" {
		t.Errorf("card = %q, want card-9348", got)
	}
}

// 2. A completed check suite and a moved base are one line each, carrying the pr number
// and the sha in the message, and the pr field is 0 on the line that is not about a PR.
func TestEmitterWritesChecksDoneAndDevMoved(t *testing.T) {
	_, rdb, ctx := newBus(t)
	p, sink := testProducer(t, rdb, &fakeForge{snap: Snapshot{
		PRs:  []PRState{{Number: 42, Branch: "rowan/x", Head: "a1b2c3d", Conclusion: ConclusionSuccess}},
		Base: "0d739352",
	}})
	if n, err := p.PollOnce(ctx); err != nil || n != 2 {
		t.Fatalf("PollOnce = %d, %v; want 2 published", n, err)
	}
	lines := emitted(t, sink)
	if len(lines) != 2 {
		t.Fatalf("two published events emitted %d lines: %s", len(lines), sink.String())
	}
	wantFields(t, lines[0], "pr-checks-done")
	if pr, _ := lines[0]["pr"].(float64); int(pr) != 42 {
		t.Errorf("pr = %v, want 42; line=%v", lines[0]["pr"], lines[0])
	}
	if msg, _ := lines[0]["msg"].(string); !strings.Contains(msg, ConclusionSuccess) {
		t.Errorf("the pr-checks-done msg does not name the conclusion: %q", msg)
	}
	wantFields(t, lines[1], "dev-moved")
	if pr, _ := lines[1]["pr"].(float64); int(pr) != 0 {
		t.Errorf("dev-moved carries pr = %v, want 0", lines[1]["pr"])
	}
	if msg, _ := lines[1]["msg"].(string); !strings.Contains(msg, "0d739352") {
		t.Errorf("the dev-moved msg does not name the sha: %q", msg)
	}
}

// 3. A quiet poll publishes nothing and so emits nothing: the log is as silent as the bus.
func TestEmitterIsSilentWhenTheBusIs(t *testing.T) {
	_, rdb, ctx := newBus(t)
	p, sink := testProducer(t, rdb, &fakeForge{snap: Snapshot{
		PRs:  []PRState{{Number: 42, Head: "a1b2c3d", Conclusion: ConclusionSuccess}},
		Base: "0d739352",
	}})
	if _, err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sink.Reset()
	if n, err := p.PollOnce(ctx); err != nil || n != 0 {
		t.Fatalf("a second identical poll published %d, want 0 (%v)", n, err)
	}
	if sink.Len() != 0 {
		t.Fatalf("a quiet poll emitted lines: %s", sink.String())
	}
}

// 4. No sink means no lines: the emitter is additive, and a caller that configures none
// keeps exactly the stdout and stderr it had before this slice.
func TestEmitterWritesNothingWithoutASink(t *testing.T) {
	_, rdb, ctx := newBus(t)
	p := NewProducer(rdb, &fakeForge{snap: Snapshot{Base: "0d739352"}}, "events", nil)
	if n, err := p.PollOnce(ctx); err != nil || n != 1 {
		t.Fatalf("PollOnce = %d, %v", n, err)
	}
}

// 5. SPEC-LOGS.md Part 2: a secret value never leaves the process. The card id here comes
// off the stream, which is data from outside this program, and it is shaped like an API
// key; the line still ships and the value does not.
func TestEmitterRedactsASecretShapedValue(t *testing.T) {
	const secret = "sk-live-9aXbQ2mR7tZk4LpW8vNc3JdH"
	_, rdb, ctx := newBus(t)
	p, sink := testProducer(t, rdb, &fakeForge{})
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	sink.Reset()
	if err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamCardsDone,
		Values: map[string]interface{}{"card": secret, "label": "leaked"},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PublishCardsDone(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.String(), secret) {
		t.Fatalf("a secret value reached the log: %s", sink.String())
	}
	if !strings.Contains(sink.String(), novalog.Redacted) {
		t.Fatalf("the line carries no redaction mark: %s", sink.String())
	}
	if len(emitted(t, sink)) != 1 {
		t.Fatalf("the redacted event is not one line: %s", sink.String())
	}
}
