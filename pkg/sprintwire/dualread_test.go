package sprintwire

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dualReadFixture is a sprint answer as the server writes it today
// (json.Marshal of Response, the legacy form): an accepted verb, and a refused
// one with text json.Marshal escapes (<, >, &) and an outcome left out.
var dualReadFixture = Response{Results: []Result{
	{Code: 0, Stdout: "MOVED 1\n<ok> & done\n", Stderr: "", Outcome: "accepted", Changed: "yes"},
	{Code: 2, Stdout: "", Stderr: "nova-sprint finish REFUSED: usage\n", Outcome: "", Changed: "no"},
}}

// dualReadForms is the fixture in every form the reader accepts: the legacy
// JSON, the schema JSON text, the binary wire and the padded standard base64 of
// it, the table forms written by the generated writer from the legacy read.
func dualReadForms(t *testing.T) map[string][]byte {
	t.Helper()
	legacy, err := json.Marshal(dualReadFixture)
	require.NoError(t, err)
	d, res, err := newAnswerReader().Read(legacy)
	require.NoError(t, err, "%s", legacy)
	require.Equal(t, schemaread.FormJSON, res.Form, "the legacy JSON is the schema JSON text form")
	wire, text := answerWire(&d), answerJSON(&d)
	require.NotEmpty(t, wire)
	require.NotEmpty(t, text)
	return map[string][]byte{
		"legacy": legacy,
		"json":   text,
		"binary": wire,
		"base64": []byte(base64.StdEncoding.EncodeToString(wire)),
	}
}

// requireRefusedByName holds the refusal shape: `Response REFUSED: <reason>;
// run: nova-sprint -h`, a typed schemaread.Refusal, and no results at all.
// It returns nothing: the callers assert the shape and read no value back.
func requireRefusedByName(t *testing.T, res []Result, err error, msgAndArgs ...any) {
	t.Helper()
	require.Error(t, err, msgAndArgs...)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), msgAndArgs...)
	assert.Contains(t, err.Error(), "Response REFUSED: ", msgAndArgs...)
	assert.Contains(t, err.Error(), "; run: nova-sprint -h", msgAndArgs...)
	assert.NotEmpty(t, r.Reason, msgAndArgs...)
	assert.Nil(t, res, msgAndArgs...)
}

