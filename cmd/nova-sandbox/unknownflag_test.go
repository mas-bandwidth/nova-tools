package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Equal(t, []string{c.want}, c.got, "%s: refusals %q, want exactly %q", c.name, c.got, c.want)
	}
	// a word that is no flag at all is not called one
	got := badTexts(parseVerb("policy", []string{"stray"}))
	if assert.Len(t, got, 1, "a stray word draws %q", got) {
		assert.True(t, strings.HasPrefix(got[0], "unexpected argument stray; run: nova-sandbox help policy"), "a stray word draws %q", got)
	}
	// everything after -- is the command's, flags included
	got = badTexts(parse([]string{"--write", "/b", "--", "tool", "--wrte", "x"}))
	assert.Empty(t, got, "the command's own flags draw %q", got)
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

// `probe --bogus` is refused at the unknown flag alone: the lines that follow from it (the
// --write the misspelled flag was meant to be, reported missing) are not printed.
func TestProbeStopsAtAnUnknownFlag(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := probeVerb([]string{"--bogus"}, &out, &errb, nil)
	require.Equal(t, 2, code, "exit %d, want 2", code)
	got, want := errb.String(), "PROBE REFUSED reason=check: unknown flag --bogus; run: nova-sandbox help probe\n"
	assert.Equal(t, want, got, "probe --bogus printed %q, want exactly %q", got, want)
}

// `check` words an unknown flag the same way as every other verb.
func TestCheckUnknownFlagIsTheSameLine(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := checkVerb([]string{"--wrte", "./x"}, &out, &errb)
	require.Equal(t, 2, code, "exit %d, want 2", code)
	got, want := errb.String(), "CHECK REFUSED reason=bad_flag: unknown flag --wrte; run: nova-sandbox help check\n"
	assert.Equal(t, want, got, "check --wrte printed %q, want %q", got, want)
}
