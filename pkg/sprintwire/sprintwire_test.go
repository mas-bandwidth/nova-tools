package sprintwire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSend records every batch a Worker sends and answers from a script: one answer a
// call, the last answer repeated. No connection: the server's own step stands in.
type fakeSend struct {
	calls   [][][]string
	answers []func(verbs [][]string) ([]Result, error)
}

func (f *fakeSend) send(_ context.Context, verbs ...[]string) ([]Result, error) {
	f.calls = append(f.calls, verbs)
	i := min(len(f.calls)-1, len(f.answers)-1)
	return f.answers[i](verbs)
}

func ok(stdout string) func([][]string) ([]Result, error) {
	return func([][]string) ([]Result, error) { return []Result{{Code: 0, Stdout: stdout}}, nil }
}

func down(why string) func([][]string) ([]Result, error) {
	return func([][]string) ([]Result, error) { return nil, errors.New(why) }
}

var opRE = regexp.MustCompile(`^w-[0-9a-f]{16}$`)

// Run sends one verb and returns its exit code and stdout; a verb that writes (take,
// finish, read) carries one fresh --op through its tries, and its free-text flags are
// joined to their values; any other verb is sent as typed; the caller's slice is never
// changed.
func TestRunSendsTheVerbAsTypedAndAWriteWithOneOp(t *testing.T) {
	t.Parallel()
	f := &fakeSend{answers: []func([][]string) ([]Result, error){ok("fine")}}
	w := &Worker{Send: f.send}
	args := []string{"status", "--json"}
	code, out := w.Run(args...)
	assert.Equal(t, 0, code)
	assert.Equal(t, "fine", string(out))
	require.Len(t, f.calls, 1)
	assert.Equal(t, [][]string{{"status", "--json"}}, f.calls[0], "one verb a request, as typed, no --op")
	assert.Equal(t, []string{"status", "--json"}, args)

	f = &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
	w = &Worker{Send: f.send}
	args = []string{"finish", "c1", "--report", "--as=x y", "--head", "abc", "--finding", "sees --as", "--branch", "b", "--base", "m", "--usage", "u", "--reason", "r", "--other", "v"}
	code, _ = w.Run(args...)
	assert.Equal(t, 0, code)
	require.Len(t, f.calls, 1)
	sent := f.calls[0][0]
	require.Len(t, sent, 11+2)
	assert.Equal(t, []string{"finish", "c1", "--report=--as=x y", "--head=abc", "--finding=sees --as", "--branch=b", "--base=m", "--usage=u", "--reason=r", "--other", "v", "--op"}, sent[:12])
	assert.Regexp(t, opRE, sent[12])
	assert.Equal(t, "--report", args[2], "the caller's slice is as it was")

	for _, verb := range []string{"take", "read"} {
		f = &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
		w = &Worker{Send: f.send}
		w.Run(verb, "c1")
		require.Len(t, f.calls, 1)
		sent = f.calls[0][0]
		require.Len(t, sent, 4, verb)
		assert.Equal(t, "--op", sent[2])
		assert.Regexp(t, opRE, sent[3])
	}

	f = &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
	w = &Worker{Send: f.send}
	w.Run("finish", "c1", "--report")
	assert.Equal(t, "--report", f.calls[0][0][2], "a trailing text flag with no value is sent as typed")
	assert.Equal(t, "--op", f.calls[0][0][3])

	f = &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
	w = &Worker{Send: f.send}
	w.Run()
	require.Len(t, f.calls, 1, "no arguments is still one verb")
	assert.Empty(t, f.calls[0][0])
}

