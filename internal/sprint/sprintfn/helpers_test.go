package sprintfn

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// testPrefix is the deployment prefix of every test twin.
const testPrefix = "t:"

var testNames = sprint.Names{Prefix: testPrefix}

// testColumns are the set columns of the four tables on Layer 1's store: the
// present build's table columns less its computed and text columns.
var testColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

var testTime = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// newTestMem is a Mem with the four tables defined at epoch zero (errata E1).
func newTestMem(t *testing.T) *tset.Mem {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(testPrefix, table, tset.TableDefinition{Columns: testColumns[table],
			MemberPrefix: testNames.TSetMemberPrefix(table), EpochKey: testNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	return m
}

// passX is X that guards nothing and writes nothing, for tests of the write
// path's order and atomicity rather than of X.
func passX() Phases {
	return Phases{
		XPre:  func(*State, *Request, *Before) *Refusal { return nil },
		XCmds: func(*State, TablePlan, LogPlan) []Cmd { return nil },
	}
}

// newTestTwin is a twin over a fresh Mem and log stub, with its own part
// registry and the phases given, at a fixed time.
func newTestTwin(t *testing.T, phases Phases) (*Twin, *tset.Mem, *LogStub) {
	t.Helper()
	m := newTestMem(t)
	log := NewLogStub()
	tw := NewTwin(m, log, testNames)
	if tw.broken != nil {
		t.Fatal(tw.broken)
	}
	tw.parts = NewPartRegistry()
	tw.phases = phases
	tw.SetClock(func() time.Time { return testTime })
	return tw, m, log
}

// seedRequest adds the stream row s1 and creates p1 and p2 in s1:waiting.
func seedRequest() *Request {
	return &Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}},
		{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"p1", "p2"}, Scores: []string{"1", "2"},
			Set: map[string]string{"kind": "work"}, About: []string{"p1", "p2"}},
	}}}
}

// moveRequest moves p1 from one cell of s1 to another.
func moveRequest(from, to string) *Request {
	return &Request{Epoch: "0", Meta: Meta{Rule: "resolve", Tick: true}, Body: Body{Entries: []tset.Entry{
		{Kind: "move", Table: sprint.Work, From: "s1:" + from, To: "s1:" + to, IDs: []string{"p1"}, About: []string{"p1"}},
	}}}
}

func mustStep(t *testing.T, c Client, req *Request) *StepReply {
	t.Helper()
	res, err := Step(context.Background(), c, req)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if res.Refusal != nil || res.Err != nil || res.Step == nil {
		t.Fatalf("step result: refusal %v, err %v", res.Refusal, res.Err)
	}
	return res.Step
}

// image is everything the twin holds, as bytes: the Mem's exported state,
// the sprint's keys and the log's lines, so two images are equal exactly when
// nothing changed.
func image(t *testing.T, tw *Twin, m *tset.Mem, log *LogStub) []byte {
	t.Helper()
	snap, err := m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(struct {
		Tables tset.MemSnapshot
		Keys   map[string]KeyValue
		Log    []json.RawMessage
	}{snap, tw.SprintKeys(), log.Lines(testPrefix, "0")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tracer records the phases the twin enters and the hooks it calls, in order.
type tracer struct {
	mu  sync.Mutex
	got []string
}

func (tr *tracer) add(s string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.got = append(tr.got, s)
}

func (tr *tracer) reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.got = nil
}

func (tr *tracer) list() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.got...)
}

func sameStrings(a, b []string) bool {
	return reflect.DeepEqual(append([]string{}, a...), append([]string{}, b...))
}
