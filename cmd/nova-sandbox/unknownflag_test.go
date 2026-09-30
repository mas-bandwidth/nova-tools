package main

import (
	"strings"
	"testing"
)

// A misspelled flag is one refusal line, `unknown flag --x; run: nova-sandbox help <verb>`,
// and the value it was given is not a second mistake: `--wrte ./x` used to draw a second
// refusal that called ./x a flag.
func TestMisspelledFlagIsOneCleanLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		got  []string // the refusals' texts
		want string
	}{
		{"policy with a value", badTexts(parseVerb("policy", []string{"--read", "/a", "--wrte", "./x", "--write", "/b"})), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"probe with a value", badTexts(parseVerb("probe", []string{"--wrte", "./x"})), "unknown flag --wrte; run: nova-sandbox help probe"},
		{"the bare form", badTexts(parse([]string{"--wrte", "./x", "--"})), "unknown flag --wrte; run: nova-sandbox help"},
		{"a flag given as --x=y", badTexts(parseVerb("policy", []string{"--wrte=./x"})), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"a flag with no value", badTexts(parseVerb("policy", []string{"--wrte", "--write", "/b"})), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"run", runBad(parseRun([]string{"--nmae", "x", "--size", "8g"})), "unknown flag --nmae; run: nova-sandbox help run"},
		{"egress", egressBad(parseEgress([]string{"--polcy", "p.json"})), "unknown flag --polcy; run: nova-sandbox help egress"},
		{"reap", reapBad(parseReap([]string{"--dry-rn", "x"})), "unknown flag --dry-rn; run: nova-sandbox help reap"},
	} {
		if len(c.got) != 1 || c.got[0] != c.want {
			t.Errorf("%s: refusals %q, want exactly %q", c.name, c.got, c.want)
		}
	}
	// a word that is no flag at all is not called one
	if got := badTexts(parseVerb("policy", []string{"stray"})); len(got) != 1 || !strings.HasPrefix(got[0], "unexpected argument stray; run: nova-sandbox help policy") {
		t.Errorf("a stray word draws %q", got)
	}
	// everything after -- is the command's, flags included
	if got := badTexts(parse([]string{"--write", "/b", "--", "tool", "--wrte", "x"})); len(got) != 0 {
		t.Errorf("the command's own flags draw %q", got)
	}
}

func badTexts(f flags) []string {
	var out []string
	for _, r := range f.bad {
		out = append(out, r.Text)
	}
	return out
}

func runBad(f runFlags) []string {
	var out []string
	for _, r := range f.bad {
		out = append(out, r.Text)
	}
	return out
}

func egressBad(f egressFlags) []string {
	var out []string
	for _, r := range f.bad {
		out = append(out, r.Text)
	}
	return out
}

func reapBad(f reapFlags) []string {
	var out []string
	for _, r := range f.bad {
		out = append(out, r.Text)
	}
	return out
}
