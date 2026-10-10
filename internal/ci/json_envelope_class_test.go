package ci

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The json-envelope rule: a --json run prints exactly one JSON object on
// stdout, and the object's result.status and result.exit agree with the
// process exit (skeleton contract 1.4; docs/STANDARD.md section 2, "One output
// structure, two renderings"; the status contract of STANDARD section 2,
// "Exit codes tell the truth and the banner states them").
// The walk is functional (json_envelope_functional_test.go); the judge below is
// proved in the unit tier.

// jsonEnvelopeStatusExit is the exit a skeleton status stands for: ok 0,
// failed 1, refused 2. The second result is false for a status the one output
// structure does not define.
func jsonEnvelopeStatusExit(status string) (int, bool) {
	switch status {
	case "ok":
		return 0, true
	case "failed":
		return 1, true
	case "refused":
		return 2, true
	}
	return 0, false
}

// jsonEnvelopeAnswers is "" when a --json run's stdout is exactly one JSON
// object whose result.status and result.exit agree with the process exit code;
// otherwise it names the breach. It reads only stdout: --json is always
// stdout, whatever the run said.
func jsonEnvelopeAnswers(code int, stdout string) string {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return fmt.Sprintf("stdout is empty at exit %d; --json prints exactly one object", code)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var envelope struct {
		Result *struct {
			Status string `json:"status"`
			Exit   *int   `json:"exit"`
		} `json:"result"`
	}
	if err := dec.Decode(&envelope); err != nil {
		return fmt.Sprintf("stdout is not one JSON object at exit %d (%v): %s", code, err, jsonEnvelopeFirstLine(trimmed))
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Sprintf("stdout carries more than one JSON value at exit %d; --json prints exactly one object: %s", code, jsonEnvelopeFirstLine(trimmed))
	}
	if envelope.Result == nil {
		return fmt.Sprintf("the object carries no result at exit %d; --json prints the one Out value: %s", code, jsonEnvelopeFirstLine(trimmed))
	}
	if envelope.Result.Exit == nil {
		return fmt.Sprintf("result carries no exit at exit %d; --json prints the one Out value: %s", code, jsonEnvelopeFirstLine(trimmed))
	}
	if *envelope.Result.Exit != code {
		return fmt.Sprintf("result.exit=%d disagrees with the process exit %d; %s", *envelope.Result.Exit, code, jsonEnvelopeRemedy)
	}
	want, ok := jsonEnvelopeStatusExit(envelope.Result.Status)
	if !ok {
		return fmt.Sprintf("result.status=%q is not ok, failed or refused at exit %d; %s", envelope.Result.Status, code, jsonEnvelopeRemedy)
	}
	if want != code {
		return fmt.Sprintf("result.status=%q exits %d, but the process exited %d; %s", envelope.Result.Status, want, code, jsonEnvelopeRemedy)
	}
	return ""
}

// jsonEnvelopeFirstLine is a one-line excerpt of a stdout that is not the one
// object, so a refusal can name the site a reader sees.
func jsonEnvelopeFirstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if len(text) > 160 {
		text = text[:160] + "…"
	}
	return text
}

// jsonEnvelopeRemedy is the remedy line every json-envelope violation names.
const jsonEnvelopeRemedy = "print one object from the one Out value (internal/tool renders --json from the same value as the lines): result.status is ok|failed|refused and result.exit equals the process exit"

// TestJSONEnvelopeJudges is the witness: a fixture that breaks the rule once is
// refused naming the site and remedy, and the fixed fixture passes.
func TestJSONEnvelopeJudges(t *testing.T) {
	t.Parallel()

	const okEnvelope = `{"result":{"verb":"list","status":"ok","exit":0},"facts":{},"items":[{"kind":"row","fields":{"name":"a"}}]}`
	const failedEnvelope = `{"result":{"verb":"check","status":"failed","exit":1,"why":["two findings"]},"facts":{}}`
	const refusedEnvelope = `{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-demo put -h","why":["--store is required"]},"facts":{}}`

	for _, tc := range []struct {
		name   string
		code   int
		stdout string
		want   string
	}{
		{"good ok envelope", 0, okEnvelope, ""},
		{"good failed envelope", 1, failedEnvelope, ""},
		{"good refused envelope", 2, refusedEnvelope, ""},
		{"good envelope with a trailing newline", 0, okEnvelope + "\n", ""},
		{"the witness: empty stdout", 0, "", "stdout is empty"},
		{"the witness: plain text, not JSON", 0, "VERB OK\n", "is not one JSON object"},
		{"the witness: a text line before the object", 0, "NOTE first\n" + okEnvelope, "is not one JSON object"},
		{"the witness: two objects", 0, okEnvelope + "\n" + okEnvelope + "\n", "carries more than one JSON value"},
		{"the witness: a JSON array", 0, "[" + okEnvelope + "]", "is not one JSON object"},
		{"the witness: no result", 0, `{"facts":{}}`, "carries no result"},
		{"the witness: result.exit disagrees with the process exit", 3, refusedEnvelope, "result.exit=2 disagrees with the process exit 3"},
		{"the witness: result.status disagrees with the process exit", 1, `{"result":{"status":"ok","exit":1}}`, `result.status="ok" exits 0, but the process exited 1`},
		{"the witness: an unknown status", 0, `{"result":{"status":"done","exit":0}}`, `result.status="done" is not ok, failed or refused`},
		{"the witness: a missing result.exit", 0, `{"result":{"status":"ok"}}`, "result carries no exit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := jsonEnvelopeAnswers(tc.code, tc.stdout)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
		})
	}
}

// TestJSONEnvelopeReadsFixtures is the witness test: a fixture that breaks the
// rule once is refused naming the site, and the fixed fixture passes.
func TestJSONEnvelopeReadsFixtures(t *testing.T) {
	t.Parallel()

	breachStdout := `{"result":{"verb":"list","status":"ok","exit":1},"facts":{}}`
	breach := jsonEnvelopeAnswers(1, breachStdout)
	assert.Contains(t, breach, `result.status="ok" exits 0, but the process exited 1`)

	fixedStdout := `{"result":{"verb":"list","status":"failed","exit":1},"facts":{}}`
	fixed := jsonEnvelopeAnswers(1, fixedStdout)
	assert.Empty(t, fixed)
}
