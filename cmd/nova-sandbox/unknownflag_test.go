package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
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
		{"reap", texts(parseReap([]string{"--dry-rn", "x"}).bad), "unknown flag --dry-rn; the flags of reap are --dry-run; did you mean --dry-run?; run: nova-sandbox help reap"},
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
	r := testkit.Streams(func(args []string, stdout, stderr io.Writer) int { return probeVerb(args, stdout, stderr, nil) }).Do(t, "--bogus").Exit(2)
	assert.Equal(t, "PROBE REFUSED reason=check: unknown flag --bogus; run: nova-sandbox help probe\n", r.Stderr)
}

// `check` words an unknown flag the same way as every other verb.
func TestCheckUnknownFlagIsTheSameLine(t *testing.T) {
	t.Parallel()
	r := testkit.Streams(checkVerb).Do(t, "--wrte", "./x").Exit(2)
	assert.Equal(t, "CHECK REFUSED reason=bad_flag: unknown flag --wrte; run: nova-sandbox help check\n", r.Stderr)
}

// Every switch the hand-read verbs take reads as Go's flag package reads one, the way
// reap's --dry-run and every skeleton tool's switches do: bare, -name, =true and =false,
// and a value that is no boolean refused with what the switch wants. The parse alone:
// nothing here runs a command, a probe or a forge.
func TestEverySwitchTakesGosBooleanForms(t *testing.T) {
	t.Parallel()
	type got struct {
		on  bool
		bad []string
	}
	read := map[string]func(args ...string) got{
		"--net-deny": func(a ...string) got {
			f := parseVerb("policy", append([]string{"--write", "/b"}, a...))
			return got{f.netDeny, texts(f.bad)}
		},
		"--net-listen": func(a ...string) got {
			f := parseVerb("policy", append([]string{"--write", "/b"}, a...))
			return got{f.netListen, texts(f.bad)}
		},
		"--json": func(a ...string) got { f := parseVerb("probe", a); return got{f.json, texts(f.bad)} },
		"--go":   func(a ...string) got { f := parseRun(a); return got{f.useGo, texts(f.bad)} },
		"--prune": func(a ...string) got {
			f := parseWorktree(a)
			return got{f.prune, f.unknown}
		},
	}
	for name, parse := range read {
		bare := strings.TrimPrefix(name, "--")
		for _, c := range []struct {
			arg string
			on  bool
			bad bool
		}{
			{name, true, false},
			{"-" + bare, true, false},
			{name + "=true", true, false},
			{name + "=1", true, false},
			{name + "=false", false, false},
			{name + "=maybe", false, true},
		} {
			t.Run(c.arg, func(t *testing.T) {
				t.Parallel()
				g := parse(c.arg)
				assert.Equal(t, c.on, g.on, "%s: %+v", c.arg, g)
				if !c.bad {
					assert.Empty(t, g.bad, "%s refused: %+v", c.arg, g)
					return
				}
				if assert.Len(t, g.bad, 1, "%s: %+v", c.arg, g) {
					assert.True(t, strings.HasPrefix(g.bad[0], name+" wants true or false, got maybe"), "%s: %q", c.arg, g.bad[0])
				}
			})
		}
	}
	// check reads --json from the argv it parses: a refusal asked for as --json=true is JSON
	r := testkit.Streams(checkVerb).Do(t, "--json=true", "--bogus").Exit(2)
	assert.True(t, strings.HasPrefix(r.Stdout, `{"result":{"verb":"check","status":"refused"`), "check --json=true refused in lines: %s", r)
}
