package tset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type fakeRedisReply struct {
	value string
	err   error
}

type fakeRedisServerError string

func (e fakeRedisServerError) Error() string { return string(e) }
func (fakeRedisServerError) RedisError()     {}

type fakeRedisClient struct {
	redis.UniversalClient
	replies       []fakeRedisReply
	maxRetries    int
	fcallCalls    int
	pipelineCalls int
	flushCalls    int
	capturedArgs  [][]interface{}
}

func (f *fakeRedisClient) Options() *redis.Options {
	return &redis.Options{MaxRetries: f.maxRetries}
}

func (f *fakeRedisClient) FCall(ctx context.Context, function string, keys []string, args ...interface{}) *redis.Cmd {
	f.fcallCalls++
	f.capturedArgs = append(f.capturedArgs, append([]interface{}{function, keys}, args...))
	return f.command(ctx, f.fcallCalls-1)
}

func (f *fakeRedisClient) Pipeline() redis.Pipeliner {
	f.pipelineCalls++
	return &fakeRedisPipeline{client: f}
}

func (f *fakeRedisClient) command(ctx context.Context, index int) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if index >= len(f.replies) {
		cmd.SetErr(errors.New("fake: no reply configured"))
		return cmd
	}
	if f.replies[index].err != nil {
		cmd.SetErr(f.replies[index].err)
	} else {
		cmd.SetVal(f.replies[index].value)
	}
	return cmd
}

type fakeRedisPipeline struct {
	redis.Pipeliner
	client   *fakeRedisClient
	commands []redis.Cmder
}

func (p *fakeRedisPipeline) FCall(ctx context.Context, function string, keys []string, args ...interface{}) *redis.Cmd {
	idx := len(p.commands)
	p.client.capturedArgs = append(p.client.capturedArgs, append([]interface{}{function, keys}, args...))
	cmd := p.client.command(ctx, idx)
	p.commands = append(p.commands, cmd)
	return cmd
}

func (p *fakeRedisPipeline) Exec(context.Context) ([]redis.Cmder, error) {
	p.client.flushCalls++
	return p.commands, nil
}

func okWireReply(result string) string {
	return fmt.Sprintf(`{"status":"ok","epoch_before":"0","epoch_after":"0","changed":0,"guarded":0,"changed_per_entry":[],"first_seq":"0","last_seq":"0","lines":0,"result":%q,"replay":false,"counters":{}}`, result)
}

func refusedWireReply(code string) string {
	return fmt.Sprintf(`{"status":"refused","code":%q,"detail":{},"message":%q}`, code, code+"; nothing was changed")
}

func emptyStep() Step {
	return Step{Epoch: "0", Space: "s", Entries: []Entry{}}
}

