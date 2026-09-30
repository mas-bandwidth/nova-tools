package sprintfn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The fake connection: a go-redis UniversalClient that answers FCALL and
// FCALL_RO from a script of replies and records every call, built on the
// tests' seam (newRedisWithClient). Nothing dials: only the methods Redis uses
// are implemented, and the embedded interface is nil, so any other call
// panics. It hands replies to the commands as go-redis v9.22 does: a reply
// read is set and its error set to nil (or to the server's error); a command
// no reply reached keeps no error until Exec; and when the flush fails, Exec
// sets the transport's error on every command whose error is still nil, the
// ones whose replies were read included (go-redis's setCmdsErr), which is
// what Redis's settled commands must not let through.

type fakeReply struct {
	val string
	err error
}

type fakeCall struct {
	fn      string
	ro      bool
	numkeys any
	args    []any
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

// valueSetter is the part of a command a reply is set on.
type valueSetter interface{ SetVal(any) }

func (p *fakePipe) Process(ctx context.Context, cmd redis.Cmder) error {
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	args := cmd.Args()
	name, _ := args[0].(string)
	fn, _ := args[1].(string)
	i := len(p.conn.calls)
	p.conn.calls = append(p.conn.calls, fakeCall{fn: fn, ro: name == "fcall_ro", numkeys: args[2], args: args[3:]})
	switch {
	case i >= len(p.conn.replies):
		// no reply reaches it
	case p.conn.replies[i].err != nil:
		cmd.SetErr(p.conn.replies[i].err)
	default:
		cmd.(valueSetter).SetVal(p.conn.replies[i].val)
		cmd.SetErr(nil)
	}
	p.cmds = append(p.cmds, cmd)
	return nil
}

func (p *fakePipe) Exec(context.Context) ([]redis.Cmder, error) {
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	p.conn.execs++
	if p.conn.execErr != nil {
		for _, cmd := range p.cmds {
			if cmd.Err() == nil {
				cmd.SetErr(p.conn.execErr)
			}
		}
	}
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
	r := newRedisWithClient(fake, testNames)
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
	if step.fn != fnStep || step.ro || fmt.Sprint(step.numkeys) != "0" || len(step.args) != 3 || step.args[0] != Version ||
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
	if rd.fn != fnRead || !rd.ro || fmt.Sprint(rd.numkeys) != "0" || len(rd.args) != 2 || json.Unmarshal([]byte(rd.args[1].(string)), &plan) != nil ||
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
	r := newRedisWithClient(fake, testNames)
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
		cc := &countingClient{c: newRedisWithClient(fake, testNames)}
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
		results, err := Steps(context.Background(), newRedisWithClient(fake, testNames), []*Request{req, moveRequest("waiting", "ready")})
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
		res, err := Step(context.Background(), newRedisWithClient(fake, testNames), req)
		if err != nil || !errors.Is(res.Err, tset.ErrOutcomeUnknown) || len(fake.calls) != 1 {
			t.Fatalf("result %+v, err %v, calls %d", res, err, len(fake.calls))
		}
	})

	t.Run("a refusal and a server error before the function ran", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{val: refusedPlace}, {err: serverError("ERR Function not found")}}}
		results, err := Steps(context.Background(), newRedisWithClient(fake, testNames), []*Request{req, req})
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
			"retries on":   newRedisWithClient(retrying, testNames),
			"no client":    newRedisWithClient(nil, testNames),
			"a nil client": newRedisWithClient((*redis.Client)(nil), testNames),
			"cluster":      newRedisWithClient(&redis.ClusterClient{}, testNames),
			"ring":         newRedisWithClient(&redis.Ring{}, testNames),
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

