package verbflag

import (
	"bytes"
	"errors"
	"flag"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noExtra is RecoverWith's extra for a tool that adds nothing to a verb's help.
func noExtra(string) string { return "" }

// run parses args on a flag set the way a dispatcher does: RecoverWith
// deferred, the exit code and what it printed returned beside Parse's error.
func run(fs *flag.FlagSet, args []string) (out string, code int, err error) {
	var b bytes.Buffer
	func() {
		defer RecoverWith(&b, "nova-demo", "", &code, noExtra)
		err = fs.Parse(args)
	}()
	return b.String(), code, err
}

func demo() (*flag.FlagSet, *string, *bool) {
	fs := New("row add")
	label := fs.String("label", "SECRET-DEFAULT", "the row's label")
	force := fs.Bool("force", false, "write even when the row is there")
	fs.Int("width", 7, "")
	return fs, label, force
}

func TestAParseErrorIsQuietAndComesBackFromParse(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--nope"},
		{"--width", "x"},
		{"--label"},
		{"-force=maybe"},
	} {
		fs, _, _ := demo()
		out, code, err := run(fs, args)
		if err == nil || errors.Is(err, flag.ErrHelp) {
			assert.Failf(t, "assertion failed", "%v: want a parse error, got %v", args, err)
		}
		if out != "" || code != 0 {
			assert.Failf(t, "assertion failed", "%v: a parse error printed %q and set code %d; the verb prints its own refusal", args, out, code)
		}
	}
}

func TestAParseErrorThenHelpOnTheSameSetStillRaisesHelp(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	if _, _, err := run(fs, []string{"--nope"}); err == nil {
		require.Fail(t, "want a parse error first")
	}
	out, code, _ := run(fs, []string{"--help"})
	if code != 0 || !strings.HasPrefix(out, "usage: nova-demo row add [flags]\n") {
		require.Failf(t, "assertion failed", "help after an error on the same set: code %d out %q", code, out)
	}
}

func TestParseReadsFlagsAndLeavesThePositionals(t *testing.T) {
	t.Parallel()
	fs, label, force := demo()
	out, code, err := run(fs, []string{"--label", "a b", "-force", "demo", "build", "--not-a-flag"})
	if err != nil || out != "" || code != 0 {
		require.Failf(t, "assertion failed", "err %v out %q code %d", err, out, code)
	}
	if *label != "a b" || !*force || strings.Join(fs.Args(), ",") != "demo,build,--not-a-flag" {
		require.Failf(t, "assertion failed", "label %q force %v args %v", *label, *force, fs.Args())
	}
}

func TestEverySpellingOfHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	const want = "usage: nova-demo row add [flags]\n" +
		"flags:\n" +
		"  --force  write even when the row is there\n" +
		"  --label <string>  the row's label\n" +
		"  --width <int>\n" +
		"exit codes: 0 done, 1 refused, 2 usage\n"
	for _, args := range [][]string{{"-h"}, {"-help"}, {"--help"}, {"--h"}, {"--label", "x", "--help"}} {
		fs, _, _ := demo()
		out, code, _ := run(fs, args)
		if out != want || code != 0 {
			assert.Failf(t, "assertion failed", "%v: code %d\n got %q\nwant %q", args, code, out, want)
		}
	}
}

// A default can come from the environment, so a help line never prints one.
func TestHelpNeverPrintsADefault(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	out, _, _ := run(fs, []string{"--help"})
	if strings.Contains(out, "SECRET-DEFAULT") || strings.Contains(out, "7") {
		require.Failf(t, "assertion failed", "help printed a default: %q", out)
	}
}

func TestHelpAfterTheTerminatorIsAPositional(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	out, code, err := run(fs, []string{"--", "--help"})
	if err != nil || out != "" || code != 0 || strings.Join(fs.Args(), ",") != "--help" {
		require.Failf(t, "assertion failed", "err %v out %q code %d args %v", err, out, code, fs.Args())
	}
}