func TestStepsMixedRefusalAndSuccess(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{
		{value: okWireReply("first")},
		{value: refusedWireReply("REVISION")},
		{value: okWireReply("third")},
	}}
	store := NewRedis(fake)
	results, err := store.Steps(context.Background(), []Step{emptyStep(), emptyStep(), emptyStep()})
	if err != nil {
		t.Fatalf("Steps returned batch error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d result slots; want 3", len(results))
	}
	if fake.pipelineCalls != 1 || fake.flushCalls != 1 || len(fake.capturedArgs) != 3 {
		t.Fatalf("pipeline calls=%d flushes=%d dispatched=%d; want one pipeline, one flush, three calls", fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
	if results[0].Err != nil || results[0].Reply.Result != "first" {
		t.Fatalf("first slot = (%+v, %v), want first success", results[0].Reply, results[0].Err)
	}
	var refusal *Refusal
	if !errors.As(results[1].Err, &refusal) || refusal.Code != "REVISION" {
		t.Fatalf("second slot error = %v, want REVISION refusal", results[1].Err)
	}
	if results[2].Err != nil || results[2].Reply.Result != "third" {
		t.Fatalf("third slot = (%+v, %v), want third success", results[2].Reply, results[2].Err)
	}
}

func TestOneRoundTripForSets(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{
		{value: okWireReply("second-input")},
		{value: okWireReply("first-input")},
	}}
	steps := []Step{setStep("first"), setStep("second")}
	results, err := NewRedis(fake).Steps(context.Background(), steps)
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if len(results) != 2 || results[0].Reply.Result != "second-input" || results[1].Reply.Result != "first-input" {
		t.Fatalf("aligned results = %+v; want replies in input order", results)
	}
	if fake.pipelineCalls != 1 || fake.flushCalls != 1 || len(fake.capturedArgs) != 2 {
		t.Fatalf("pipeline calls=%d flushes=%d dispatched=%d; want one pipeline, one flush, two calls", fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
	for i, args := range fake.capturedArgs {
		sent, decodeErr := stepFromCapturedArgs(args)
		if decodeErr != nil || len(sent.Entries) != 1 || !reflect.DeepEqual(sent.Entries[0].IDs, []string{"card-a", "card-b"}) || sent.Entries[0].Set["status"] != []string{"first", "second"}[i] {
			t.Errorf("request %d lost multi-id SET data: step=%+v decode-error=%v", i, sent, decodeErr)
		}
	}
}

func TestAPIAlwaysSets(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{{value: okWireReply("created")}}}
	step := setStep("ready")
	if _, err := NewRedis(fake).Step(context.Background(), step); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if len(fake.capturedArgs) != 1 {
		t.Fatalf("Step dispatched %d calls; want one", len(fake.capturedArgs))
	}
	sent, err := stepFromCapturedArgs(fake.capturedArgs[0])
	if err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	if len(sent.Entries) != 1 || !reflect.DeepEqual(sent.Entries[0].IDs, []string{"card-a", "card-b"}) {
		t.Fatalf("API request lost the two target records: %+v", sent.Entries)
	}
	if got := sent.Entries[0].Set; !reflect.DeepEqual(got, map[string]string{"status": "ready", "source": "api"}) {
		t.Fatalf("API request omitted or changed SET fields: got %v", got)
	}
}

func setStep(status string) Step {
	return Step{Epoch: "0", Space: "s", Entries: []Entry{{
		Kind: "create", Table: "cards", To: "in:ready", IDs: []string{"card-a", "card-b"},
		Scores: []string{"1", "2"}, Set: map[string]string{"status": status, "source": "api"},
	}}}
}

func TestStepsEncodingFailureDispatchesNothing(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{{value: okWireReply("unused")}}}
	bad := emptyStep()
	bad.Epoch = "00"
	results, err := NewRedis(fake).Steps(context.Background(), []Step{emptyStep(), bad})
	if err == nil || results != nil {
		t.Fatalf("Steps encoding failure = (%v, %v), want nil results and error", results, err)
	}
	if fake.pipelineCalls != 0 || fake.flushCalls != 0 || fake.fcallCalls != 0 || len(fake.capturedArgs) != 0 {
		t.Fatalf("encoding failure dispatched work: pipelines=%d flushes=%d direct=%d calls=%d", fake.pipelineCalls, fake.flushCalls, fake.fcallCalls, len(fake.capturedArgs))
	}
}

