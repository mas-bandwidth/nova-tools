package testverbhelp

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const completeHelp = `usage: tool go [--fast] [--out <path>]
Goes to the place named and says where it got to.
flags:
  --fast  skip the checks
  --out <string>  where to write the result
example: tool go --out /tmp/x
exit codes: 0 done, 2 could not run
`

const completeRefusal = "GO REFUSED: unknown flag --nope; the flags of go are --fast, --out; run: tool go -h\n"

func fixture(help, refusal string) Run {
	return func(args []string, stdout, stderr io.Writer) int {
		if args[len(args)-1] == UnknownFlag {
			_, _ = io.WriteString(stderr, refusal)
			return 2
		}
		_, _ = io.WriteString(stdout, help)
		return 0
	}
}

// A fixture verb missing each part is red for that part, and only that part.
func TestEveryVerbsHelpIsComplete(t *testing.T) {
	t.Parallel()
	require.Empty(t, Gaps(completeHelp, completeRefusal), "the complete fixture is the green witness")
	rows := []struct {
		name string
		help string
		ref  string
		want []string
	}{
		{"no usage line", strings.Replace(completeHelp, "usage: tool go [--fast] [--out <path>]", "tool go does things", 1), completeRefusal, []string{GapUsage}},
		{"no description", strings.Replace(completeHelp, "Goes to the place named and says where it got to.\n", "", 1), completeRefusal, []string{GapDescription}},
		{"a flag with no description", strings.Replace(completeHelp, "  --fast  skip the checks", "  --fast", 1), completeRefusal, []string{GapFlagPrefix + "fast"}},
		{"a typed flag with no description", strings.Replace(completeHelp, "  --out <string>  where to write the result", "  --out <string>", 1), completeRefusal, []string{GapFlagPrefix + "out"}},
		{"a registered flag -h omits", strings.Replace(completeHelp, "  --fast  skip the checks\n", "", 1), completeRefusal, []string{GapMissingPrefix + "fast"}},
		{"no example", strings.Replace(completeHelp, "example: tool go --out /tmp/x\n", "", 1), completeRefusal, []string{GapExample}},
		{"no exit codes", strings.Replace(completeHelp, "exit codes: 0 done, 2 could not run\n", "", 1), completeRefusal, []string{GapExitCodes}},
		{"a synopsis alone is no description", strings.Replace(completeHelp, "Goes to the place named and says where it got to.", "tool go --fast", 1), completeRefusal, []string{GapDescription}},
	}
	for _, r := range rows {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, r.want, Gaps(r.help, r.ref))
			_, gaps := Completeness(fixture(r.help, r.ref), Case{Verb: "go"}, t.TempDir())
			assert.Equal(t, r.want, gaps)
		})
	}
}

// The ledger only shrinks: a gap not in it fails, and so does a line already fixed.
func TestTheCompleteLedgerOnlyShrinks(t *testing.T) {
	t.Parallel()
	rows, err := Ledger("# c\ntool go example,exit-codes\n")
	require.NoError(t, err)
	key := "tool go"
	assert.Empty(t, Judge(key, []string{GapExample, GapExitCodes}, rows[key]))
	assert.Len(t, Judge(key, []string{GapExample, GapExitCodes, GapUsage}, rows[key]), 1, "a new gap is refused")
	assert.Len(t, Judge(key, []string{GapExample}, rows[key]), 1, "a fixed part still in the ledger is refused")
	assert.Len(t, Judge(key, nil, rows[key]), 2)
	_, err = Ledger("tool go\n")
	assert.Error(t, err)
	_, err = Ledger("tool go example\ntool go usage\n")
	assert.Error(t, err, "a verb twice")
	got, _ := Ledger("tool go b,a\n")
	assert.True(t, reflect.DeepEqual(got["tool go"], []string{"a", "b"}))
}

// The shipped ledger parses and is sorted, so a diff of it shows only what shrank.
func TestTheShippedCompleteLedgerParses(t *testing.T) {
	t.Parallel()
	text := readLedger(t)
	_, err := Ledger(text)
	require.NoError(t, err)
	var keys []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			keys = append(keys, l)
		}
	}
	assert.IsIncreasing(t, keys, "keep the ledger's lines sorted")
}

func readLedger(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(LedgerPath())
	require.NoError(t, err)
	return string(b)
}

// LedgerPath names a real file even when Go builds the test with -trimpath, in
// which case runtime.Caller returns this source file's import path instead of
// its directory; the ledger is then found from the test's working directory,
// and a tool under cmd/ is still recognized as shipped.
func TestTheLedgerIsFoundInATrimpathBuild(t *testing.T) {
	t.Parallel()
	p := LedgerPath()
	require.True(t, filepath.IsAbs(p), "LedgerPath() = %q, want an absolute file path", p)
	require.FileExists(t, p)
	assert.True(t, shipped("nova-check"), "a directory under cmd/ is a shipped tool")
}