func TestASetThatDefinesHelpKeepsItsOwn(t *testing.T) {
	t.Parallel()
	fs := New("probe")
	own := fs.Bool("help", false, "this verb's own help flag")
	out, code, err := run(fs, []string{"--help"})
	if err != nil || out != "" || code != 0 || !*own {
		require.Failf(t, "assertion failed", "err %v out %q code %d own %v", err, out, code, *own)
	}
}

func TestASetWithNoFlagsPrintsNoFlagsHeading(t *testing.T) {
	t.Parallel()
	out, code, _ := run(New("list"), []string{"-h"})
	if out != "usage: nova-demo list [flags]\nexit codes: 0 done, 1 refused, 2 usage\n" || code != 0 {
		require.Failf(t, "assertion failed", "code %d out %q", code, out)
	}
}

func TestRecoverLetsAnyOtherPanicThrough(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	code := 0
	got := func() (r any) {
		defer func() { r = recover() }()
		func() {
			defer RecoverWith(&b, "nova-demo", "", &code, noExtra)
			panic("not help")
		}()
		return nil
	}()
	if got != "not help" || b.Len() != 0 || code != 0 {
		require.Failf(t, "assertion failed", "recovered %v, printed %q, code %d", got, b.String(), code)
	}
}

func TestRecoverWithNoPanicChangesNothing(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	code := 1
	func() { defer RecoverWith(&b, "nova-demo", "", &code, noExtra) }()
	if b.Len() != 0 || code != 1 {
		require.Failf(t, "assertion failed", "printed %q, code %d", b.String(), code)
	}
}

// pushHelp is a hand-read verb's help: each flag says what it wants.
const pushHelp = "usage: nova-demo task push [flags]\nflags:\n  --as <string>  who pushes\n  --force  push over a newer card\n" +
	"  --id <id>  the card's id\nexit codes: 0 done, 1 refused, 2 usage\n"

func TestHelpIfAskedRaisesHelpForAHandReadVerb(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a", "b"}, ""},
		{nil, ""},
		{[]string{"a", "--", "--help"}, ""},
		{[]string{"a", "-h"}, pushHelp},
		{[]string{"--help"}, pushHelp},
		{[]string{"-help", "--", "x"}, pushHelp},
		{[]string{"--h"}, pushHelp},
	} {
		var b bytes.Buffer
		code := 7
		func() {
			defer RecoverWith(&b, "nova-demo", "", &code, noExtra)
			HelpIfAsked(c.args, "task push", Flag{Name: "id", Wants: "the card's `id`"}, Flag{Name: "as", Wants: "who pushes"},
				Flag{Name: "force", Wants: "push over a newer card", Bool: true})
		}()
		wantCode := 7
		if c.want != "" {
			wantCode = 0
		}
		if b.String() != c.want || code != wantCode {
			assert.Failf(t, "assertion failed", "%v: code %d out %q, want code %d out %q", c.args, code, b.String(), wantCode, c.want)
		}
	}
}

// parseRun is run for a flag set the verb built with flag.NewFlagSet and
// parses through Parse: the shape every living tool's verbs have.
func parseRun(fs *flag.FlagSet, banner string, args []string) (out string, code int, err error) {
	var b bytes.Buffer
	code = 7
	func() {
		defer RecoverWith(&b, "nova-demo", banner, &code, noExtra)
		err = Parse(fs, args)
	}()
	return b.String(), code, err
}

const demoBanner = `nova-demo: a demo tool

usage:
  nova-demo row add --label <text> [--force]
        [--width <n>]
  nova-demo row addendum --x
  nova-demo row del <row>

exit codes: 0 done, 1 said NO,
2 could not run

example:
  nova-demo row add --label build
`