func TestLostReplyRetainsBytes(t *testing.T) {
	t.Parallel()
	transport := errors.New("connection reset after write")
	fake := &fakeRedisClient{replies: []fakeRedisReply{
		{value: okWireReply("known")},
		{err: transport},
	}}
	known, uncertain := emptyStep(), emptyStep()
	knownOp, uncertainOp := "known-op", "uncertain-op"
	knownIntent, uncertainIntent := `{"part":"known"}`, `{"part":"uncertain"}`
	known.Op, known.Intent = &knownOp, &knownIntent
	uncertain.Op, uncertain.Intent = &uncertainOp, &uncertainIntent
	steps := []Step{known, uncertain}
	results, err := NewRedis(fake).Steps(context.Background(), steps)
	if err != nil {
		t.Fatalf("dispatched Steps returned global error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d result slots; want 2", len(results))
	}
	if results[0].Err != nil || results[0].Reply.Result != "known" {
		t.Fatalf("known reply was lost: %+v, %v", results[0].Reply, results[0].Err)
	}
	if !errors.Is(results[1].Err, ErrOutcomeUnknown) || !errors.Is(results[1].Err, transport) {
		t.Fatalf("unresolved slot error = %v, want OUTCOMEUNKNOWN wrapping transport error", results[1].Err)
	}
	var unknown *OutcomeUnknownError
	if !errors.As(results[1].Err, &unknown) || !reflect.DeepEqual(unknown.RawRequest, results[1].RawRequest) {
		t.Fatalf("unresolved slot did not retain its exact dispatched request bytes: err=%+v slot=%+v", unknown, results[1])
	}
	for i, step := range steps {
		want, err := EncodeStep(step)
		if err != nil {
			t.Fatalf("EncodeStep input %d: %v", i, err)
		}
		if !reflect.DeepEqual(results[i].RawRequest, want) {
			t.Errorf("slot %d retained request %q; want original bytes %q", i, results[i].RawRequest, want)
		}
	}
	if fake.pipelineCalls != 1 || fake.flushCalls != 1 || len(fake.capturedArgs) != len(steps) {
		t.Fatalf("dispatched retries or split flushes: pipelines=%d flushes=%d calls=%d", fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
}

func TestStepPreExecutionRedisFailuresMapExactlyAndDoNotRetry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wire string
		want string
	}{
		{name: "NOSUCHFUNCTION", wire: "NOSUCHFUNCTION No matching function", want: "FUNCTIONMISSING"},
		{name: "function not found reply", wire: "ERR Function not found", want: "FUNCTIONMISSING"},
		{name: "pre-execution oom", wire: "OOM command not allowed when used memory > 'maxmemory'", want: "OOMSTART"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeRedisClient{replies: []fakeRedisReply{{err: fakeRedisServerError(tc.wire)}}}
			step := emptyStep()
			op := "stable-op"
			intent := `{"part":"fixed-1","verb":"move"}`
			step.Op, step.Intent = &op, &intent
			_, err := NewRedis(fake).Step(context.Background(), step)
			var mapped *ClientError
			if !errors.As(err, &mapped) || mapped.Code != tc.want {
				t.Fatalf("Step error = %v, want exact %s pre-execution mapping", err, tc.want)
			}
			if fake.pipelineCalls != 0 || fake.flushCalls != 0 || fake.fcallCalls != 1 || len(fake.capturedArgs) != 1 {
				t.Fatalf("Step retried dispatched request: pipelines=%d flushes=%d direct=%d calls=%d", fake.pipelineCalls, fake.flushCalls, fake.fcallCalls, len(fake.capturedArgs))
			}
			sent, decodeErr := stepFromCapturedArgs(fake.capturedArgs[0])
			if decodeErr != nil || sent.Intent == nil || *sent.Intent != intent {
				t.Fatalf("dispatched request lost caller's stable intent %q: decoded=%+v err=%v", intent, sent, decodeErr)
			}
		})
	}
}

func TestStepUnknownReplyIsNotRetriedOrRenamed(t *testing.T) {
	t.Parallel()
	transport := errors.New("read: connection reset")
	fake := &fakeRedisClient{replies: []fakeRedisReply{{err: transport}}}
	step := emptyStep()
	op := "stable-op"
	intent := "stable semantic bytes"
	step.Op, step.Intent = &op, &intent
	_, err := NewRedis(fake).Step(context.Background(), step)
	if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, transport) {
		t.Fatalf("Step error = %v, want OUTCOMEUNKNOWN with transport cause", err)
	}
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("Step error type = %T, want *OutcomeUnknownError", err)
	}
	wantRaw, encodeErr := EncodeStep(step)
	if encodeErr != nil {
		t.Fatalf("EncodeStep: %v", encodeErr)
	}
	if !reflect.DeepEqual(unknown.RawRequest, wantRaw) {
		t.Fatalf("unknown outcome retained request %q; want exact bytes %q", unknown.RawRequest, wantRaw)
	}
	if fake.pipelineCalls != 0 || fake.flushCalls != 0 || fake.fcallCalls != 1 || len(fake.capturedArgs) != 1 {
		t.Fatalf("Step retried request: pipelines=%d flushes=%d direct=%d calls=%d", fake.pipelineCalls, fake.flushCalls, fake.fcallCalls, len(fake.capturedArgs))
	}
	sent, decodeErr := stepFromCapturedArgs(fake.capturedArgs[0])
	if decodeErr != nil || sent.Intent == nil || sent.Op == nil || *sent.Intent != intent || *sent.Op != op {
		t.Fatalf("wire request changed op/intent identity: decoded=%+v err=%v", sent, decodeErr)
	}
}

