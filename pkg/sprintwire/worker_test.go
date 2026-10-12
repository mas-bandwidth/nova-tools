package sprintwire

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder is a server's step that keeps what it was sent and answers each verb
// with the result given.
type recorder struct {
	sent   [][]string
	answer Result
	fail   int // the sends that are not answered, first
}

func (r *recorder) send(_ context.Context, verbs ...[]string) ([]Result, error) {
	r.sent = append(r.sent, verbs...)
	if r.fail > 0 {
		r.fail--
		return nil, errors.New("the sprint server did not answer")
	}
	return []Result{r.answer}, nil
}

// A worker's verb is sent as given and its answer comes back as the verb's own:
// exit 0 with what it printed, or its exit code with its words.
func TestAWorkerRunsAVerbOnTheServer(t *testing.T) {
	t.Parallel()
	ok := &recorder{answer: Result{Stdout: "QUEUE OK\n"}}
	code, out := (&Worker{Send: ok.send}).Run("queue", "--as", "m1", "--json")
	assert.Equal(t, 0, code)
	assert.Equal(t, "QUEUE OK\n", string(out))
	assert.Equal(t, [][]string{{"queue", "--as", "m1", "--json"}}, ok.sent, "a read is sent as given, with no operation id")

	refused := &recorder{answer: Result{Code: 1, Stdout: "MOVED 0\n", Stderr: "REFUSED s1-1.w1: not working\n"}}
	code, out = (&Worker{Send: refused.send}).Run("finish", "--as", "m1", "s1-1.w1@1")
	assert.Equal(t, 1, code)
	assert.Equal(t, "REFUSED s1-1.w1: not working\nMOVED 0\n", string(out))

	shaped := &Worker{Send: refused.send, Failed: func(stdout, stderr []byte) []byte { return []byte("shaped") }}
	_, out = shaped.Run("finish", "--as", "m1", "s1-1.w1@1")
	assert.Equal(t, "shaped", string(out))
}

// A write carries one operation id through its tries: a verb that ran and whose
// answer was lost is sent again as the same operation. After its tries the
// worker has exit 2 and the server's words.
func TestAWriteKeepsItsOperationIDThroughItsTries(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"take", "finish", "read"} {
		r := &recorder{fail: 2, answer: Result{Stdout: "ok\n"}}
		code, _ := (&Worker{Send: r.send}).Run(verb, "--as", "m1")
		require.Equal(t, 0, code, verb)
		require.Len(t, r.sent, 3, verb)
		assert.Equal(t, r.sent[0], r.sent[1], verb)
		assert.Equal(t, r.sent[0], r.sent[2], verb)
		n := len(r.sent[0])
		assert.Equal(t, "--op", r.sent[0][n-2], verb)
		assert.NotEmpty(t, r.sent[0][n-1], verb)
	}
	a, b := &recorder{}, &recorder{}
	(&Worker{Send: a.send}).Run("take", "--as", "m1")
	(&Worker{Send: b.send}).Run("take", "--as", "m1")
	assert.NotEqual(t, a.sent[0], b.sent[0], "two verbs are two operations")

	dead := &recorder{fail: Tries}
	code, out := (&Worker{Send: dead.send}).Run("finish", "--as", "m1")
	assert.Equal(t, 2, code)
	assert.Len(t, dead.sent, Tries)
	assert.Contains(t, string(out), "did not answer")
}

// A child's words reach the server as their flag's value and nothing else: a
// report or a finding that begins like a flag is joined to its flag, so the
// server's check of the words never takes it for one.
func TestAChildsWordsAreJoinedToTheirFlag(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	(&Worker{Send: r.send}).Run("read", "--as", "reader-a", "--ok", "s1-1.r1.reader-a", "--finding", "--as=m2 is the flag it should take", "--usage", "wall=1s", "--epoch", "0")
	require.Len(t, r.sent, 1)
	got := r.sent[0][:len(r.sent[0])-2] // less the operation id
	assert.Equal(t, []string{"read", "--as", "reader-a", "--ok", "s1-1.r1.reader-a", "--finding=--as=m2 is the flag it should take", "--usage=wall=1s", "--epoch", "0"}, got)
}

// A beat names its load to the server, which cannot measure the worker's
// machine: the load the member gave, as `--load <n>`, and when it gave none, the
// last it gave. Before it has given any, the beat is not sent and says so: no
// load is invented.
func TestABeatAlwaysNamesItsOwnLoad(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	w := &Worker{Send: r.send}
	code, out := w.Run("fleet", "beat", "m1")
	assert.Equal(t, 1, code)
	assert.Contains(t, string(out), "no sample yet")
	assert.Empty(t, r.sent, "a beat with no load ever sampled is not sent")
	for _, args := range [][]string{
		{"fleet", "beat", "m1", "--load", "41.5"},
		{"fleet", "beat", "m1"},
		{"fleet", "beat", "m1", "--load=7"},
		{"fleet", "beat", "m1"},
	} {
		code, _ := w.Run(args...)
		assert.Equal(t, 0, code, "%v", args)
	}
	assert.Equal(t, [][]string{
		{"fleet", "beat", "m1", "--load", "41.5"},
		{"fleet", "beat", "m1", "--load", "41.5"},
		{"fleet", "beat", "m1", "--load", "7"},
		{"fleet", "beat", "m1", "--load", "7"},
	}, r.sent)
}