// The rule, once: -h after a verb is help on stdout at exit 0 and nothing
// else. The set the verb built writes nothing to its own output (stderr in
// the tools), whatever Usage it carries.
func TestParseTurnsHelpIntoTheVerbsHelpAtExitZero(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-h"}, {"-help"}, {"--help"}, {"--label", "x", "-h"}} {
		var stderr bytes.Buffer
		fs := flag.NewFlagSet("nova-demo row add", flag.ContinueOnError)
		fs.SetOutput(&stderr)
		fs.String("label", "SECRET-DEFAULT", "the row's label")
		ran := false
		fs.Usage = func() { ran = true; stderr.WriteString("custom usage\n") }
		out, code, _ := parseRun(fs, demoBanner, args)
		const want = "usage: nova-demo row add [flags]\n" +
			"from `nova-demo help`:\n" +
			"  nova-demo row add --label <text> [--force]\n" +
			"  [--width <n>]\n" +
			"  nova-demo row add --label build\n" +
			"flags:\n" +
			"  --label <string>  the row's label\n" +
			"exit codes: 0 done, 1 said NO,\n" +
			"2 could not run\n"
		if out != want || code != 0 {
			assert.Failf(t, "assertion failed", "%v: code %d\n got %q\nwant %q", args, code, out, want)
		}
		if stderr.Len() != 0 || ran {
			assert.Failf(t, "assertion failed", "%v: help wrote %q to the set's output (usage ran %v); help is stdout only", args, stderr.String(), ran)
		}
	}
}

// A parse error is untouched: the same error back, the same bytes on the
// set's own output, the set's own Usage called, and no help.
func TestParseLeavesAParseErrorAsItWas(t *testing.T) {
	t.Parallel()
	for _, custom := range []bool{false, true} {
		var stderr bytes.Buffer
		fs := flag.NewFlagSet("row add", flag.ContinueOnError)
		fs.SetOutput(&stderr)
		fs.Int("width", 0, "columns")
		if custom {
			fs.Usage = func() { stderr.WriteString("custom usage\n") }
		}
		out, code, err := parseRun(fs, demoBanner, []string{"--width", "x"})
		if err == nil || errors.Is(err, flag.ErrHelp) || out != "" || code != 7 {
			require.Failf(t, "assertion failed", "custom=%v: err %v out %q code %d", custom, err, out, code)
		}
		got := stderr.String()
		if !strings.HasPrefix(got, "invalid value \"x\" for flag -width") {
			assert.True(t, strings.HasPrefix(got, "invalid value \"x\" for flag -width"), "custom=%v: the flag package's message is gone: %q", custom, got)
		}
		if custom && !strings.HasSuffix(got, "custom usage\n") {
			assert.Failf(t, "assertion failed", "custom usage was not called after the error: %q", got)
		}
		if !custom && !strings.Contains(got, "-width int") {
			assert.Failf(t, "assertion failed", "the default usage was not written after the error: %q", got)
		}
	}
}

func TestParseOnASetFromNewIsStillQuietOnAnError(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	out, code, err := parseRun(fs, "", []string{"--nope"})
	if err == nil || out != "" || code != 7 {
		require.Failf(t, "assertion failed", "err %v out %q code %d", err, out, code)
	}
	out, code, _ = parseRun(fs, "", []string{"-h"})
	if code != 0 || !strings.HasPrefix(out, "usage: nova-demo row add [flags]\nflags:\n") {
		require.Failf(t, "assertion failed", "help after an error: code %d out %q", code, out)
	}
}

func TestExcerptQuotesOnlyThatVerbsLines(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb string
		want []string
	}{
		{"row add", []string{"nova-demo row add --label <text> [--force]", "[--width <n>]", "nova-demo row add --label build"}},
		{"row del", []string{"nova-demo row del <row>"}},
		{"row", []string{"nova-demo row add --label <text> [--force]", "[--width <n>]", "nova-demo row addendum --x", "nova-demo row del <row>", "nova-demo row add --label build"}},
		{"col", nil},
		{"", nil},
	} {
		got := Excerpt(demoBanner, "nova-demo", c.verb)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			assert.Equal(t, strings.Join(c.want, "|"), strings.Join(got, "|"), "%q: got %q want %q", c.verb, got, c.want)
		}
	}
}

func TestVerbTakesTheToolsNameOffTheSetsName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{"nova-demo send": "send", "send": "send", "nova-demo": "", "slots take": "slots take"} {
		if got := Verb("nova-demo", flag.NewFlagSet(name, flag.ContinueOnError)); got != want {
			assert.Equal(t, want, got, "%q: got %q want %q", name, got, want)
		}
	}
}

