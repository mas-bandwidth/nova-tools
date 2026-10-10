package ci

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The json-envelope rule: every verb accepts --json and its stdout is exactly one
// object whose result.status and result.exit agree with the process exit
// (docs/STANDARD.md section 2, "One output structure, two renderings"; skeleton
// contract 1.4). The skeleton, internal/tool, renders one Out as one JSON object;
// a tool that prints its own shape, more than one object, or nothing breaches it.
// The walk over the built tools is functional (json_envelope_functional_test.go);
// the judge below is proved in the unit tier.

// jsonEnvelopeEnvelope is the one object the skeleton renders.
type jsonEnvelopeEnvelope struct {
	Result jsonEnvelopeResult `json:"result"`
}

// jsonEnvelopeResult is the result object every --json rendering carries.
type jsonEnvelopeResult struct {
	Status string `json:"status"`
	Exit   int    `json:"exit"`
}

// jsonEnvelopeStatus is the status word the exit table requires, the inverse of
// internal/tool's Out.Exit mapping: 0 ok, 1 failed, every other code refused.
func jsonEnvelopeStatus(code int) string {
	switch code {
	case 0:
		return "ok"
	case 1:
		return "failed"
	default:
		return "refused"
	}
}

// jsonEnvelopeAnswers is "" when the process's stdout is exactly one JSON object
// whose result.status and result.exit agree with the exit code; otherwise it
// names the breach. It reads stdout only: the skeleton renders a refusal's JSON
// to stdout as well, so a refusal on stderr in the text grammar is itself a
// breach. stderr is taken so a message can point at where the text went.
func jsonEnvelopeAnswers(code int, stdout, stderr string) string {
	trimmed := strings.TrimRight(stdout, "\n")
	if trimmed == "" {
		if strings.TrimSpace(stderr) != "" {
			return fmt.Sprintf("stdout carries no JSON object for exit %d; the --json rendering belongs on stdout (the text form went to stderr: %q)", code, jsonEnvelopeFirstLine(stderr))
		}
		return fmt.Sprintf("stdout carries no JSON object for exit %d; the skeleton renders one object for every result", code)
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) != 1 {
		return fmt.Sprintf("stdout carries %d lines, not one JSON object for exit %d; the skeleton renders exactly one line", len(lines), code)
	}
	var env jsonEnvelopeEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		return fmt.Sprintf("stdout is not one JSON object for exit %d (%v): %q", code, err, jsonEnvelopeFirstLine(lines[0]))
	}
	if env.Result.Status == "" {
		return fmt.Sprintf("the JSON object carries no result.status for exit %d; the skeleton renders result {status, exit}: %q", code, jsonEnvelopeFirstLine(lines[0]))
	}
	if env.Result.Exit != code {
		return fmt.Sprintf("result.exit %d disagrees with the process exit %d; the skeleton renders the exit it returns", env.Result.Exit, code)
	}
	if want := jsonEnvelopeStatus(code); env.Result.Status != want {
		return fmt.Sprintf("result.status %q disagrees with the exit %d (want %q); the status word follows the exit table", env.Result.Status, code, want)
	}
	return ""
}

// firstLine is the first non-empty line of s, for a message that names the site.
func jsonEnvelopeFirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// TestJsonEnvelopeJudges is the witness: a fixture that breaks the rule once is
// refused naming the site and remedy, and the fixed fixture passes.
func TestJsonEnvelopeJudges(t *testing.T) {
	t.Parallel()

	const goodOK = `{"result":{"verb":"run","status":"ok","exit":0},"facts":{}}`
	const goodRefused = `{"result":{"verb":"send","status":"refused","exit":2,"remedy":"nova-bus help send"}}`

	for _, tc := range []struct {
		name           string
		code           int
		stdout, stderr string
		want           string
	}{
		{"good ok object", 0, goodOK + "\n", "", ""},
		{"good refused object", 2, goodRefused + "\n", "", ""},
		{"good failed object", 1, `{"result":{"verb":"run","status":"failed","exit":1}}` + "\n", "", ""},
		{"the witness: two objects on stdout", 0, goodOK + "\n" + goodOK + "\n", "", "2 lines, not one JSON object"},
		{"the witness: the object is missing result.status", 0, `{"result":{"verb":"run","exit":0}}` + "\n", "", "no result.status"},
		{"the witness: result.exit disagrees with the exit", 1, `{"result":{"verb":"run","status":"failed","exit":0}}` + "\n", "", "result.exit 0 disagrees with the process exit 1"},
		{"the witness: result.status disagrees with the exit", 1, `{"result":{"verb":"run","status":"ok","exit":1}}` + "\n", "", `result.status "ok" disagrees with the exit 1`},
		{"the witness: stdout is the text form, not JSON", 2, "", "RUN REFUSED: why; run: tool help\n", "stdout carries no JSON object"},
		{"the witness: stdout is not JSON at all", 0, "RUN OK done=1\n", "", "not one JSON object"},
		{"the witness: an empty run", 0, "", "", "stdout carries no JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := jsonEnvelopeAnswers(tc.code, tc.stdout, tc.stderr)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
		})
	}
}

// TestJsonEnvelopeFixesTheFixture is the second half of the witness: the fixture
// that broke the rule passes once the object carries result.status and
// result.exit that agree with the exit.
func TestJsonEnvelopeFixesTheFixture(t *testing.T) {
	t.Parallel()

	breach := `{"result":{"verb":"run","status":"ok","exit":1}}` + "\n"
	assert.Contains(t, jsonEnvelopeAnswers(1, breach, ""), "disagrees with the exit 1")

	fixed := `{"result":{"verb":"run","status":"failed","exit":1}}` + "\n"
	assert.Empty(t, jsonEnvelopeAnswers(1, fixed, ""))
}
