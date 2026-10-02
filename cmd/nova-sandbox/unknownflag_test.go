package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bare form answers an unknown flag as bad_flag, never no_command, naming the
// flags it takes; and that list is the parser's: each flag in it parses as a flag.
func TestTheBareFormNamesAnUnknownFlagAndItsFlags(t *testing.T) {
	t.Parallel()
	bad := parse([]string{"--wrte", "./x", "--", "true"}).bad
	require.Len(t, bad, 1)
	assert.Equal(t, "bad_flag", bad[0].Reason)
	for _, f := range bareFlags {
		got := parse([]string{f, "v", "--", "true"}).bad
		for _, r := range got {
			assert.NotContains(t, r.Text, "unknown flag", "%s is in bareFlags and the parser refuses it", f)
		}
	}
}

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
		{"policy with a value", texts(parseVerb("policy", []string{"--read", "/a", "--wrte", "./x", "--write", "/b"}).bad), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"probe with a value", texts(parseVerb("probe", []string{"--wrte", "./x"}).bad), "unknown flag --wrte; run: nova-sandbox help probe"},
		{"the bare form", texts(parse([]string{"--wrte", "./x", "--"}).bad), "unknown flag --wrte; the flags are " + strings.Join(bareFlags, ", ") +
			"; did you mean --write?; run: nova-sandbox help"},
		{"a flag given as --x=y", texts(parseVerb("policy", []string{"--wrte=./x"}).bad), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"a flag with no value", texts(parseVerb("policy", []string{"--wrte", "--write", "/b"}).bad), "unknown flag --wrte; run: nova-sandbox help policy"},
		{"run", texts(parseRun([]string{"--nmae", "x", "--size", "8g"}).bad), "unknown flag --nmae; run: nova-sandbox help run"},
		{"egress", texts(parseEgress([]string{"--polcy", "p.json"}).bad), "unknown flag --polcy; run: nova-sandbox help egress"},
		{"reap", texts(parseReap([]string{"--dry-rn", "x"}).bad), "unknown flag --dry-rn; run: nova-sandbox help reap"},
	} {
		assert.Equal(t, []string{c.want}, c.got, c.name)
	}
	// a word that is no flag at all is not called one
	got := texts(parseVerb("policy", []string{"stray"}).bad)
	if assert.Len(t, got, 1) {
		assert.True(t, strings.HasPrefix(got[0], "unexpected argument stray; run: nova-sandbox help policy"), got[0])
	}
	// everything after -- is the command's, flags included
	got = texts(parse([]string{"--write", "/b", "--", "tool", "--wrte", "x"}).bad)
	assert.Empty(t, got, "the command's own flags drew refusals")
}

// texts is the text of each refusal a parser returned.
func texts(bad []sandbox.Refusal) []string {
	var out []string
	for _, r := range bad {
		out = append(out, r.Text)
	}
	return out
}

// `probe --bogus` is refused at the unknown flag alone: the lines that follow from it (the
// --write the misspelled flag was meant to be, reported missing) are not printed.
func TestProbeStopsAtAnUnknownFlag(t *testing.T) {
	t.Parallel()
	r := streams(func(args []string, stdout, stderr io.Writer) int { return probeVerb(args, stdout, stderr, nil) }).Do(t, "--bogus").Exit(2)
	assert.Equal(t, "PROBE REFUSED reason=check: unknown flag --bogus; run: nova-sandbox help probe\n", r.Stderr)
}

// `check` words an unknown flag the same way as every other verb.
func TestCheckUnknownFlagIsTheSameLine(t *testing.T) {
	t.Parallel()
	r := streams(checkVerb).Do(t, "--wrte", "./x").Exit(2)
	assert.Equal(t, "CHECK REFUSED reason=bad_flag: unknown flag --wrte; run: nova-sandbox help check\n", r.Stderr)
}
