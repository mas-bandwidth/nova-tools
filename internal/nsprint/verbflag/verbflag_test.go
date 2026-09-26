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
	err := fs.Parse([]string{"--actor", "friend:rowan"})
	if err == nil || err.Error() != "--actor is spelled --as" {
		t.Fatalf("got %v; want --actor is spelled --as", err)
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
	want := "--by is retired; nova-sprint stream ls -h lists the flags"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}

// TestUnknownFlagStaysTheFlagPackagesError: a flag nobody ever spelled is the
// flag package's own message.
func TestUnknownFlagStaysTheFlagPackagesError(t *testing.T) {
	t.Parallel()
	fs := New("x")
	err := fs.Parse([]string{"--nope"})
	if err == nil || !strings.Contains(err.Error(), "not defined: -nope") {
		t.Fatalf("got %v", err)
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

// TestPrintNamesEveryFlagWithItsHelp: -h prints one line per flag with its
// one-line help.
func TestPrintNamesEveryFlagWithItsHelp(t *testing.T) {
	t.Parallel()
	fs := New("card deal")
	_ = fs.String("to", "", "the target worker, friend:<f> or bench:<b>")
	_ = fs.Int("n", 0, "how many")
	var b bytes.Buffer
	Print(&b, "nova-sprint", fs.FlagSet)
	out := b.String()
	for _, want := range []string{"usage: nova-sprint card deal [flags]\n", "  --n <int>  how many\n", "  --to <string>  the target worker, friend:<f> or bench:<b>\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
