package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The fake connection: a go-redis UniversalClient that answers FCALL and
// FCALL_RO from a script of replies and records every call. Nothing dials:
// only the methods Redis uses are implemented, and the embedded interface is
// nil, so any other call panics.

type fakeReply struct {
	val string
	err error
}

type fakeCall struct {
	fn   string
	ro   bool
	args []any
}

type fakeConn struct {
	redis.UniversalClient
	maxRetries int
	replies    []fakeReply
	execErr    error

	mu        sync.Mutex
	pipelines int
	execs     int
	calls     []fakeCall
}

func (f *fakeConn) Options() *redis.Options { return &redis.Options{MaxRetries: f.maxRetries} }

func (f *fakeConn) Pipeline() redis.Pipeliner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pipelines++
	return &fakePipe{conn: f}
}

type fakePipe struct {
	redis.Pipeliner
	conn *fakeConn
	cmds []redis.Cmder
}

func (p *fakePipe) FCall(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	return p.add(ctx, fn, false, args)
}

func (p *fakePipe) FCallRO(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	return p.add(ctx, fn, true, args)
}

func (p *fakePipe) add(ctx context.Context, fn string, ro bool, args []any) *redis.Cmd {
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	i := len(p.conn.calls)
	p.conn.calls = append(p.conn.calls, fakeCall{fn: fn, ro: ro, args: args})
	cmd := redis.NewCmd(ctx)
	switch {
	case i >= len(p.conn.replies):
		cmd.SetErr(redis.Nil) // no reply came for it
	case p.conn.replies[i].err != nil:
		cmd.SetErr(p.conn.replies[i].err)
	default:
		cmd.SetVal(p.conn.replies[i].val)
	}
	p.cmds = append(p.cmds, cmd)
	return cmd
}

func (p *fakePipe) Exec(context.Context) ([]redis.Cmder, error) {
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	p.conn.execs++
	return p.cmds, p.conn.execErr
}

// serverError is a reply the server gave, as go-redis types one.
type serverError string

func (e serverError) Error() string { return string(e) }
func (serverError) RedisError()     {}

func okEnvelope(firstSeq, lastSeq string, lines int, parts string) string {
	return fmt.Sprintf(`{"reply":{"status":"ok","epoch_before":"0","epoch_after":"0","changed":1,"guarded":0,`+
		`"changed_per_entry":[1],"first_seq":%q,"last_seq":%q,"lines":%d,"result":"","replay":false,"counters":{}},"parts":%s}`,
		firstSeq, lastSeq, lines, parts)
}

const (
	refusedPlace = `{"status":"refused","code":"PLACE","detail":{"ids":["p1"],"cells":[],"rows":[]},"message":"PLACE; nothing was changed"}`
	readReply    = `{"status":"read","epoch":"0","active_epoch":"0","time_ms":"7","answers":[{"kind":"count","counts":[2],"sum":2},{"kind":"related","records":[]}],"complete":true,"counters":{}}`
	countReply   = `{"status":"read","epoch":"0","active_epoch":"0","time_ms":"7","answers":[{"kind":"count","counts":[2],"sum":2}],"complete":true,"counters":{}}`
	pageReply    = `{"status":"page","epoch":"0","active_epoch":"0","time_ms":"7","items":[],"next":null,"through":"0","exhausted":true,"counters":{}}`
)

// relatedQuery is a sprint query as IT30 will send it.
var relatedQuery = SprintQuery{Kind: "related", Query: json.RawMessage(`{"kind":"related","table":"work","ids":["p1"],"fields":[]}`)}