// A tool's codes are its own: quoted when its help states them, pointed at
// when it does not, and the family's common line only with no help text.
func TestExitCodesAreTheToolsOwn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ banner, want string }{
		{demoBanner, "exit codes: 0 done, 1 said NO,|2 could not run"},
		{"nova-demo: a demo\n\nusage:\n  nova-demo send\n", "exit codes: see `nova-demo help`"},
		{"", "exit codes: 0 done, 1 refused, 2 usage"},
		// The label is matched in any case, and inside a sentence too.
		{"nova-demo: a demo\n\nEXIT CODES: 0 ok, 1 no\n\nmore\n", "EXIT CODES: 0 ok, 1 no"},
		{"nova-demo: a demo\n\nFlags come first. Exit codes: 0 no findings, 1 findings, 2 could not\nrun (bad invocation).\n\nNext.\n", "Exit codes: 0 no findings, 1 findings, 2 could not|run (bad invocation)."},
		// A label that opens a line wins over one inside a sentence before it.
		{"nova-demo: a demo\n\nSee the exit codes: below.\n\nexit codes: 0 ok\n", "exit codes: 0 ok"},
		// The short label opens a line; inside a sentence it is not one.
		{"nova-demo: a demo\n\nexit: 0 done; 1 said NO,\n2 could not run.\n\nnext\n", "exit: 0 done; 1 said NO,|2 could not run."},
		{"nova-demo: a demo\n\non exit: nothing is left behind.\n", "exit codes: see `nova-demo help`"},
		// Prose that only mentions exits states no paragraph.
		{"nova-demo: a demo\n\nrecall exits 1 on a miss (exit 2 on usage).\n", "exit codes: see `nova-demo help`"},
	} {
		if got := strings.Join(exitCodes(c.banner, "nova-demo"), "|"); got != c.want {
			assert.Equal(t, c.want, got, "got %q want %q", got, c.want)
		}
	}
}