func TestStepDoesNotTreatRuntimeTextAsPreexecutionProof(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "ordinary error quotes missing function", err: errors.New("NOSUCHFUNCTION No matching function")},
		{name: "script runtime error mentions OOM", err: fakeRedisServerError("ERR script failed after dispatch: OOM command not allowed when used memory > 'maxmemory'")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeRedisClient{replies: []fakeRedisReply{{err: tc.err}}}
			step := emptyStep()
			_, err := NewRedis(fake).Step(context.Background(), step)
			var unknown *OutcomeUnknownError
			if !errors.As(err, &unknown) || !errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("Step error = %v; text alone is not proof of pre-execution refusal", err)
			}
			wantRaw, encodeErr := EncodeStep(step)
			if encodeErr != nil || !reflect.DeepEqual(unknown.RawRequest, wantRaw) {
				t.Fatalf("unknown outcome request bytes = %q, want exact request %q (encode error %v)", unknown.RawRequest, wantRaw, encodeErr)
			}
			if fake.fcallCalls != 1 || fake.pipelineCalls != 0 || len(fake.capturedArgs) != 1 {
				t.Fatalf("unknown outcome retried or split dispatch: direct=%d pipelines=%d calls=%d", fake.fcallCalls, fake.pipelineCalls, len(fake.capturedArgs))
			}
		})
	}
}

