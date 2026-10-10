package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/TESTS.md is the page a stranger meets first, and its promise is exact: every
// transcript line is real output pasted whole -- type these lines, see these lines, in
// this order and no others. The `## nova-secrets` section had no test at all, so it could
// rot silently. Three of its four documented commands (`check`, `names`, `exec`) need a
// real sops binary and a sealed store and cannot be executed by a test; the `keygen` step
// can, because its receipt comes from the pure keygenLines. The two age1... strings are
// PUBLIC keys already committed to this repository and are handled as text only, never as
// key material. The pub and recovery keys are the transcript's declared Norms; everything
// else is compared exactly. placeholder is false because the documented command passes
// --store, so the `SECRETS RULE NOTE` line is absent and must not appear.
func TestTheKeygenFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	all := strings.Split(string(data), "\n")

	start := -1
	for i, l := range all {
		if strings.TrimRight(l, "\r") == "## nova-secrets" {
			start = i
			break
		}
	}
	require.GreaterOrEqual(t, start, 0, "docs/TESTS.md has no `## nova-secrets` section")
	end := len(all)
	for i := start + 1; i < len(all); i++ {
		if strings.HasPrefix(strings.TrimRight(all[i], "\r"), "## ") {
			end = i
			break
		}
	}
	section := all[start+1 : end]

	firstRun := -1
	for i, l := range section {
		if strings.TrimRight(l, "\r") == "### First run" {
			firstRun = i
			break
		}
	}
	require.GreaterOrEqual(t, firstRun, 0, "the `## nova-secrets` section has no `### First run` subsection")
	fenceOpen := -1
	for i := firstRun + 1; i < len(section); i++ {
		if strings.HasPrefix(strings.TrimRight(section[i], "\r"), "```") {
			fenceOpen = i
			break
		}
	}
	require.GreaterOrEqual(t, fenceOpen, 0, "the `### First run` subsection has no fenced block")
	fenceClose := -1
	for i := fenceOpen + 1; i < len(section); i++ {
		if strings.HasPrefix(strings.TrimRight(section[i], "\r"), "```") {
			fenceClose = i
			break
		}
	}
	require.GreaterOrEqual(t, fenceClose, 0, "the `### First run` fenced block is not closed")
	block := section[fenceOpen+1 : fenceClose]

	var steps [][]string
	for _, raw := range block {
		l := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(l, "$ ") {
			steps = append(steps, []string{l})
			continue
		}
		if len(steps) == 0 {
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		steps[len(steps)-1] = append(steps[len(steps)-1], l)
	}
	require.NotEmpty(t, steps, "the `### First run` block documents no commands")
	for _, s := range steps {
		require.True(t, strings.HasPrefix(s[0], "$ nova-secrets "), "a documented first-run command is not `$ nova-secrets ...`: %q", s[0])
	}

	step := steps[0]
	cmdFields := strings.Fields(strings.TrimPrefix(step[0], "$ "))
	require.GreaterOrEqual(t, len(cmdFields), 2, "the first documented first-run command is not `keygen`: %q", step[0])
	require.Equal(t, "keygen", cmdFields[1], "the first documented first-run command is not `keygen`: %q", step[0])

	flagValue := func(name string) string {
		for i, f := range cmdFields {
			if f == name && i+1 < len(cmdFields) {
				return cmdFields[i+1]
			}
		}
		return ""
	}
	as := flagValue("--as")
	key := flagValue("--key")
	require.NotEmpty(t, as, "the documented keygen command has no --as argument: %q", step[0])
	require.NotEmpty(t, key, "the documented keygen command has no --key argument: %q", step[0])

	const rulePrefix = "SECRETS RULE       age: "
	pub, recovery := "", ""
	foundKeys := false
	for _, l := range step[1:] {
		if strings.HasPrefix(l, rulePrefix) {
			parts := strings.Split(strings.TrimPrefix(l, rulePrefix), ",")
			require.Len(t, parts, 2, "the documented SECRETS RULE age line does not carry exactly two keys: %q", l)
			pub, recovery = parts[0], parts[1]
			foundKeys = true
			break
		}
	}
	require.True(t, foundKeys, "the keygen step documents no `SECRETS RULE       age: ` line to read the public keys from")

	printed := keygenLines(as, key, pub, recovery, false)
	documented := step[1:]

	if assert.Len(t, documented, len(printed), "docs/TESTS.md promises every transcript line is real output pasted whole; the keygen first-run step documents %d line(s) where the tool prints %d, so the transcript is abridged or padded\n--- documented ---\n%s\n--- printed ---\n%s",
		len(documented), len(printed), strings.Join(documented, "\n"), strings.Join(printed, "\n")) {
		for i := range documented {
			assert.Equal(t, printed[i], documented[i], "docs/TESTS.md promises every transcript line is real output pasted whole, in order; line %d of the keygen first-run transcript is not what the tool prints\n  documented: %q\n  printed:    %q", i+1, documented[i], printed[i])
		}
	}

	foundNext := false
	for _, l := range printed {
		if l == KeygenNextLine {
			foundNext = true
		}
	}
	assert.True(t, foundNext, "the receipt no longer carries the exact KeygenNextLine const %q:\n%s", KeygenNextLine, strings.Join(printed, "\n"))
}
