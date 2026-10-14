package onboardtest_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/onboardtest"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake is a command that meets the onboarding standard, one knob per check:
// every field holds the standard value in standard(), and a row turns the knob
// of the check it breaks. No knob, no way to break a check the module does not
// hold.
type fake struct {
	bare                   int
	bareStdout, bareStderr string
	help                   int
	banner, helpStderr     string
	verb                   int
	verbStdout, verbStderr string
	run                    int
}

// stdBanner is the banner of the standard fake: line 1 says what the tool
// does in one sentence, and the `example:` block holds three command lines.
const stdBanner = `nova-x: lists and shows the fleet's cards

example:
nova-x list
nova-x show
nova-x list
`

// standard is the fake as the onboarding standard wants it.
func standard() fake {
	return fake{
		bare:       2,
		bareStderr: "nova-x REFUSED: no verb; run: nova-x help\n",
		banner:     stdBanner,
		verbStdout: "usage: nova-x list [flags]\n",
	}
}

// Main is the fake as a testkit.Main: no verb refuses, `help` prints the
// banner, -h and --help print the verb's usage, an example line runs.
func (f fake) Main(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	say := func(code int, out, err string) int {
		fmt.Fprint(stdout, out)
		fmt.Fprint(stderr, err)
		return code
	}
	switch {
	case len(args) == 0:
		return say(f.bare, f.bareStdout, f.bareStderr)
	case args[0] == "help":
		return say(f.help, f.banner, f.helpStderr)
	case args[len(args)-1] == "-h", args[len(args)-1] == "--help":
		return say(f.verb, f.verbStdout, f.verbStderr)
	default:
		return say(f.run, "", "")
	}
}

// recorder is a testing.TB that writes every problem down instead of failing:
// a row reads its broken check back from it. The real *testing.T it embeds
// still answers Helper, Name and TempDir.
type recorder struct {
	*testing.T
	problems []string
}

func (r *recorder) Errorf(format string, a ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, a...))
}
func (r *recorder) Error(a ...any) { r.problems = append(r.problems, fmt.Sprint(a...)) }

// TestOnboardtestReportsEachBrokenCheck pins that every check names itself
// when the tool breaks it: one row per check, each observed through a
// recording testing.TB fake, and the tool that meets the standard keeps it
// quiet. assert inside the rows, so one bad row does not hide the rest.
func TestOnboardtestReportsEachBrokenCheck(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		f    fake
		want string
	}{
		{"a tool that meets the standard", standard(), ""},
		{"the bare command accepts", fake{bare: 1, bareStderr: "no\n", verbStdout: "usage: nova-x list [flags]\n", banner: stdBanner, helpStderr: ""}, "a bare `nova-x` exits 1, want 2"},
		{"the bare refusal names no door", fake{bare: 2, bareStderr: "not today\n", verbStdout: "usage: nova-x list [flags]\n", banner: stdBanner}, "names no door"},
		{"the bare refusal prints its banner", fake{bare: 2, bareStderr: "nova-x\nREFUSED: no verb\nrun: nova-x help\n", banner: stdBanner, verbStdout: "usage: nova-x list [flags]\n"}, "printed 3 lines, want 1"},
		{"help exits nonzero", fake{bare: 2, bareStderr: "nova-x REFUSED: no verb; run: nova-x help\n", help: 1, banner: stdBanner, verbStdout: "usage: nova-x list [flags]\n"}, "`nova-x help` exits 1, want 0"},
		{"help prints no banner", fake{bare: 2, bareStderr: "nova-x REFUSED: no verb; run: nova-x help\n", banner: "", verbStdout: "usage: nova-x list [flags]\n"}, "printed no banner"},
		{"the banner's first line is a usage line", fake{bare: 2, bareStderr: "nova-x REFUSED: no verb; run: nova-x help\n", banner: "usage: nova-x list [flags]\n", verbStdout: "usage: nova-x list [flags]\n"}, "line 1 of `nova-x help`"},
		{"a verb answers -h at exit 1", func() fake { f := standard(); f.verb = 1; return f }(), "exits 1, want 0"},
		{"a verb's help writes to stderr", func() fake { f := standard(); f.verbStderr = "boom\n"; return f }(), "wrote to stderr"},
		{"a verb's help prints no usage line", func() fake { f := standard(); f.verbStdout = "list of things\n"; return f }(), "prints no usage line"},
		{"the banner carries no example block", func() fake { f := standard(); f.banner = "nova-x: lists and shows the fleet's cards\n"; return f }(), "no `example:` block"},
		{"an example line carries a placeholder", func() fake {
			f := standard()
			f.banner = "nova-x: lists and shows the fleet's cards\n\nexample:\nnova-x list <dir>\n"
			return f
		}(), "still carries a placeholder"},
		{"an example line exits 2", func() fake { f := standard(); f.run = 2; return f }(), "want 0 or 1"},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{T: t}
			onboardtest.Hold(rec, row.f.Main, onboardtest.Spec{Name: "nova-x", Verbs: []string{"list", "show"}})
			said := strings.Join(rec.problems, "\n")
			if row.want == "" {
				assert.Empty(t, rec.problems, "a tool that meets the standard is reported:\n%s", said)
				return
			}
			assert.Contains(t, said, row.want, "the broken check is not named; reported:\n%s", said)
		})
	}
}

// TestOnboardtestCheckIsQuietForTheStandardTool pins Check's own shape: every
// check runs as its own subtest, and a tool that meets the standard fails none
// of them. Hold's rows above hold the checks; this holds the wiring.
func TestOnboardtestCheckIsQuietForTheStandardTool(t *testing.T) {
	t.Parallel()
	onboardtest.Check(t, standard().Main, onboardtest.Spec{Name: "nova-x", Verbs: []string{"list", "show"}})
}

// TestOnboardtestVerbHelpRunsEveryVerbAndBothSpellings pins the verb-help leg
// a caller switches onto: both -h and --help, every verb, and a broken verb
// named through a recording testing.TB.
func TestOnboardtestVerbHelpRunsEveryVerbAndBothSpellings(t *testing.T) {
	t.Parallel()
	calls := map[string]int{}
	m := testkit.Main(func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		calls[strings.Join(args, " ")]++
		return standard().Main(args, nil, stdout, stderr)
	})
	rec := &recorder{T: t}
	onboardtest.VerbHelp(rec, m, onboardtest.Spec{Name: "nova-x", Verbs: []string{"list", "show"}})
	assert.Empty(t, rec.problems, "a tool that meets the standard is reported:\n%s", strings.Join(rec.problems, "\n"))
	for _, verb := range []string{"list", "show"} {
		for _, spelling := range []string{"-h", "--help"} {
			require.Equal(t, 1, calls[verb+" "+spelling], "the %s leg of %s ran once", spelling, verb)
		}
	}
}

// Example shows the module's report for a tool whose bare command accepts:
// every check runs, and the broken one is named.
func Example() {
	f := standard()
	f.bare = 0
	f.bareStderr = ""
	for _, p := range onboardtest.Problems(f.Main, onboardtest.Spec{Name: "nova-x", Verbs: []string{"list", "show"}}) {
		fmt.Println(p)
	}
	// Output:
	// a bare `nova-x` exits 0, want 2 (could not run -- no arguments is not an invocation); stdout "" stderr ""
	// a bare `nova-x` names no door; the refusal must carry "run: nova-x help"; stderr:
}
