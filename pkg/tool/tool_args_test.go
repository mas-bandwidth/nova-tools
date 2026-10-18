package tool

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argsTool has one verb that takes positional arguments after its flags
// (Verb.Args) and one that takes none, and a version line with extras.
func argsTool() *Tool {
	return &Tool{
		Name:          "nova-args",
		What:          "joins files",
		ExitTable:     "0 done, 1 said no, 2 could not run.",
		VersionExtras: func() []string { return []string{"runner=abc123"} },
		Verbs: []Verb{
			{
				Name: "join", Usage: "join [--sep <s>] <file>...", Effect: Inspection, Args: true,
				Flags: func(f *Flags) { f.String("sep", ",", "the separator") },
				Run:   func(c *Call) *Out { return Done().Fact("files", strings.Join(c.Args(), c.Str("sep"))) },
			},
			{
				Name: "count", Usage: "count", Effect: Inspection,
				Run: func(c *Call) *Out { return Done().Fact("args", len(c.Args())) },
			},
		},
	}
}

func runArgs(args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := argsTool().Run(args, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

// TestAVerbThatTakesArgumentsReadsThemAfterItsFlags pins Verb.Args: the words
// after the flags reach the verb through Call.Args, a flag after one is refused
// by name unless a literal -- came first, and a verb without Args still refuses
// any argument.
func TestAVerbThatTakesArgumentsReadsThemAfterItsFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"its arguments", []string{"join", "--sep", "+", "a", "b"}, 0, "JOIN OK files=a+b"},
		{"none at all", []string{"join"}, 0, "JOIN OK files=-"},
		{"a late flag", []string{"join", "a", "--sep", "+"}, 2, `JOIN REFUSED: flag "--sep" comes after a positional argument`},
		{"a dash after a literal --", []string{"join", "--", "a", "-b"}, 0, "JOIN OK files=a,-b"},
		{"a verb without Args", []string{"count", "x"}, 2, `COUNT REFUSED: takes no positional arguments, got "x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, errs := runArgs(tc.args...)
			assert.Equal(t, tc.code, code, "stdout %q stderr %q", out, errs)
			assert.Contains(t, out+errs, tc.want)
		})
	}
}

// TestVersionExtrasRideOnTheVersionLine pins Tool.VersionExtras: the version
// verb's one line carries the extras after the build identity.
func TestVersionExtrasRideOnTheVersionLine(t *testing.T) {
	t.Parallel()
	code, out, errs := runArgs("version")
	require.Equal(t, 0, code, errs)
	assert.True(t, strings.HasPrefix(out, "nova-args "), out)
	assert.True(t, strings.HasSuffix(out, " runner=abc123\n"), out)
}