func TestNoRetryClientDispatchesOnce(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{{value: okWireReply("done")}}}
	reply, err := NewRedis(fake).Step(context.Background(), emptyStep())
	if err != nil || reply.Result != "done" {
		t.Fatalf("Step with effective MaxRetries=0 returned (%+v, %v)", reply, err)
	}
	if fake.fcallCalls != 1 || fake.pipelineCalls != 0 || fake.flushCalls != 0 || len(fake.capturedArgs) != 1 {
		t.Fatalf("valid no-retry client dispatched more than once: direct=%d pipelines=%d flushes=%d calls=%d", fake.fcallCalls, fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
}

func TestRetryEnabledClientRefusesBeforeDispatch(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{maxRetries: 3, replies: []fakeRedisReply{{value: okWireReply("must not dispatch")}}}
	store := NewRedis(fake)
	_, stepErr := store.Step(context.Background(), emptyStep())
	_, batchErr := store.Steps(context.Background(), []Step{emptyStep(), emptyStep()})
	for name, err := range map[string]error{"Step": stepErr, "Steps": batchErr} {
		var clientErr *ClientError
		if !errors.As(err, &clientErr) || clientErr.Code != "UNSUPPORTEDSTORE" {
			t.Errorf("%s with retries enabled returned %v; want UNSUPPORTEDSTORE", name, err)
		}
	}
	if fake.fcallCalls != 0 || fake.pipelineCalls != 0 || fake.flushCalls != 0 || len(fake.capturedArgs) != 0 {
		t.Fatalf("retry-enabled client dispatched before refusing: direct=%d pipelines=%d flushes=%d calls=%d", fake.fcallCalls, fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
}

func TestGoRedisDefaultRetriesAndUnsupportedRoutersRefuseBeforeDial(t *testing.T) {
	t.Parallel()
	dialer := func(counter *int) func(context.Context, string, string) (net.Conn, error) {
		return func(context.Context, string, string) (net.Conn, error) {
			*counter = *counter + 1
			return nil, errors.New("unexpected dial")
		}
	}
	defaultDials := 0
	defaultClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", Dialer: dialer(&defaultDials)})
	defer defaultClient.Close()
	if defaultClient.Options().MaxRetries == 0 {
		t.Fatal("fixture unexpectedly disabled go-redis retries by default")
	}
	assertUnsupportedBeforeDial(t, "default retries", defaultClient, &defaultDials)

	clusterDials := 0
	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{"127.0.0.1:1"}, Dialer: dialer(&clusterDials)})
	defer cluster.Close()
	assertUnsupportedBeforeDial(t, "cluster router", cluster, &clusterDials)

	ringDials := 0
	ring := redis.NewRing(&redis.RingOptions{
		Addrs:  map[string]string{"one": "127.0.0.1:1"},
		Dialer: dialer(&ringDials), HeartbeatFrequency: 24 * time.Hour,
	})
	defer ring.Close()
	assertUnsupportedBeforeDial(t, "ring router", ring, &ringDials)
}

func assertUnsupportedBeforeDial(t *testing.T, name string, client redis.UniversalClient, dials *int) {
	t.Helper()
	_, err := NewRedis(client).Step(context.Background(), emptyStep())
	var clientErr *ClientError
	if !errors.As(err, &clientErr) || clientErr.Code != "UNSUPPORTEDSTORE" {
		t.Fatalf("%s Step returned %v; want UNSUPPORTEDSTORE", name, err)
	}
	if *dials != 0 {
		t.Fatalf("%s client dialed %d times before refusing", name, *dials)
	}
}

func stepFromCapturedArgs(args []interface{}) (Step, error) {
	if len(args) == 0 {
		return Step{}, errors.New("captured function call has no request argument")
	}
	raw, ok := args[len(args)-1].(string)
	if !ok {
		return Step{}, fmt.Errorf("captured request argument has type %T", args[len(args)-1])
	}
	return DecodeStep([]byte(raw))
}

func TestStepsPreservesInputRequestIdentity(t *testing.T) {
	t.Parallel()
	fake := &fakeRedisClient{replies: []fakeRedisReply{{value: okWireReply("ok")}, {value: okWireReply("ok")}}}
	first, second := emptyStep(), emptyStep()
	opA, opB := "part-0", "part-1"
	intentA, intentB := `{"part":"0"}`, `{"part":"1"}`
	first.Op, first.Intent = &opA, &intentA
	second.Op, second.Intent = &opB, &intentB
	results, err := NewRedis(fake).Steps(context.Background(), []Step{first, second})
	if err != nil || len(results) != 2 {
		t.Fatalf("Steps = (%d slots, %v)", len(results), err)
	}
	if fake.pipelineCalls != 1 || fake.flushCalls != 1 || len(fake.capturedArgs) != 2 {
		t.Fatalf("steps were retried or split: pipelines=%d flushes=%d calls=%d", fake.pipelineCalls, fake.flushCalls, len(fake.capturedArgs))
	}
	wants := []Step{first, second}
	for i, args := range fake.capturedArgs {
		sent, decodeErr := stepFromCapturedArgs(args)
		if decodeErr != nil || sent.Op == nil || sent.Intent == nil || *sent.Op != *wants[i].Op || *sent.Intent != *wants[i].Intent {
			t.Errorf("slot %d request changed original identity: got %+v want op=%q intent=%q decode-error=%v", i, sent, *wants[i].Op, *wants[i].Intent, decodeErr)
		}
		wantRaw, encodeErr := EncodeStep([]Step{first, second}[i])
		if encodeErr != nil {
			t.Fatalf("EncodeStep input %d: %v", i, encodeErr)
		}
		if !reflect.DeepEqual(results[i].RawRequest, wantRaw) {
			t.Errorf("slot %d request bytes changed: got %q want %q", i, results[i].RawRequest, wantRaw)
		}
	}
}
