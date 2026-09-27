package verbflag

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

// run parses args on a flag set the way a dispatcher does: Recover deferred,
// the exit code and what Recover printed returned beside Parse's error.
func run(fs *flag.FlagSet, args []string) (out string, code int, err error) {
	var b bytes.Buffer
	func() {
		defer Recover(&b, "nova-demo", &code)
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
			t.Errorf("%v: want a parse error, got %v", args, err)
		}
		if out != "" || code != 0 {
			t.Errorf("%v: a parse error printed %q and set code %d; the verb prints its own refusal", args, out, code)
		}
	}
}

func TestAParseErrorThenHelpOnTheSameSetStillRaisesHelp(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	if _, _, err := run(fs, []string{"--nope"}); err == nil {
		t.Fatal("want a parse error first")
	}
	out, code, _ := run(fs, []string{"--help"})
	if code != 2 || !strings.HasPrefix(out, "usage: nova-demo row add [flags]\n") {
		t.Fatalf("help after an error on the same set: code %d out %q", code, out)
	}
}

func TestParseReadsFlagsAndLeavesThePositionals(t *testing.T) {
	t.Parallel()
	fs, label, force := demo()
	out, code, err := run(fs, []string{"--label", "a b", "-force", "demo", "build", "--not-a-flag"})
	if err != nil || out != "" || code != 0 {
		t.Fatalf("err %v out %q code %d", err, out, code)
	}
	if *label != "a b" || !*force || strings.Join(fs.Args(), ",") != "demo,build,--not-a-flag" {
		t.Fatalf("label %q force %v args %v", *label, *force, fs.Args())
	}
}

func TestEverySpellingOfHelpPrintsTheUsageAndExitsTwo(t *testing.T) {
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
		if out != want || code != 2 {
			t.Errorf("%v: code %d\n got %q\nwant %q", args, code, out, want)
		}
	}
}

// A default can come from the environment, so a help line never prints one.
func TestHelpNeverPrintsADefault(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	out, _, _ := run(fs, []string{"--help"})
	if strings.Contains(out, "SECRET-DEFAULT") || strings.Contains(out, "7") {
		t.Fatalf("help printed a default: %q", out)
	}
}

func TestHelpAfterTheTerminatorIsAPositional(t *testing.T) {
	t.Parallel()
	fs, _, _ := demo()
	out, code, err := run(fs, []string{"--", "--help"})
	if err != nil || out != "" || code != 0 || strings.Join(fs.Args(), ",") != "--help" {
		t.Fatalf("err %v out %q code %d args %v", err, out, code, fs.Args())
	}
}

func TestASetThatDefinesHelpKeepsItsOwn(t *testing.T) {
	t.Parallel()
	fs := New("probe")
	own := fs.Bool("help", false, "this verb's own help flag")
	out, code, err := run(fs, []string{"--help"})
	if err != nil || out != "" || code != 0 || !*own {
		t.Fatalf("err %v out %q code %d own %v", err, out, code, *own)
	}
}

func TestASetWithNoFlagsPrintsNoFlagsHeading(t *testing.T) {
	t.Parallel()
	out, code, _ := run(New("list"), []string{"-h"})
	if out != "usage: nova-demo list [flags]\nexit codes: 0 done, 1 refused, 2 usage\n" || code != 2 {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestRecoverLetsAnyOtherPanicThrough(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	code := 0
	got := func() (r any) {
		defer func() { r = recover() }()
		func() {
			defer Recover(&b, "nova-demo", &code)
			panic("not help")
		}()
		return nil
	}()
	if got != "not help" || b.Len() != 0 || code != 0 {
		t.Fatalf("recovered %v, printed %q, code %d", got, b.String(), code)
	}
}

func TestRecoverWithNoPanicChangesNothing(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	code := 1
	func() { defer Recover(&b, "nova-demo", &code) }()
	if b.Len() != 0 || code != 1 {
		t.Fatalf("printed %q, code %d", b.String(), code)
	}
}

func TestHelpIfAskedRaisesHelpForAHandReadVerb(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a", "b"}, ""},
		{nil, ""},
		{[]string{"a", "--", "--help"}, ""},
		{[]string{"a", "-h"}, "usage: nova-demo task push [flags]\nflags:\n  --as <string>\n  --id <string>\nexit codes: 0 done, 1 refused, 2 usage\n"},
		{[]string{"--help"}, "usage: nova-demo task push [flags]\nflags:\n  --as <string>\n  --id <string>\nexit codes: 0 done, 1 refused, 2 usage\n"},
		{[]string{"-help", "--", "x"}, "usage: nova-demo task push [flags]\nflags:\n  --as <string>\n  --id <string>\nexit codes: 0 done, 1 refused, 2 usage\n"},
		{[]string{"--h"}, "usage: nova-demo task push [flags]\nflags:\n  --as <string>\n  --id <string>\nexit codes: 0 done, 1 refused, 2 usage\n"},
	} {
		var b bytes.Buffer
		code := 0
		func() {
			defer Recover(&b, "nova-demo", &code)
			HelpIfAsked(c.args, "task push", "id", "as")
		}()
		wantCode := 0
		if c.want != "" {
			wantCode = 2
		}
		if b.String() != c.want || code != wantCode {
			t.Errorf("%v: code %d out %q, want code %d out %q", c.args, code, b.String(), wantCode, c.want)
		}
	}
}