// TestRedisSendsOneFCALLPerStep: each step is exactly one FCALL
// ns_sprint_step with the version, Layer 1's step bytes and the sprint half,
// and each read one FCALL_RO ns_sprint_read with one plan carrying every
// query, the sprint's after Layer 1's.
func TestRedisSendsOneFCALLPerStep(t *testing.T) {
	t.Parallel()
	fake := &fakeConn{replies: []fakeReply{{val: okEnvelope("1", "2", 2, `{"lease":{"held":"self"}}`)}, {val: readReply}}}
	r := NewRedis(fake, testNames)
	req := seedRequest()
	req.Lease = &LeasePart{Owner: "tok", HoldMS: 5000}
	read := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: "work", Cells: []string{"s1:waiting"}}},
		Sprint: []SprintQuery{relatedQuery}}
	results, err := r.Pipeline(context.Background(), []Item{{Step: req}, {Read: read}})
	if err != nil {
		t.Fatal(err)
	}
	if fake.pipelines != 1 || fake.execs != 1 || len(fake.calls) != 2 {
		t.Fatalf("pipelines %d, flushes %d, calls %d; want 1, 1, 2", fake.pipelines, fake.execs, len(fake.calls))
	}
	enc, ref := encodeStep(testPrefix, req)
	if ref != nil {
		t.Fatal(ref)
	}
	step := fake.calls[0]
	if step.fn != fnStep || step.ro || len(step.args) != 3 || step.args[0] != Version ||
		step.args[1] != string(enc.raw) || step.args[2] != string(enc.sprint) {
		t.Fatalf("step call %s ro=%v args %v", step.fn, step.ro, step.args)
	}
	var sp map[string]json.RawMessage
	if json.Unmarshal([]byte(step.args[2].(string)), &sp) != nil || sp["lease"] == nil || sp["meta"] == nil {
		t.Fatalf("sprint half %s", step.args[2])
	}
	rd := fake.calls[1]
	var plan struct {
		Epoch   string            `json:"epoch"`
		Mode    string            `json:"mode"`
		Queries []json.RawMessage `json:"queries"`
	}
	if rd.fn != fnRead || !rd.ro || len(rd.args) != 2 || json.Unmarshal([]byte(rd.args[1].(string)), &plan) != nil ||
		plan.Mode != "atomic" || len(plan.Queries) != 2 || string(plan.Queries[1]) != string(relatedQuery.Query) {
		t.Fatalf("read call %s ro=%v args %v", rd.fn, rd.ro, rd.args)
	}
	if results[0].Step == nil || results[0].Step.Reply.LastSeq != "2" || string(results[0].Step.Parts["lease"]) != `{"held":"self"}` {
		t.Fatalf("step result %+v", results[0])
	}
	if results[1].Read == nil || len(results[1].Read.Tset) != 1 || len(results[1].Read.Sprint) != 1 ||
		results[1].Read.Tset[0].Sum != 2 || results[1].Read.TimeMS != "7" {
		t.Fatalf("read result %+v", results[1])
	}
}

// TestRedisMalformedItemSendsNothing: one malformed item refuses the whole
// pipeline before a command is sent, naming the item.
func TestRedisMalformedItemSendsNothing(t *testing.T) {
	t.Parallel()
	fake := &fakeConn{replies: []fakeReply{{val: okEnvelope("0", "0", 0, `{}`)}}}
	r := NewRedis(fake, testNames)
	fence := &Request{Epoch: "0", Fence: true, Body: Body{Op: &Op{ID: "op/p1", Intent: "x"}}, Lease: &LeasePart{Owner: "tok"}}
	for _, items := range [][]Item{
		{{Step: seedRequest()}, {Step: fence}},
		{{Step: seedRequest()}, {}},
		{{Read: &ReadRequest{Epoch: "0", Sprint: []SprintQuery{{Kind: "range", Query: json.RawMessage(`{"kind":"range","fields":[]}`)}}}}},
		{{Page: &tset.ReadPlan{Epoch: "0", Mode: "page", Queries: []tset.ReadQuery{{Kind: "last"}}}}},
	} {
		_, err := r.Pipeline(context.Background(), items)
		var itemErr *ItemError
		var ref *Refusal
		if !errors.As(err, &itemErr) || !errors.As(err, &ref) || ref.Code != CodeRequest {
			t.Fatalf("items %+v: %v, want a REQUEST refusal of the pipeline", items, err)
		}
	}
	if fake.pipelines != 0 || len(fake.calls) != 0 {
		t.Fatalf("a refused pipeline sent: pipelines %d, calls %d", fake.pipelines, len(fake.calls))
	}
}

// countingClient counts the pipelines a Client is given.
type countingClient struct {
	c Client
	n int
}

func (cc *countingClient) Pipeline(ctx context.Context, items []Item) ([]Result, error) {
	cc.n++
	return cc.c.Pipeline(ctx, items)
}

