package ci

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// json_envelope_class_test.go holds the standard's one-output-structure rule
// (docs/STANDARD.md section 2, "One output structure, two renderings", and
// skeleton contract 1.4): every verb takes --json and prints exactly one JSON
// object on stdout whose result.status and result.exit agree with the process
// exit. The walk is functional (json_envelope_functional_test.go); the judge
// below is proved in the unit tier.

// jsonEnvelopeEnvelope is the one object the rule reads: its result carries the
// status word and the exit code.
type jsonEnvelopeEnvelope struct {
	Result struct {
		Status string `json:"status"`
		Exit   int    `json:"exit"`
	} `json:"result"`
}

// jsonEnvelopeStatusFor is the status word an exit requires: ok for 0, failed
// for 1, refused for 2. A code above 2 is a verb's own, so no word is required
// of it (docs/STANDARD.md section 2, "Exit codes tell the truth").
func jsonEnvelopeStatusFor(exit int) string {
	switch exit {
	case 0:
		return "ok"
	case 1:
		return "failed"
	case 2:
		return "refused"
	}
	return ""
}

// jsonEnvelopeAnswers is "" when stdout is exactly one JSON object whose
// result.status and result.exit agree with the process exit; otherwise it names
// the breach. It reads stdout only: --json is always stdout, refusals included
// (internal/tool/out.go render prints the object to w).
func jsonEnvelopeAnswers(exit int, stdout string) string {
	text := strings.TrimSpace(stdout)
	if text == "" {
		return "stdout is empty; --json prints exactly one JSON object"
	}
	dec := json.NewDecoder(strings.NewReader(text))
	var env jsonEnvelopeEnvelope
	if err := dec.Decode(&env); err != nil {
		return "stdout is not one JSON object: " + err.Error()
	}
	var more json.RawMessage
	if err := dec.Decode(&more); err != io.EOF {
		return "stdout holds more than one JSON value; --json prints exactly one object"
	}
	if env.Result.Exit != exit {
		return fmt.Sprintf("result.exit is %d but the process exits %d; --json must carry the process exit", env.Result.Exit, exit)
	}
	switch env.Result.Status {
	case "ok", "refused", "failed":
	default:
		return fmt.Sprintf("result.status is %q, not one of ok, refused, failed", env.Result.Status)
	}
	if want := jsonEnvelopeStatusFor(exit); want != "" && env.Result.Status != want {
		return fmt.Sprintf("result.status is %q but exit %d requires %q", env.Result.Status, exit, want)
	}
	return ""
}

// TestJSONEnvelopeJudges is the witness over the judge's shapes: a good object
// passes at each exit, and a breach is refused.
func TestJSONEnvelopeJudges(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		exit   int
		stdout string
		want   string
	}{
		{"good ok", 0, `{"result":{"verb":"nova-demo","status":"ok","exit":0}}` + "\n", ""},
		{"good failed", 1, `{"result":{"verb":"nova-demo","status":"failed","exit":1}}`, ""},
		{"good refused", 2, `{"result":{"verb":"nova-demo","status":"refused","exit":2}}`, ""},
		{"good code above 2", 3, `{"result":{"verb":"nova-demo","status":"failed","exit":3}}`, ""},
		{"the witness: empty stdout", 0, "", "stdout is empty"},
		{"the witness: plain text", 0, "nova-demo OK\n", "not one JSON object"},
		{"the witness: a text line before the object", 0, "nova-demo OK\n{\"result\":{\"status\":\"ok\",\"exit\":0}}", "not one JSON object"},
		{"the witness: two objects", 0, `{"result":{"status":"ok","exit":0}}` + "\n" + `{"result":{"status":"ok","exit":0}}`, "more than one JSON value"},
		{"the witness: a JSON array", 0, `[{"result":{"status":"ok","exit":0}}]`, "not one JSON object"},
		{"the witness: a missing result", 0, `{}`, "not one of ok, refused, failed"},
		{"the witness: a missing result.exit", 1, `{"result":{"status":"failed"}}`, "result.exit is 0 but the process exits 1"},
		{"the witness: an unknown status", 0, `{"result":{"status":"done","exit":0}}`, "not one of ok, refused, failed"},
		{"the witness: a status that disagrees with the exit", 1, `{"result":{"status":"ok","exit":1}}`, `requires "failed"`},
		{"the witness: a result.exit that disagrees with the process", 2, `{"result":{"status":"ok","exit":0}}`, "result.exit is 0 but the process exits 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := jsonEnvelopeAnswers(tc.exit, tc.stdout)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
		})
	}
}

// TestJSONEnvelopeReadsFixtures is the required witness: a fixture that breaks
// the rule once is refused naming the breach, and the fixed fixture passes.
func TestJSONEnvelopeReadsFixtures(t *testing.T) {
	t.Parallel()

	breach := `{"result":{"verb":"nova-demo","status":"ok","exit":0}}`
	assert.Contains(t, jsonEnvelopeAnswers(2, breach), "result.exit is 0 but the process exits 2")
	assert.Empty(t, jsonEnvelopeAnswers(0, breach))
}