// handler answers a batch as a server would, in this process: the client's
// transport calls it with no connection.
type handler struct{ got Request }

func (h *handler) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	if req.URL.Path != Path || req.Method != http.MethodPost {
		http.Error(rec, "no", http.StatusNotFound)
		return rec.Result(), nil
	}
	if err := json.NewDecoder(req.Body).Decode(&h.got); err != nil {
		http.Error(rec, err.Error(), http.StatusBadRequest)
		return rec.Result(), nil
	}
	out := Response{Results: make([]Result, len(h.got.Verbs))}
	for i := range out.Results {
		out.Results[i].Stdout = h.got.Verbs[i][0]
	}
	_ = json.NewEncoder(rec).Encode(out) // ignored: a recorder takes every write
	return rec.Result(), nil
}

// The client sends a batch as one request and returns one answer a verb, in
// order; an answer that is not one a verb is an error, never a guess.
func TestTheClientSendsABatchInOneRequest(t *testing.T) {
	t.Parallel()
	h := &handler{}
	c := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: h}}
	res, err := c.Do(context.Background(), []string{"take", "--as", "m1"}, []string{"queue", "--as", "m1"})
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Equal(t, "take", res[0].Stdout)
	assert.Equal(t, "queue", res[1].Stdout)
	assert.Equal(t, [][]string{{"take", "--as", "m1"}, {"queue", "--as", "m1"}}, h.got.Verbs)

	short := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		_ = json.NewEncoder(rec).Encode(Response{Results: []Result{{}}}) // ignored: a recorder takes every write
		return rec.Result(), nil
	})}}
	_, err = short.Do(context.Background(), []string{"take"}, []string{"queue"})
	require.ErrorContains(t, err, "answered 1 results for 2 verbs")

	gone := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}}
	_, err = gone.Do(context.Background(), []string{"take"})
	require.ErrorContains(t, err, "did not answer")
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// RunAnswer says what became of a write, which the exit code alone cannot: nova-sprint's
// refuse() exits 2 as a worker whose server never answered does, and a cut operation exits
// 1 as a refused card does. Each of nova-sprint's answers is read from the server's own
// result, whole, before Failed shapes it (cmd/nova-sprint/main.go refuse; verbs.go stepExit
// and report; internal/sprint/store engine.go ErrUnknown and CutError, epoch.go ClearedError;
// serve.go's worker-verb refusal).
func TestRunAnswerTellsARefusalFromAnUnknownOutcome(t *testing.T) {
	t.Parallel()
	shaped := func([]byte, []byte) []byte { return []byte("capped") }
	for _, tc := range []struct {
		name   string
		answer Result
		fail   int
		want   Answer
	}{
		{"accepted", Result{Stdout: "MOVED 1\nFINISH OK moved=1 refused=0 notes=0\n"}, 0, Accepted},
		{"a card refused, exit 1", Result{Code: 1, Stderr: "REFUSED s1-1.w1@1: not the live generation (2)\nFINISH FAILED moved=0 refused=1 notes=0\n"}, 0, Refused},
		{"refuse(), exit 2", Result{Code: 2, Stderr: "nova-sprint finish REFUSED: a report names the epoch its cards were handed at: --epoch <n>; nothing was changed\n"}, 0, Refused},
		{"the server's worker-verb refusal, exit 2", Result{Code: 2, Stderr: "nova-sprint server: finish: names no --as; nothing was changed\n"}, 0, Refused},
		{"a pending operation, exit 1", Result{Code: 1, Stderr: "FINISH FAILED moved=0 refused=0 notes=0 changed=no\nnova-sprint finish: operation o1 is pending: x; run: nova-sprint repair\n"}, 0, Refused},
		{"the store did not confirm, exit 2", Result{Code: 2, Stderr: "FINISH FAILED moved=0 refused=0 notes=0 changed=unknown\nnova-sprint finish: the store did not confirm the write (changed=unknown)\n"}, 0, Unknown},
		{"an operation cut, exit 1", Result{Code: 1, Stderr: "FINISH FAILED moved=0 refused=0 notes=0 changed=no\nnova-sprint finish: operation o1 cut at table cards: write failed; run: nova-sprint repair\n"}, 0, Unknown},
		{"a finish a clear closed, exit 1", Result{Code: 1, Stderr: "nova-sprint finish: the sprint was cleared at 12:00 as this step finished: its moves stand at epoch 7\n"}, 0, Unknown},
		{"no answer, the worker's own exit 2", Result{}, Tries, Unknown},
	} {
		r := &recorder{answer: tc.answer, fail: tc.fail}
		code, out, answer := (&Worker{Send: r.send, Failed: shaped}).RunAnswer("finish", "--as", "m1", "s1-1.w1@1")
		assert.Equal(t, tc.want, answer, tc.name)
		if tc.fail >= Tries {
			assert.Equal(t, 2, code, tc.name)
		} else {
			assert.Equal(t, tc.answer.Code, code, tc.name)
		}
		if code != 0 {
			assert.Equal(t, "capped", string(out), "%s: the log line is Failed's; the answer was read before it", tc.name)
		}
	}
}