// TestServerErrorClassification: the outcome of a step whose reply is an error
// is known not to have applied only when the error proves the function never
// ran; every other error, whichever words it carries, is OUTCOMEUNKNOWN with
// the bytes sent. The unsafe direction is the one tested hardest: an error
// taken as known when a write began would let a caller replan under a new op
// and apply the work twice. Each text below is a whole reply as the server
// gives it, typed as go-redis types a server error, and the same replies to a
// read or a page are checked too (a read writes nothing, so its unknown is
// only an error).
func TestServerErrorClassification(t *testing.T) {
	t.Parallel()
	req := seedRequest()
	enc, ref := encodeStep(testPrefix, req)
	if ref != nil {
		t.Fatal(ref)
	}
	read := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: "work", Cells: []string{"s1:waiting"}}}}

	known := []struct {
		name string
		err  error
		code string
	}{
		{"no such function", serverError("ERR Function not found"), "FUNCTIONMISSING"},
		{"no matching function", serverError("NOSUCHFUNCTION No matching function"), "FUNCTIONMISSING"},
		{"no matching script", serverError("NOSCRIPT No matching script. Please use EVAL."), "FUNCTIONMISSING"},
		{"out of memory at the start", serverError("OOM command not allowed when used memory > 'maxmemory'"), "OOMSTART"},
		{"out of memory at the start, with a stop", serverError("OOM command not allowed when used memory > 'maxmemory'."), "OOMSTART"},
		{"arity of fcall", serverError("ERR wrong number of arguments for 'fcall' command"), "ARITY"},
		{"arity of fcall_ro", serverError("ERR wrong number of arguments for 'fcall_ro' command"), "ARITY"},
		{"an ACL refusal of fcall", serverError("NOPERM User sprint has no permissions to run the 'fcall' command"), "NOPERM"},
		{"an ACL refusal of fcall_ro", serverError("NOPERM User sprint has no permissions to run the 'fcall_ro' command"), "NOPERM"},
		{"a known error, wrapped", fmt.Errorf("pipeline: %w", serverError("ERR Function not found")), "FUNCTIONMISSING"},
	}
	unknown := []struct {
		name string
		err  error
	}{
		// Server errors that follow a write that began, or that do not prove the
		// function never ran.
		{"a script error", serverError("ERR user_function:120: attempt to index a nil value script: ns_sprint_step, on @user_function:120.")},
		{"a script error from a command", serverError("ERR Error running script (call to f_1): @user_script:9: ERR value is not an integer or out of range")},
		{"a WRONGTYPE from inside the function", serverError("WRONGTYPE Operation against a key holding the wrong kind of value script: ns_sprint_step, on @user_function:212.")},
		{"a WRONGTYPE", serverError("WRONGTYPE Operation against a key holding the wrong kind of value")},
		{"an ACL refusal of a command inside the function", serverError("ERR ACL failure in script: User sprint has no permissions to run the 'hset' command script: ns_sprint_step")},
		{"an ACL refusal naming another command", serverError("NOPERM User sprint has no permissions to run the 'hset' command")},
		{"an ACL refusal of fcall with words after", serverError("NOPERM User sprint has no permissions to run the 'fcall' command script: ns_sprint_step, on @user_function:88.")},
		{"a script killed", serverError("ERR Script killed by user with SCRIPT KILL... script: ns_sprint_step, on @user_function:1.")},
		{"busy", serverError("BUSY Redis is busy running a script. You can only call SCRIPT KILL or SHUTDOWN NOSAVE.")},
		{"loading", serverError("LOADING Redis is loading the dataset in memory")},
		{"a read only replica", serverError("READONLY You can't write against a read only replica.")},
		{"out of memory inside the function", serverError("OOM command not allowed when used memory > 'maxmemory'. script: ns_sprint_step, on @user_function:88.")},
		{"the known words after a write", serverError("ERR user_function:40: ERR Function not found script: ns_sprint_step, on @user_function:40.")},
		{"a longer no-function reply", serverError("ERR Function not found: ns_sprint_step")},
		{"a longer arity reply", serverError("ERR wrong number of arguments for 'fcall' command script: x")},
		{"arity of another command", serverError("ERR wrong number of arguments for 'hset' command")},
		{"an unrelated error", serverError("ERR something else")},
		// Transport errors: never a server's proof, whatever they quote.
		{"end of file", io.EOF},
		{"an unexpected end of file", io.ErrUnexpectedEOF},
		{"a deadline", context.DeadlineExceeded},
		{"a cancelled context", context.Canceled},
		{"a timeout", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}},
		{"a closed connection", net.ErrClosed},
		{"a transport error quoting a known reply", errors.New("ERR Function not found")},
		{"a transport error quoting an ACL reply", errors.New("NOPERM User sprint has no permissions to run the 'fcall' command")},
		{"a wrapped transport error", fmt.Errorf("read: %w", io.ErrUnexpectedEOF)},
	}

	for _, c := range known {
		t.Run("known/"+c.name, func(t *testing.T) {
			t.Parallel()
			for kind, item := range map[string]Item{"step": {Step: req}, "read": {Read: read}, "page": {Page: pagePlan()}} {
				fake := &fakeConn{replies: []fakeReply{{err: c.err}}}
				results, err := newRedisWithClient(fake, testNames).Pipeline(context.Background(), []Item{item})
				if err != nil || len(results) != 1 {
					t.Fatalf("%s: %v, %d results", kind, err, len(results))
				}
				var client *tset.ClientError
				if !errors.As(results[0].Err, &client) || client.Code != c.code || errors.Is(results[0].Err, tset.ErrOutcomeUnknown) {
					t.Fatalf("%s: %q gave %v, want a known %s and not an unknown outcome", kind, c.err, results[0].Err, c.code)
				}
			}
		})
	}
	for _, c := range unknown {
		t.Run("unknown/"+c.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeConn{replies: []fakeReply{{err: c.err}}}
			res, err := Step(context.Background(), newRedisWithClient(fake, testNames), req)
			if err != nil {
				t.Fatal(err)
			}
			var lost *OutcomeUnknownError
			var client *tset.ClientError
			if !errors.As(res.Err, &lost) || !errors.Is(res.Err, tset.ErrOutcomeUnknown) || errors.As(res.Err, &client) {
				t.Fatalf("a step's %q gave %v, want OUTCOMEUNKNOWN and no known code", c.err, res.Err)
			}
			if string(lost.Step) != string(enc.raw) || string(lost.Sprint) != string(enc.sprint) || !errors.Is(res.Err, c.err) {
				t.Fatalf("OUTCOMEUNKNOWN lost the bytes sent or its cause: %+v", lost)
			}
			for kind, item := range map[string]Item{"read": {Read: read}, "page": {Page: pagePlan()}} {
				fake := &fakeConn{replies: []fakeReply{{err: c.err}}}
				results, err := newRedisWithClient(fake, testNames).Pipeline(context.Background(), []Item{item})
				if err != nil || len(results) != 1 {
					t.Fatalf("%s: %v, %d results", kind, err, len(results))
				}
				got := results[0].Err
				if got == nil || errors.As(got, &client) || errors.Is(got, tset.ErrOutcomeUnknown) || !errors.Is(got, c.err) {
					t.Fatalf("a %s's %q gave %v, want the error itself: a read writes nothing, so it has no known code and no unknown outcome", kind, c.err, got)
				}
			}
		})
	}
}

