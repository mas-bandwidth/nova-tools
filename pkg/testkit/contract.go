package testkit

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The probes' names: nothing defines them, so every tool refuses them.
const (
	NoSuchFlag  = "--no-such-flag-breadcrumb"
	NoSuchFlag2 = "--no-such-flag-either"
)

// Contract runs the skeleton contract's probes on a tool (docs/STANDARD.md,
// the skeleton contract's "How a port is proved"): the bare command refuses in
// one line naming its door at exit 2; an unknown verb refuses naming the
// nearest; an unknown flag on each verb refuses naming it; two bad flags in one
// run are both named; --json on a refusal is one object on stdout at the same
// exit; <verb> -h answers on stdout at exit 0 with nothing on stderr; and a
// refusal's remedy run back through the tool does not refuse for the same
// reason. Every breach is reported with assert, naming the probe, so one call
// holds a tool to the whole shape.
//
// verbs are the tool's verbs, each as its own words ("send", "fn load"). Every
// verb named must take --json and answer -h: Contract holds the verbs the
// caller lists, and a verb that prints its own output (tool.Flags.Prints) or
// refuses help (tool.HelpRefused) is left out by its tool's own contract test.
func Contract(t testing.TB, m Main, verbs []string) {
	t.Helper()
	require.NotEmpty(t, verbs, "no verbs named: a contract over no verbs would pass by checking nothing")

	bare := m.Run()
	assert.Equal(t, 2, bare.Code, "bare command: a command with no verb refuses at exit 2; %s", runText(bare))
	assert.Equal(t, 1, refusalLines(bare), "bare command: the refusal is one line; %s", runText(bare))
	if line := refusalLine(bare); assert.NotEmpty(t, line, "bare command: no refusal line; %s", runText(bare)) {
		assert.Contains(t, line, "help", "bare command: the refusal does not name its door (help); %q", line)
		if road := remedyOf(line); assert.NotEmpty(t, road, "bare command: the refusal names no remedy; %q", line) {
			back := m.Run(remedyArgs(road)...)
			assert.NotContains(t, back.Stderr, whyOf(line), "bare command: the remedy %q refuses for the same reason; %s", road, runText(back))
		}
	}

	near := verbs[0] + "z"
	unknown := m.Run(near)
	assert.Equal(t, 2, unknown.Code, "unknown verb: %q refuses at exit 2; %s", near, runText(unknown))
	if line := refusalLine(unknown); assert.NotEmpty(t, line, "unknown verb: %q names no refusal; %s", near, runText(unknown)) {
		assert.Contains(t, line, near, "unknown verb: the refusal does not name %q; %q", near, line)
		assert.Contains(t, line, "did you mean", "unknown verb: the refusal does not name the nearest; %q", line)
		assert.Contains(t, line, verbs[0], "unknown verb: the refusal does not name the nearest %q; %q", verbs[0], line)
	}

	both := m.Run(append(strings.Fields(verbs[0]), NoSuchFlag, NoSuchFlag2)...)
	assert.Equal(t, 2, both.Code, "two bad flags: refuses at exit 2; %s", runText(both))
	assert.Contains(t, both.Stderr, NoSuchFlag, "two bad flags: the first is not named; %s", runText(both))
	assert.Contains(t, both.Stderr, NoSuchFlag2, "two bad flags: the second is not named; %s", runText(both))

	for _, verb := range verbs {
		word := strings.Fields(verb)
		plain := m.Run(append(append([]string{}, word...), NoSuchFlag)...)
		assert.Equal(t, 2, plain.Code, "%s unknown flag: refuses at exit 2; %s", verb, runText(plain))
		if line := refusalLine(plain); assert.NotEmpty(t, line, "%s unknown flag: no refusal line; %s", verb, runText(plain)) {
			assert.Contains(t, line, NoSuchFlag, "%s unknown flag: the refusal does not name it; %q", verb, line)
		}

		asJSON := m.Run(append(append(append([]string{}, word...), NoSuchFlag), "--json")...)
		assert.Equal(t, plain.Code, asJSON.Code, "%s --json: the JSON refusal exits as the plain one (%d); %s", verb, plain.Code, runText(asJSON))
		var obj struct {
			Result struct {
				Status string `json:"status"`
				Exit   int    `json:"exit"`
			} `json:"result"`
		}
		dec := json.NewDecoder(strings.NewReader(asJSON.Stdout))
		if assert.NoError(t, dec.Decode(&obj), "%s --json: stdout is not one JSON object; %s", verb, runText(asJSON)) {
			assert.Equal(t, io.EOF, dec.Decode(&struct{}{}), "%s --json: stdout holds more than one JSON value; %s", verb, runText(asJSON))
			assert.Equal(t, "refused", obj.Result.Status, "%s --json: the object's status is refused; %s", verb, runText(asJSON))
			assert.Equal(t, plain.Code, obj.Result.Exit, "%s --json: the object's exit is the run's; %s", verb, runText(asJSON))
		}

		help := m.Run(append(append([]string{}, word...), "-h")...)
		assert.Equal(t, 0, help.Code, "%s -h: help exits 0; %s", verb, runText(help))
		assert.NotEmpty(t, help.Stdout, "%s -h: help is on stdout; %s", verb, runText(help))
		assert.Empty(t, help.Stderr, "%s -h: help is not a refusal; %s", verb, runText(help))
		assert.Contains(t, help.Stdout, "usage:", "%s -h: the answer is a usage text; %s", verb, runText(help))
	}
}

// refusalLine is a run's first stderr line in the refusal grammar,
// `<TOKEN> REFUSED: <why>; run: <remedy>` (docs/STANDARD.md, "The status word
// leads every line"), else "".
func refusalLine(r Result) string {
	for _, line := range strings.Split(r.Stderr, "\n") {
		if strings.Contains(line, "REFUSED") && strings.Contains(line, "; run: ") {
			return line
		}
	}
	return ""
}

// refusalLines is how many stderr lines carry the refusal status word.
func refusalLines(r Result) int {
	n := 0
	for _, line := range strings.Split(r.Stderr, "\n") {
		if strings.Contains(line, "REFUSED") {
			n++
		}
	}
	return n
}

// whyOf is the reason of a refusal line: what follows "REFUSED: " up to the
// remedy, else "".
func whyOf(line string) string {
	_, rest, found := strings.Cut(line, "REFUSED: ")
	if !found {
		return ""
	}
	why, _, _ := strings.Cut(rest, "; run: ")
	return why
}

// remedyOf is the remedy of a refusal line: what follows its last "; run: ",
// else "".
func remedyOf(line string) string {
	i := strings.LastIndex(line, "; run: ")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(line[i+len("; run: "):])
}

// remedyArgs is a remedy command run through a Main: a refusal's remedy names
// the tool first and a Main takes the words after it, so the first word is
// dropped when there is more than one. A one-word remedy is run as it stands.
func remedyArgs(remedy string) []string {
	words := strings.Fields(remedy)
	if len(words) > 1 && !strings.HasPrefix(words[0], "-") {
		return words[1:]
	}
	return words
}

// runText is a run as a failure prints it.
func runText(r Result) string {
	return fmt.Sprintf("exit=%d stdout=%q stderr=%q", r.Code, r.Stdout, r.Stderr)
}
