package verbflag

import (
	"bytes"
	"strings"
	"testing"
)

// TestRetiredSpellingNamesTheNewOne: a retired spelling is refused naming the
// one it is spelled now (#4352 A), never kept as an alias.
func TestRetiredSpellingNamesTheNewOne(t *testing.T) {
	t.Parallel()
	fs := New("card work")
	_ = fs.String("as", "", "the worker")
	err := fs.Parse([]string{"--actor", "friend:rowan", "--n=2"})
	want := "--actor is spelled --as; run: nova-sprint card work --as friend:rowan --n=2"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v; want %s", err, want)
	}
	if fs.Lookup("actor") != nil {
		t.Fatal("--actor is still defined: an alias, not a refusal")
	}
}

// TestRetiredSpellingWithoutTheNewOneSaysRetired: a verb that does not take
// the new spelling says the old one is retired and where the flags are.
func TestRetiredSpellingWithoutTheNewOneSaysRetired(t *testing.T) {
	t.Parallel()
	fs := New("stream ls")
	err := fs.Parse([]string{"--by", "x"})
	want := "--by is retired and nova-sprint stream ls has no --as; it takes no flags"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}

// TestUnknownFlagIsTheGrammarsRefusal (#4399 item 6): a flag nobody ever
// spelled is refused naming the flags the verb takes, never with the flag
// package's own "flag provided but not defined".
func TestUnknownFlagIsTheGrammarsRefusal(t *testing.T) {
	t.Parallel()
	fs := New("reconcile")
	_ = fs.Bool("once", false, "one pass")
	_ = fs.String("host", "", "this host")
	err := fs.Parse([]string{"--sprint", "s1", "--once"})
	want := "--sprint is not a flag of nova-sprint reconcile; it takes --host and --once; without it: nova-sprint reconcile --once"
	if err == nil || err.Error() != want || strings.Contains(err.Error(), "provided but not defined") {
		t.Fatalf("got %v; want %s", err, want)
	}
}

// TestSpelledIsThisVerbsRetiredSpelling (#4399 item 3): task ls --state is
// spelled --where on that verb alone, refused with the whole line.
func TestSpelledIsThisVerbsRetiredSpelling(t *testing.T) {
	t.Parallel()
	fs := New("task ls")
	_ = fs.String("where", "", "a column")
	_ = fs.String("stream", "", HelpStream)
	fs.Spelled("state", "where")
	err := fs.Parse([]string{"--stream", "probe a", "--state=ready"})
	want := "--state is spelled --where; run: nova-sprint task ls --stream 'probe a' --where=ready"
	if err == nil || err.Error() != want || fs.Lookup("state") != nil {
		t.Fatalf("got %v; want %s", err, want)
	}
}