// A verb that did not exit 0 returns its code and what it printed, shaped by Failed (nil:
// stderr then stdout); a server that does not answer is tried Tries times with the same
// op, then exit 2 with why; one answer is enough.
func TestRunTriesTheServerThreeTimesAndKeepsOneOp(t *testing.T) {
	t.Parallel()
	f := &fakeSend{answers: []func([][]string) ([]Result, error){func([][]string) ([]Result, error) {
		return []Result{{Code: 3, Stdout: "out", Stderr: "err"}}, nil
	}}}
	w := &Worker{Send: f.send}
	code, out := w.Run("status")
	assert.Equal(t, 3, code)
	assert.Equal(t, "errout", string(out))
	require.Len(t, f.calls, 1, "a refusal is not retried")

	w = &Worker{Send: f.send, Failed: func(stdout, stderr []byte) []byte { return []byte("shaped:" + string(stdout) + "|" + string(stderr)) }}
	code, out = w.Run("status")
	assert.Equal(t, 3, code)
	assert.Equal(t, "shaped:out|err", string(out))

	f = &fakeSend{answers: []func([][]string) ([]Result, error){down("no route")}}
	w = &Worker{Send: f.send}
	code, out = w.Run("take", "c1")
	assert.Equal(t, 2, code)
	assert.Equal(t, "no route", string(out))
	require.Len(t, f.calls, Tries)
	assert.Equal(t, 3, Tries)
	op := f.calls[0][0][3]
	assert.Regexp(t, opRE, op)
	for _, c := range f.calls[1:] {
		assert.Equal(t, op, c[0][3], "one operation id through every try")
	}

	f = &fakeSend{answers: []func([][]string) ([]Result, error){down("blip"), ok("second")}}
	w = &Worker{Send: f.send}
	code, out = w.Run("status")
	assert.Equal(t, 0, code)
	assert.Equal(t, "second", string(out))
	assert.Len(t, f.calls, 2)

	f = &fakeSend{answers: []func([][]string) ([]Result, error){down("x")}}
	w = &Worker{Send: f.send, Failed: func(stdout, stderr []byte) []byte { return append([]byte("F:"), stderr...) }}
	_, out = w.Run("status")
	assert.Equal(t, "F:x", string(out), "Failed shapes the no-answer text too")

	seen := map[string]bool{}
	for range 8 {
		f = &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
		(&Worker{Send: f.send}).Run("take", "c")
		seen[f.calls[0][0][3]] = true
	}
	assert.Len(t, seen, 8, "every verb gets its own op")
}

// A fleet beat carries the load the member named, or the last it named when it names
// none; before any load is named the beat is not sent and the member is told why; the
// --load flag is moved to the end in either spelling.
func TestBeatRemembersTheLastLoadAndSendsNoneBeforeOne(t *testing.T) {
	t.Parallel()
	f := &fakeSend{answers: []func([][]string) ([]Result, error){ok("")}}
	w := &Worker{Send: f.send}
	code, out := w.Run("fleet", "beat", "m1")
	assert.Equal(t, 1, code)
	assert.Contains(t, string(out), "beat not sent: this machine's load has no sample yet")
	assert.Empty(t, f.calls, "nothing reached the server")

	code, _ = w.Run("fleet", "beat", "--load", "42", "m1", "--tiers", "pro")
	assert.Equal(t, 0, code)
	require.Len(t, f.calls, 1)
	assert.Equal(t, []string{"fleet", "beat", "m1", "--tiers", "pro", "--load", "42"}, f.calls[0][0])

	code, _ = w.Run("fleet", "beat", "m1")
	assert.Equal(t, 0, code)
	require.Len(t, f.calls, 2)
	assert.Equal(t, []string{"fleet", "beat", "m1", "--load", "42"}, f.calls[1][0], "the last load is named again")

	w.Run("fleet", "beat", "m1", "--load=7")
	assert.Equal(t, []string{"fleet", "beat", "m1", "--load", "7"}, f.calls[2][0])
	code, _ = w.Run("fleet", "beat", "m1", "--load=")
	assert.Equal(t, 1, code, "an empty --load= names an empty load: the memory is cleared and the beat is not sent")
	assert.Len(t, f.calls, 3)
	code, _ = w.Run("fleet", "beat", "m1")
	assert.Equal(t, 1, code, "and nothing is remembered after it")
	assert.Len(t, f.calls, 3)

	fresh := &Worker{Send: f.send}
	code, _ = fresh.Run("fleet", "beat", "m1", "--load")
	assert.Equal(t, 1, code, "a bare --load with no value names nothing")
	assert.Len(t, f.calls, 3)

	code, _ = fresh.Run("fleet", "beats")
	assert.Equal(t, 0, code, "only `fleet beat` is a beat")
	assert.Equal(t, []string{"fleet", "beats"}, f.calls[3][0])
}