// TestSprintwireDualRead is the dual-read proof of the sprint answer and the
// worker's reads of it (S2, S4, the reader-first order C13): the result list
// reads alike from the binary wire, its padded standard base64, the schema
// JSON text and the legacy JSON the server writes today; a fuzzed malformed
// value of each form is refused by name in every case and never read as
// absent, empty or zero; a clamped value is refused; reads are counted per
// record and per form; the worker refuses a list that is not one result and an
// outcome it cannot read, and counts both. The transport is a fake that hands
// the reader bytes: no socket is opened, and no clock is read.
func TestSprintwireDualRead(t *testing.T) {
	t.Parallel()
	forms := dualReadForms(t)

	// every form reads alike, as the Result values the legacy JSON holds, and
	// each read is counted under its own form
	rd := newAnswerReader()
	for _, name := range []string{"legacy", "json", "binary", "base64"} {
		got, err := readAnswer(rd, forms[name])
		require.NoError(t, err, name)
		assert.Equal(t, dualReadFixture.Results, got, name)
	}
	assert.Equal(t, uint64(2), rd.Counts().Reads(schemaread.FormJSON), "the legacy and the schema JSON text")
	assert.Equal(t, uint64(1), rd.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), rd.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(0), rd.Counts().Reads(schemaread.FormLegacy))

	// the client reads every form off the fake transport, through the one
	// reader whose counters a process exports
	before := map[schemaread.Form]uint64{}
	for _, f := range []schemaread.Form{schemaread.FormJSON, schemaread.FormBinary, schemaread.FormBase64} {
		before[f] = answerCounts().Reads(f)
	}
	for _, name := range []string{"legacy", "json", "binary", "base64"} {
		body := forms[name]
		c := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
		})}}
		got, err := c.Do(context.Background(), []string{"finish", "--as", "m1"}, []string{"status"})
		require.NoError(t, err, name)
		assert.Equal(t, dualReadFixture.Results, got, name)
	}
	assert.GreaterOrEqual(t, answerCounts().Reads(schemaread.FormJSON)-before[schemaread.FormJSON], uint64(2))
	assert.GreaterOrEqual(t, answerCounts().Reads(schemaread.FormBinary)-before[schemaread.FormBinary], uint64(1))
	assert.GreaterOrEqual(t, answerCounts().Reads(schemaread.FormBase64)-before[schemaread.FormBase64], uint64(1))

	// a value past a declared bound is refused by name, never clamped
	for _, c := range []struct{ name, in, want string }{
		{"an exit code past 255", `{"results":[{"code":256,"stdout":"","stderr":""}]}`, "Response REFUSED: clamped"},
		{"a negative exit code", `{"results":[{"code":-1,"stdout":"","stderr":""}]}`, "Response REFUSED: clamped"},
		{"results past MaxVerbs", `{"results":[` + manyResults(MaxVerbs+1) + `]}`, "Response REFUSED: clamped (results)"},
	} {
		res, err := readAnswer(rd, []byte(c.in))
		requireRefusedByName(t, res, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
	}
	_, err := readAnswer(rd, []byte(`{"results":[`+manyResults(MaxVerbs)+`]}`))
	require.NoError(t, err, "MaxVerbs results is in bounds")

	// an answer that names no exit code or output, a key given twice, a null
	// result or outcome, and text that is no form at all are refused by name
	for _, c := range []struct{ name, in, want string }{
		{"no exit code", `{"results":[{"stdout":"","stderr":""}]}`, "absent_required (code)"},
		{"no stdout", `{"results":[{"code":0,"stderr":""}]}`, "absent_required (stdout)"},
		{"no stderr", `{"results":[{"code":0,"stdout":""}]}`, "absent_required (stderr)"},
		{"a code given twice", `{"results":[{"code":0,"code":1,"stdout":"","stderr":""}]}`, "Response REFUSED: "},
		{"a null result", `{"results":[null]}`, "Response REFUSED: kind_mismatch"},
		{"a null outcome", `{"results":[{"code":0,"stdout":"","stderr":"","outcome":null}]}`, "Response REFUSED: kind_mismatch"},
		{"an outcome by number", `{"results":[{"code":0,"stdout":"","stderr":"","outcome":7}]}`, "Response REFUSED: kind_mismatch"},
		{"a word", `ok`, "Response REFUSED: "},
		{"nothing", ``, "Response REFUSED: unknown_form"},
		{"a binary image of an unknown form", string(append([]byte{9}, forms["binary"][1:]...)), "Response REFUSED: unknown_form"},
	} {
		res, err := readAnswer(rd, []byte(c.in))
		requireRefusedByName(t, res, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
	}

	// an unknown field, or an outcome word the table does not know, reads as
	// an unknown field: a damaged key or id, refused as malformed, never read as
	// absent (a newer server's field comes only after every reader updates, S4)
	for _, in := range []string{
		`{"results":[{"code":1,"stdout":"","stderr":"x","outcome":"maybe"}]}`,
		`{"results":[{"code":1,"stdout":"","stderr":"x","outcomes":"refused"}]}`,
		`{"result":[{"code":1,"stdout":"","stderr":"x"}]}`,
	} {
		res, err := readAnswer(rd, []byte(in))
		requireRefusedByName(t, res, err, in)
		assert.Contains(t, err.Error(), "Response REFUSED: malformed", in)
	}

	// fuzzed malformed values of each form: every cut of every form is refused
	// by name, in 100% of cases; every single-bit flip of the binary wire and
	// 128 seeded random byte flips of each form are refused by name or read a
	// whole answer of two results, never an absent, empty or zero one
	rng := rand.New(rand.NewPCG(1000, 21))
	for name, img := range forms {
		for n := 0; n < len(img); n++ {
			if bytes.Equal(bytes.TrimSpace(img[:n]), bytes.TrimSpace(img)) {
				continue // only trailing white space cut: the same text
			}
			res, err := readAnswer(rd, img[:n])
			requireRefusedByName(t, res, err, "%s cut at %d of %d", name, n, len(img))
		}
		for i := range img {
			if name != "binary" {
				break // the text forms are swept by the random flips below
			}
			for bit := range 8 {
				bad := bytes.Clone(img)
				bad[i] ^= 1 << bit
				res, err := readAnswer(rd, bad)
				if err != nil {
					requireRefusedByName(t, res, err, "%s bit %d of byte %d", name, bit, i)
					continue
				}
				require.Len(t, res, 2, "%s bit %d of byte %d reads a whole answer or none", name, bit, i)
			}
		}
		for range 128 {
			bad := bytes.Clone(img)
			i := rng.IntN(len(bad))
			bad[i] ^= byte(1 + rng.IntN(255))
			res, err := readAnswer(rd, bad)
			if err != nil {
				requireRefusedByName(t, res, err, "%s flipped at %d", name, i)
				continue
			}
			require.Len(t, res, 2, "%s flipped at %d reads a whole answer or none", name, i)
		}
	}

	// the client refuses what the reader refuses, and a count other than the
	// verbs sent, as nothing known of what ran
	for _, c := range []struct{ name, body, want string }{
		{"a cut binary answer", string(forms["binary"][:len(forms["binary"])-1]), "Response REFUSED: "},
		{"no results key", `{}`, "answered 0 results for 2 verbs"},
	} {
		body := c.body
		cl := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader([]byte(body))), Header: http.Header{}}, nil
		})}}
		res, err := cl.Do(context.Background(), []string{"finish"}, []string{"status"})
		require.Error(t, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
		assert.Nil(t, res, c.name)
	}

	// the worker record: a list that is not one result for the one verb is a
	// counted refusal, a try that did not answer, never res[0] of nothing
	readsBefore, resultsBefore, outcomeBefore := resultReadCount(), resultRefusedResultsCount(), resultRefusedOutcomeCount()
	for _, n := range []int{0, 2} {
		w := &Worker{Send: func(context.Context, ...[]string) ([]Result, error) { return make([]Result, n), nil }}
		code, out, answer := w.RunAnswer("finish", "--as", "m1", "s1-1.w1@1")
		assert.Equal(t, 2, code, "%d results", n)
		assert.Equal(t, Unknown, answer, "%d results", n)
		assert.Contains(t, string(out), "Result REFUSED: malformed (results: ", "%d results", n)
		assert.Contains(t, string(out), "; run: nova-sprint -h", "%d results", n)
	}
	assert.GreaterOrEqual(t, resultRefusedResultsCount()-resultsBefore, uint64(2*Tries))

	// an outcome word the worker cannot read is a counted refusal, read as
	// Unknown, never as no outcome (Classify would call exit 0 accepted)
	assert.Equal(t, Unknown, Result{Code: 0, Stdout: "MOVED 1\n", Outcome: "maybe"}.Answer())
	assert.Equal(t, Accepted, Result{Code: 0, Stdout: "MOVED 1\n"}.Answer())
	assert.GreaterOrEqual(t, resultRefusedOutcomeCount()-outcomeBefore, uint64(1))
	assert.GreaterOrEqual(t, resultReadCount()-readsBefore, uint64(2))
}

