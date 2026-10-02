package testverbhelp

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HelpWhateverFollows checks `<tool> help <verb>` for every verb the tool's help
// names, and for extra, the verbs it does not (a verb of every kind in nova-config):
// with nothing after the verb, with a word, with `--` and a word, and with --json,
// it prints what `<verb> -h` prints, at exit 0, with nothing on stderr. A word after
// the verb never turns a request for help into a run, a refusal, or the help of
// another verb, and a verb of two words (`fn load`) is matched whole.
func HelpWhateverFollows(t *testing.T, run Run, banner, prog string, extra ...string) {
	t.Helper()
	verbs := slices.Compact(slices.Sorted(slices.Values(slices.Concat(verbflag.Verbs(banner, prog), extra))))
	require.NotEmpty(t, verbs, "no verbs read from the help text; a check over no verbs would pass by checking nothing")
	for _, verb := range verbs {
		var want, stderr bytes.Buffer
		require.Zero(t, run(append(strings.Fields(verb), "-h"), &want, &stderr), "%s -h: %s", verb, stderr.String())
		require.NotEmpty(t, want.String(), "%s -h printed nothing", verb)
		for _, after := range [][]string{nil, {"extra"}, {"--", "x"}, {"--json"}} {
			args := append(append([]string{"help"}, strings.Fields(verb)...), after...)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				t.Parallel()
				var out, errs bytes.Buffer
				assert.Zero(t, run(args, &out, &errs), "stderr %q", errs.String())
				assert.Equal(t, want.String(), out.String())
				assert.Empty(t, errs.String())
			})
		}
	}
}