// fakeRT answers an HTTP request without a socket: it records the request and answers
// from a function.
type fakeRT struct {
	req    *http.Request
	body   []byte
	answer func() (*http.Response, error)
}

func (f *fakeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	f.req = r
	if r.Body != nil {
		f.body, _ = io.ReadAll(r.Body)
	}
	return f.answer()
}

func reply(code int, body string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}
}

// Client.Do posts the verbs as JSON to the server's one endpoint and reads every result
// strictly: a result count other than the verb count, a result with no exit code or no
// output, a status other than 200, an answer that is no JSON, or no answer at all is an
// error, never exit 0.
func TestClientDoReadsEveryResultStrictly(t *testing.T) {
	t.Parallel()
	rt := &fakeRT{answer: reply(200, `{"results":[{"code":0,"stdout":"a","stderr":""},{"code":2,"stdout":"","stderr":"no","outcome":"refused","changed":"no"}]}`)}
	c := Client{Addr: "127.0.0.1:1", HTTP: &http.Client{Transport: rt}}
	res, err := c.Do(context.Background(), []string{"status"}, []string{"take", "c1"})
	require.NoError(t, err)
	assert.Equal(t, []Result{{Code: 0, Stdout: "a"}, {Code: 2, Stderr: "no", Outcome: "refused", Changed: "no"}}, res)
	require.NotNil(t, rt.req)
	assert.Equal(t, http.MethodPost, rt.req.Method)
	assert.Equal(t, "http://127.0.0.1:1"+Path, rt.req.URL.String())
	assert.Equal(t, "/verbs", Path)
	assert.Equal(t, "application/json", rt.req.Header.Get("Content-Type"))
	var sent Request
	require.NoError(t, json.Unmarshal(rt.body, &sent))
	assert.Equal(t, Request{Verbs: [][]string{{"status"}, {"take", "c1"}}}, sent)

	for _, tc := range []struct {
		name   string
		answer func() (*http.Response, error)
		want   string
	}{
		{"no answer", func() (*http.Response, error) { return nil, errors.New("refused") }, "the sprint server at 127.0.0.1:1 did not answer: Post \"http://127.0.0.1:1/verbs\": refused"},
		{"500", reply(500, "boom\n"), "the sprint server at 127.0.0.1:1 refused the request (Internal Server Error): boom"},
		{"404", reply(404, ""), "refused the request (Not Found): "},
		{"too many results", reply(200, `{"results":[{"code":0,"stdout":"","stderr":""},{"code":0,"stdout":"","stderr":""}]}`), "answered 2 results for 1 verbs"},
		{"no results", reply(200, `{"results":[]}`), "answered 0 results for 1 verbs"},
		{"not json", reply(200, `ok`), "Response REFUSED: malformed; run: nova-sprint -h: nothing is known of what ran"},
		{"result with no code", reply(200, `{"results":[{"stdout":"","stderr":""}]}`), "Response REFUSED: absent_required (code); run: nova-sprint -h: nothing is known of what ran"},
		{"result with no stdout", reply(200, `{"results":[{"code":0,"stderr":""}]}`), "Response REFUSED: absent_required (stdout)"},
		{"result with no stderr", reply(200, `{"results":[{"code":0,"stdout":""}]}`), "Response REFUSED: absent_required (stderr)"},
		{"null result", reply(200, `{"results":[null]}`), "Response REFUSED: kind_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Client{Addr: "127.0.0.1:1", HTTP: &http.Client{Transport: &fakeRT{answer: tc.answer}}}
			res, err := c.Do(context.Background(), []string{"status"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Nil(t, res, "an error is no result at all")
		})
	}
}

// The wire's bounds and the request's shape are fixed numbers other ends depend on.
func TestWireBounds(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1<<20, MaxRequest)
	assert.Equal(t, 256, MaxVerbs)
	b, err := json.Marshal(Response{Results: []Result{{Code: 1, Stdout: "o", Stderr: "e"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"results":[{"code":1,"stdout":"o","stderr":"e"}]}`, string(b), "outcome and changed are left out when empty")
}