// manyResults is n copies of one well-formed result, comma-joined.
func manyResults(n int) string {
	var b bytes.Buffer
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"code":0,"stdout":"","stderr":""}`)
	}
	return b.String()
}

// FuzzSprintwireDualRead holds the reader's one promise over any input: it is
// refused by name, or it reads a list of results each of which names an exit
// code in 0..255; it never panics and never reads a refusal as an answer.
func FuzzSprintwireDualRead(f *testing.F) {
	legacy, err := json.Marshal(dualReadFixture)
	if err != nil {
		f.Fatal(err)
	}
	d, _, err := newAnswerReader().Read(legacy)
	if err != nil {
		f.Fatal(err)
	}
	wire := answerWire(&d)
	f.Add(legacy)
	f.Add(answerJSON(&d))
	f.Add(wire)
	f.Add([]byte(base64.StdEncoding.EncodeToString(wire)))
	f.Add([]byte(`{"results":[{"code":256,"stdout":"","stderr":""}]}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		res, err := readAnswer(newAnswerReader(), in)
		if err != nil {
			var r *schemaread.Refusal
			require.ErrorAs(t, err, &r)
			assert.Equal(t, "Response", r.Name)
			assert.NotEmpty(t, r.Reason)
			assert.Nil(t, res)
			return
		}
		for _, r := range res {
			assert.GreaterOrEqual(t, r.Code, 0)
			assert.LessOrEqual(t, r.Code, 255)
		}
	})
}