func TestBoolAskedReadsTheFlagBeforeTheParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"double dash", []string{"x", "--json"}, true},
		{"single dash", []string{"-json"}, true},
		{"explicit true", []string{"--json=true"}, true},
		{"explicit false", []string{"--json=false"}, false},
		{"after the terminator", []string{"--", "--json"}, false},
		{"absent", []string{"--jsonx"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := BoolAsked(tc.args, "json"); got != tc.want {
				assert.Equal(t, tc.want, got, "BoolAsked(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// RecoverWith prints the lines the tool gives for the verb above its flags, and
// Insert falls back to the exit codes, then the end, when a help has no flags.
func TestRecoverWithShowsTheToolsLinesAboveTheFlags(t *testing.T) {
	t.Parallel()
	fs := New("go")
	fs.String("to", "", "where")
	var out bytes.Buffer
	code := 9
	func() {
		defer RecoverWith(&out, "tool", "", &code, func(verb string) string { return "example:\n  tool " + verb + " --to x\n" })
		_ = Parse(fs, []string{"-h"})
	}()
	got := out.String()
	ex, fl := strings.Index(got, "example:\n  tool go --to x\n"), strings.Index(got, "flags:\n")
	if code != 0 || ex < 0 || fl < 0 || ex > fl {
		assert.Failf(t, "assertion failed", "code %d; the example is not above the flags:\n%s", code, got)
	}
	if got := Insert("usage: t\nexit codes: 0\n", "x\n"); got != "usage: t\nx\nexit codes: 0\n" {
		assert.Equal(t, "usage: t\nx\nexit codes: 0\n", got, "Insert without flags: %q", got)
	}
	if got := Insert("usage: t\n", "x\n"); got != "usage: t\nx\n" {
		assert.Equal(t, "usage: t\nx\n", got, "Insert at the end: %q", got)
	}
	if got := Insert("usage: t\n", ""); got != "usage: t\n" {
		assert.Equal(t, "usage: t\n", got, "Insert of nothing: %q", got)
	}
}

// TestExplainWordsEveryParseError pins the one wording of a flag-parse error:
// an unknown flag with the verb's flags and the nearest, a missing value and a
// bad one with what the flag wants, the value given never repeated back.
func TestExplainWordsEveryParseError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown, with the nearest", []string{"--lable", "x"}, "unknown flag --lable; the flags of row add are --force, --label, --wait, --width; did you mean --label?"},
		{"unknown, nothing near", []string{"-zzzz"}, "unknown flag --zzzz; the flags of row add are --force, --label, --wait, --width"},
		{"a bad whole number", []string{"--width", "SECRET"}, "invalid value for --width: it wants a whole number"},
		{"a bad duration, with its description", []string{"--wait", "soon"}, "invalid value for --wait: it wants a duration such as 30s or 5m (how long to wait)"},
		{"a bad bool", []string{"-force=maybe"}, "invalid value for --force: it wants true or false (write even when the row is there)"},
		{"a missing value", []string{"--label"}, "--label needs a value: it wants the row's label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, _, _ := demo()
			fs.Duration("wait", 0, "how long to wait")
			err := Parse(fs, tc.args)
			require.Error(t, err)
			assert.Equal(t, tc.want, Explain(fs, err))
			assert.NotContains(t, Explain(fs, err), "SECRET")
		})
	}
	none := New("list")
	err := Parse(none, []string{"--x"})
	require.Error(t, err)
	assert.Equal(t, "unknown flag --x; list takes no flags", Explain(none, err))
	assert.Equal(t, "something else", Explain(none, errors.New("something else")))
}

// TestBoolGivenReadsTheArgumentsAsTheFlagSetDoes pins the JSON question of a
// refusal: a value is never read as a flag, a flag after an unknown one still
// counts, and nothing after the terminator or a first argument is a flag.
func TestBoolGivenReadsTheArgumentsAsTheFlagSetDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"asked", []string{"--json"}, true},
		{"single dash", []string{"-json"}, true},
		{"explicit true", []string{"--json=true"}, true},
		{"explicit false", []string{"--json", "--json=false"}, false},
		{"the value of a flag that takes one", []string{"--store", "--json", "--nope"}, false},
		{"an inline value", []string{"--store=--json"}, false},
		{"after the terminator it is an argument", []string{"--", "--json"}, false},
		{"after the first argument it is an argument", []string{"pos", "--json"}, false},
		{"after an unknown flag it is still a flag", []string{"--bogus", "--json"}, true},
		{"after a bool flag", []string{"--force", "--json"}, true},
		{"after a flag's value", []string{"--store", "s", "--json"}, true},
		{"after an unknown flag and a word, it is unsure", []string{"--bogus", "x", "--json"}, false},
		{"an unknown flag's inline value takes no word", []string{"--bogus=x", "--json"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := New("put")
			fs.String("store", "", "the store")
			fs.Bool("force", false, "write over")
			fs.Bool("json", false, "as JSON")
			assert.Equal(t, tc.want, BoolGiven(fs, tc.args, "json"))
		})
	}
}

// TestNamesInARefusal pins the two helpers every unknown name is answered
// with: the nearest name within its edit bound, and a list cut to one line.
func TestNamesInARefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ got, want string }{
		{"--sesion", "--session"},
		{"--sessoin", "--session"},
		{"--zzzz", ""},
		{"opne", "open"},
		{"seal", ""},
	} {
		assert.Equal(t, tc.want, Nearest(tc.got, []string{"--session", "--store", "--key", "open", "put", "deny"}), tc.got)
	}
	var many []string
	for i := range ListMax + 4 {
		many = append(many, "v"+strconv.Itoa(i))
	}
	assert.True(t, strings.HasSuffix(List(many), ", v15 and 4 more"), List(many))
	assert.Equal(t, "a, b", List([]string{"a", "b"}))
}