// TestNewRedisOwnsItsClient: NewRedis builds the client as Layer 1's NewRedis
// does (L1 1.5), and no caller hands it one. The client is a standalone
// go-redis client with effective MaxRetries zero, the address and user given,
// and the password read from the environment variable named: an empty name is
// no password, a name that is not set is an error that names the variable and
// carries no value, and a set one is the password. An empty address is an
// error. Construction dials nothing, the preflight passes, and Close closes the
// client it owns (a later call fails as closed, and nothing is dialed); a Redis
// on the tests' seam owns nothing to close.
func TestNewRedisOwnsItsClient(t *testing.T) {
	t.Parallel()
	if _, err := NewRedis(" ", "", "", testNames, nil); err == nil {
		t.Fatal("an empty address was accepted")
	}
	env := map[string]string{"SPRINTFN_TEST_PASSWORD": "test-value"}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	for _, c := range []struct {
		name, variable, password string
		fails                    bool
	}{
		{"no variable", "", "", false},
		{"a variable that is set", "SPRINTFN_TEST_PASSWORD", "test-value", false},
		{"a variable that is not set", "SPRINTFN_TEST_UNSET", "", true},
	} {
		client, err := newOwnedClient("sprintfn-unit-test", "sprint", c.variable, lookup)
		if c.fails {
			if err == nil || !strings.Contains(err.Error(), c.variable) || strings.Contains(err.Error(), "test-value") {
				t.Fatalf("%s: %v; want an error naming the variable and no value", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		o := client.Options()
		if o.Addr != "sprintfn-unit-test" || o.Username != "sprint" || o.Password != c.password || o.MaxRetries != 0 {
			t.Fatalf("%s: options addr %q user %q password set %v retries %d", c.name, o.Addr, o.Username, o.Password != "", o.MaxRetries)
		}
		_ = client.Close()
	}

	r, err := NewRedis("sprintfn-unit-test", "sprint", "", testNames, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.conn.(*redis.Client); !ok || r.preflight() != nil || r.prefix != testPrefix {
		t.Fatalf("NewRedis built %T (preflight %v, prefix %q); want a standalone client that passes", r.conn, r.preflight(), r.prefix)
	}
	dials := &dialCounter{}
	r.conn.AddHook(dials)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.conn.Ping(context.Background()).Err(); !errors.Is(err, redis.ErrClosed) || dials.n.Load() != 0 {
		t.Fatalf("a call after Close: %v, dials %d; want the client closed and nothing dialed", err, dials.n.Load())
	}
	if err := newRedisWithClient(&fakeConn{}, testNames).Close(); err != nil {
		t.Fatalf("Close on the tests' seam: %v", err)
	}
}

// dialCounter counts dials and refuses them, and passes every call on.
type dialCounter struct{ n atomic.Int32 }

func (d *dialCounter) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		d.n.Add(1)
		return nil, errors.New("a unit test has no store to dial")
	}
}
func (d *dialCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (d *dialCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// pipeStore is a store's side of an in-memory connection (net.Pipe: no
// socket), for go-redis itself to read replies from. It reads the commands of
// one flush, the want FCALLs, then answers the first of them with the replies
// given, in order, and closes the connection, so the replies after those are
// lost as a dropped connection loses them.
type pipeStore struct {
	mu      sync.Mutex
	dials   int
	fcalls  [][]string
	replies []string
	want    int
}

func (p *pipeStore) dial(context.Context, string, string) (net.Conn, error) {
	p.mu.Lock()
	p.dials++
	p.mu.Unlock()
	client, store := net.Pipe()
	go p.serve(store)
	return client, nil
}

func (p *pipeStore) serve(conn net.Conn) {
	defer conn.Close()
	in := bufio.NewReader(conn)
	for {
		p.mu.Lock()
		done := len(p.fcalls) == p.want
		p.mu.Unlock()
		if done {
			break
		}
		args, err := readCommand(in)
		if err != nil {
			return
		}
		switch strings.ToUpper(args[0]) {
		case "FCALL", "FCALL_RO":
		case "HELLO":
			// As a server without HELLO answers it: go-redis goes on in RESP2.
			fmt.Fprint(conn, "-ERR unknown command 'HELLO'\r\n")
			continue
		default:
			fmt.Fprint(conn, "+OK\r\n") // any other part of the handshake
			continue
		}
		p.mu.Lock()
		p.fcalls = append(p.fcalls, args)
		p.mu.Unlock()
	}
	for _, reply := range p.replies {
		if _, err := fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(reply), reply); err != nil {
			return
		}
	}
}

// readCommand reads one RESP array of bulk strings.
func readCommand(in *bufio.Reader) ([]string, error) {
	line, err := in.ReadString('\n')
	if err != nil || len(line) < 3 || line[0] != '*' {
		return nil, fmt.Errorf("not an array: %q (%v)", line, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := range args {
		line, err = in.ReadString('\n')
		if err != nil || len(line) < 3 || line[0] != '$' {
			return nil, fmt.Errorf("not a bulk string: %q (%v)", line, err)
		}
		size, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil {
			return nil, err
		}
		b := make([]byte, size+2)
		if _, err := io.ReadFull(in, b); err != nil {
			return nil, err
		}
		args[i] = string(b[:size])
	}
	return args, nil
}

// TestRedisKeepsRepliesThatCame: on go-redis itself (v9.22, over an in-memory
// connection), a flush of two steps whose second reply is lost: the first
// step's reply stays known and the second is OUTCOMEUNKNOWN with the bytes it
// sent. go-redis sets the transport's error on every command whose error is
// still nil when the connection drops, the first included; Redis's settled
// commands keep the reply that was read. One dial, one flush, and the store
// read exactly the two FCALLs.
func TestRedisKeepsRepliesThatCame(t *testing.T) {
	t.Parallel()
	store := &pipeStore{replies: []string{okEnvelope("1", "2", 2, `{}`)}, want: 2}
	client := redis.NewClient(&redis.Options{Addr: "pipe", Protocol: 2, DisableIdentity: true, MaxRetries: -1, Dialer: store.dial})
	t.Cleanup(func() { _ = client.Close() })
	second := moveRequest("waiting", "ready")
	results, err := Steps(context.Background(), newRedisWithClient(client, testNames), []*Request{seedRequest(), second})
	if err != nil {
		t.Fatal(err)
	}
	var unknown *OutcomeUnknownError
	if results[0].Step == nil || results[0].Step.Reply.LastSeq != "2" {
		t.Fatalf("the step whose reply came: %+v; want its reply", results[0])
	}
	enc, ref := encodeStep(testPrefix, second)
	if ref != nil {
		t.Fatal(ref)
	}
	if !errors.As(results[1].Err, &unknown) || string(unknown.Step) != string(enc.raw) || string(unknown.Sprint) != string(enc.sprint) {
		t.Fatalf("the step whose reply was lost: %+v; want OUTCOMEUNKNOWN with its bytes", results[1])
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.dials != 1 || len(store.fcalls) != 2 {
		t.Fatalf("dials %d, FCALLs read %d; want 1 and 2", store.dials, len(store.fcalls))
	}
	for _, args := range store.fcalls {
		if len(args) != 6 || !strings.EqualFold(args[0], "FCALL") || args[1] != fnStep || args[2] != "0" || args[3] != Version {
			t.Fatalf("the store read %q", args[:min(4, len(args))])
		}
	}
}