// TestClientNeverRetries: a lost reply is OUTCOMEUNKNOWN after one send,
// holding the exact bytes of both halves, and nothing resends it: not the
// Redis client, not Step. The replies that came stay known; a server error
// from before the function ran is known not to have applied; a client that
// could retry, or route, is refused before anything is sent.
func TestClientNeverRetries(t *testing.T) {
	t.Parallel()
	req := seedRequest()
	enc, ref := encodeStep(testPrefix, req)
	if ref != nil {
		t.Fatal(ref)
	}

	t.Run("a lost reply", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{err: io.ErrUnexpectedEOF}}, execErr: io.ErrUnexpectedEOF}
		cc := &countingClient{c: NewRedis(fake, testNames)}
		res, err := Step(context.Background(), cc, req)
		if err != nil {
			t.Fatal(err)
		}
		var unknown *OutcomeUnknownError
		if !errors.As(res.Err, &unknown) || !errors.Is(res.Err, tset.ErrOutcomeUnknown) ||
			string(unknown.Step) != string(enc.raw) || string(unknown.Sprint) != string(enc.sprint) {
			t.Fatalf("result %+v; want OUTCOMEUNKNOWN with the bytes sent", res)
		}
		if cc.n != 1 || fake.pipelines != 1 || fake.execs != 1 || len(fake.calls) != 1 {
			t.Fatalf("pipelines given %d, opened %d, flushes %d, calls %d; want one of each", cc.n, fake.pipelines, fake.execs, len(fake.calls))
		}
	})

	t.Run("a reply lost after another came", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{val: okEnvelope("1", "2", 2, `{}`)}}, execErr: io.ErrUnexpectedEOF}
		results, err := Steps(context.Background(), NewRedis(fake, testNames), []*Request{req, moveRequest("waiting", "ready")})
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Step == nil || !errors.Is(results[1].Err, tset.ErrOutcomeUnknown) || len(fake.calls) != 2 || fake.execs != 1 {
			t.Fatalf("results %+v, calls %d, flushes %d", results, len(fake.calls), fake.execs)
		}
	})

	t.Run("an unreadable reply", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{val: "not json"}}}
		res, err := Step(context.Background(), NewRedis(fake, testNames), req)
		if err != nil || !errors.Is(res.Err, tset.ErrOutcomeUnknown) || len(fake.calls) != 1 {
			t.Fatalf("result %+v, err %v, calls %d", res, err, len(fake.calls))
		}
	})

	t.Run("a refusal and a server error before the function ran", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{val: refusedPlace}, {err: serverError("ERR Function not found")}}}
		results, err := Steps(context.Background(), NewRedis(fake, testNames), []*Request{req, req})
		if err != nil {
			t.Fatal(err)
		}
		var client *tset.ClientError
		if results[0].Refusal == nil || results[0].Refusal.Code != "PLACE" || results[0].Refusal.Detail.IDs[0] != "p1" ||
			!errors.As(results[1].Err, &client) || client.Code != "FUNCTIONMISSING" || errors.Is(results[1].Err, tset.ErrOutcomeUnknown) {
			t.Fatalf("results %+v", results)
		}
	})

	t.Run("a client that could resend or route", func(t *testing.T) {
		t.Parallel()
		retrying := &fakeConn{maxRetries: 3, replies: []fakeReply{{val: okEnvelope("0", "0", 0, `{}`)}}}
		for name, c := range map[string]*Redis{
			"retries on": NewRedis(retrying, testNames),
			"no client":  NewRedis(nil, testNames),
			"cluster":    NewRedis(&redis.ClusterClient{}, testNames),
			"ring":       NewRedis(&redis.Ring{}, testNames),
		} {
			_, err := Step(context.Background(), c, req)
			var client *tset.ClientError
			if !errors.As(err, &client) || client.Code != "UNSUPPORTEDSTORE" {
				t.Fatalf("%s: %v, want UNSUPPORTEDSTORE", name, err)
			}
		}
		if retrying.pipelines != 0 || len(retrying.calls) != 0 {
			t.Fatalf("a retrying client was sent %d calls", len(retrying.calls))
		}
	})

	t.Run("the twin through Step", func(t *testing.T) {
		t.Parallel()
		tw, _, _ := newTestTwin(t, passX())
		cc := &countingClient{c: tw}
		mustStep(t, cc, req)
		if res, err := Step(context.Background(), cc, moveRequest("ready", "working")); err != nil || res.Refusal == nil {
			t.Fatalf("a refused step: %+v, %v", res, err)
		}
		if cc.n != 2 {
			t.Fatalf("two steps gave the twin %d pipelines", cc.n)
		}
	})
}