// A synopsis replaces the placeholder. No synopsis, and a group, keep [flags],
// which is the line the other tools' help already prints.
func TestUsageLineSynopsisNamesTheRealLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "usage: nova-demo put [flags]", UsageLine("nova-demo", "put", nil))
	assert.Equal(t, "usage: nova-demo row <add|del> [flags]", UsageLineSynopsis("nova-demo", "row", []string{"add", "del"}, "<ignored>"))
	got := UsageLineSynopsis("nova-demo", "inbox", nil, "[--open <group>] [--wait]")
	assert.Equal(t, "usage: nova-demo inbox [--open <group>] [--wait]", got)
	assert.NotContains(t, got, "[flags]")

	fs := New("run")
	fs.Bool("land", false, "land what passed")
	fs.String("listen", "", "on this `address:port`")
	syn := FlagSynopsis(fs)
	assert.Equal(t, "[--land] [--listen <address:port>]", syn)
	assert.NotContains(t, syn, "[flags]")
	line := UsageLineSynopsis("nova-demo", "run", nil, syn)
	assert.Equal(t, "usage: nova-demo run [--land] [--listen <address:port>]", line)
	assert.NotContains(t, line, "[flags]")
	assert.Empty(t, FlagSynopsis(nil))
	assert.Empty(t, FlagSynopsis(New("bare")))
}

// TestAGroupsHelpNamesItsVerbs pins the group form: a verb with no flags whose
// every usage line goes on with a verb word is a group, and its help's usage
// line names its verbs; a verb with flags, or a line going on with an
// argument, is no group, and an example line is not read.
func TestAGroupsHelpNamesItsVerbs(t *testing.T) {
	t.Parallel()
	banner := demoBanner + "  nova-demo row zap\n"
	for _, tc := range []struct{ name, verb, want string }{
		{"a group", "row", "usage: nova-demo row <add|addendum|del> [flags]\n"},
		{"a verb going on with an argument", "row del", "usage: nova-demo row del [flags]\n"},
		{"a word the banner does not name", "col", "usage: nova-demo col [flags]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			Print(&b, "nova-demo", banner, New(tc.verb))
			assert.True(t, strings.HasPrefix(b.String(), tc.want), b.String())
		})
	}
	var b bytes.Buffer
	withFlags := New("row")
	withFlags.Bool("all", false, "every row")
	Print(&b, "nova-demo", banner, withFlags)
	assert.True(t, strings.HasPrefix(b.String(), "usage: nova-demo row [flags]\n"), b.String())
	assert.Equal(t, []string{"list", "show", "add"}, Subverbs("usage:\n  t m list|show <n>\n  t m add -h\n", "t", "m"))
	pasted := "usage:\n  t fleet set --x <v>\n  t fleet|sprint show\n  t version     print this build\n  t worker\tcheck <f>\n"
	assert.Equal(t, []string{"set", "show"}, Subverbs(pasted, "t", "fleet"))
	assert.Nil(t, Subverbs(pasted, "t", "version"), "a pasted description is no verb")
	assert.Nil(t, Subverbs(pasted, "t", "worker"), "a pasted description is no verb")
	assert.Nil(t, Subverbs(pasted+"  t <kind> list\n", "t", "fleet"), "a placeholder leaves the group's verbs unnamed")
	assert.Equal(t, []string{"set", "show"}, Subverbs(pasted+"t <verb> REFUSED: why\n", "t", "fleet"), "a sentence about every verb is no usage")
}

// TestAVerbsOwnExitCodesStandForTheTools pins the per-verb exit table: given
// to Print, or as an `exit codes:` line from RecoverWith's extra, it stands
// where the banner's paragraph would, and the extra's other lines stay above
// the flags.
func TestAVerbsOwnExitCodesStandForTheTools(t *testing.T) {
	t.Parallel()
	fs := New("row add")
	fs.String("label", "", "the row's label")
	var b bytes.Buffer
	Print(&b, "nova-demo", demoBanner, fs, "exit codes: 0 added, 2 could not run")
	assert.True(t, strings.HasSuffix(b.String(), "\nexit codes: 0 added, 2 could not run\n"), b.String())
	assert.NotContains(t, b.String(), "said NO")

	var out bytes.Buffer
	code := 9
	func() {
		defer RecoverWith(&out, "nova-demo", demoBanner, &code, func(string) string {
			return "effect: a local write\nexit codes: 0 added; 1 the row is there,\n  2 could not run\n"
		})
		_ = Parse(fs, []string{"-h"})
	}()
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "effect: a local write\nflags:\n")
	assert.True(t, strings.HasSuffix(out.String(), "\nexit codes: 0 added; 1 the row is there,\n  2 could not run\n"), out.String())
	assert.NotContains(t, out.String(), "said NO")
}