// TestJoinQuotesWhatAShellWouldSplit: the corrected line runs verbatim.
func TestJoinQuotesWhatAShellWouldSplit(t *testing.T) {
	t.Parallel()
	got := Join([]string{"--why", "it's red", "--ids", "a~1", "--ref", "nova-tools#1", "--x", ""})
	want := `--why 'it'\''s red' --ids a~1 --ref nova-tools#1 --x ''`
	if got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

// TestEveryRetiredSpellingHasACurrentOneThatIsNotRetired: the table maps to
// current spellings only, never to another retired one.
func TestEveryRetiredSpellingHasACurrentOneThatIsNotRetired(t *testing.T) {
	t.Parallel()
	for old, now := range Retired {
		if _, again := Retired[now]; again {
			t.Errorf("--%s is spelled --%s, which is itself retired", old, now)
		}
		if old == now || old == "" || now == "" {
			t.Errorf("bad row %q -> %q", old, now)
		}
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	got := List(" a, b,,c ")
	if strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("got %v", got)
	}
	if List("") != nil {
		t.Fatal("empty is nil")
	}
}

// TestWriteHelpNamesTheFormsEveryFlagAndTheExamples: -h prints the path's
// forms from the one table, one line per flag with its one-line help, and
// the path's examples.
func TestWriteHelpNamesTheFormsEveryFlagAndTheExamples(t *testing.T) {
	t.Parallel()
	fs := New("card deal")
	_ = fs.String("to", "", "the target worker, friend:<f> or bench:<b>")
	_ = fs.Int("n", 0, "how many")
	var b bytes.Buffer
	WriteHelp(&b, "card deal", fs.FlagSet)
	out := b.String()
	for _, want := range []string{"usage: nova-sprint card deal --ids <primary> --to <worker> [--n <k>]\n", "  --n <int>  how many\n",
		"  --to <string>  the target worker, friend:<f> or bench:<b>\n", "example:\n  nova-sprint card deal --ids nova-tools-4352 --to friend:emma\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestNNamesThePROnAVerbThatTakesPR (nova-tools#4352 A): the coordinator's
// `ci status --repo nova-tools --n 4371` was refused "-n not defined" on a
// verb that takes --pr; --n is the PR there, in every spelling, and a verb
// whose --n counts keeps its own meaning, as does anything after "--".
func TestNNamesThePROnAVerbThatTakesPR(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--n", "4371"}, {"-n", "4371"}, {"--n=4371"}, {"-n=4371"}, {"--pr", "4371"}} {
		fs := New("ci status")
		pr := fs.Int("pr", 0, HelpPR)
		if err := fs.Parse(args); err != nil || *pr != 4371 {
			t.Fatalf("%v: pr=%d err=%v; want 4371", args, *pr, err)
		}
	}
	fs := New("card deal")
	pr := fs.String("pr", "", HelpPR)
	n := fs.Int("n", 0, HelpN)
	if err := fs.Parse([]string{"--n", "3"}); err != nil || *n != 3 || *pr != "" {
		t.Fatalf("a verb that counts with --n: n=%d pr=%q err=%v", *n, *pr, err)
	}
	fs = New("redis-cli")
	pr = fs.String("pr", "", HelpPR)
	if err := fs.Parse([]string{"--pr", "1", "--", "-n", "2"}); err != nil || *pr != "1" || strings.Join(fs.Args(), " ") != "-n 2" {
		t.Fatalf("after --: pr=%q args=%v err=%v", *pr, fs.Args(), err)
	}
	fs = New("width")
	if err := fs.Parse([]string{"--n", "1"}); err == nil || !strings.Contains(err.Error(), "--n is not a flag of nova-sprint width") {
		t.Fatalf("a verb with neither --pr nor --n: %v", err)
	}
}

// TestNounHelpListsEverySubverbAndNoWall: a noun's -h is its subverbs'
// forms and one example each, never another noun's.
func TestNounHelpListsEverySubverbAndNoWall(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	WriteHelp(&b, "stream", nil)
	out := b.String()
	for _, want := range []string{"usage: nova-sprint stream <close|ls|open|order|pr|rebase|rename|status> [flags]\n",
		"  nova-sprint stream open --repo <owner/repo> --stream <s,...> [--base dev] [--dry-run]\n", "example:\n  nova-sprint stream close "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "land") || strings.Contains(out, "nova-sprint help") {
		t.Errorf("another verb's usage, or the help tail:\n%s", out)
	}
}

// TestRefusalEndsInThePathsOwnUsage (#4399): a refusal ends in its path's
// forms, a Final one (a corrected line) in that line, never the help tail.
func TestRefusalEndsInThePathsOwnUsage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ verb, what, want string }{
		{"task ls", "--where is required", "nova-sprint task ls: --where is required; usage: nova-sprint task ls [--stream <s> | --as friend:<f>] [--where <w>]"},
		{"ready --why", "p1 is not a card", "nova-sprint ready --why: p1 is not a card; usage: nova-sprint ready [--sprint <S>] [--why <id>]"},
		{"read brief", "--id is spelled --ids; run: nova-sprint read brief --ids p1", "nova-sprint read brief: --id is spelled --ids; run: nova-sprint read brief --ids p1"},
		{"", "no verb", "nova-sprint: no verb"},
	} {
		if got := Refusal(c.verb, c.what); got != c.want || strings.Contains(got, "nova-sprint help") {
			t.Errorf("Refusal(%q, %q)\n got %s\nwant %s", c.verb, c.what, got, c.want)
		}
	}
}
