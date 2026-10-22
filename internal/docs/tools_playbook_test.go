package docs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
)

// tools_playbook_test.go holds docs/TOOLS-PLAYBOOK.md to the tools it names, as
// cli_generated_functional_test.go holds docs/CLI.md:
// tools_playbook_functional_test.go builds each nova-tools tool the page names
// and asks the binaries themselves, so the page cannot go stale. This file is
// the checker, and its unit test holds it to canned help.
// Every usage line the page quotes is in the tool's help (`<tool> help` or
// `<tool> <verb> -h`, compared with runs of spaces folded), every command it
// names runs a verb whose `-h` exits 0, and every flag it gives is one that
// verb's help names. nova-card, nova-work and nova-sprint are built in
// nova-sprint, whose own playbook test holds their lines there.

// builtElsewhere is the tools the page names that this repository does not
// build.
var builtElsewhere = []string{"nova-card", "nova-work", "nova-sprint"}

// playbookSpanRe reads the inline code spans of the page.
var playbookSpanRe = regexp.MustCompile("`([^`]+)`")

// playbookUsageRe reads a quoted usage line: the word usage, then the line in
// a code span, across a line break where the page wraps.
var playbookUsageRe = regexp.MustCompile("usage:?\\s+`(nova-[a-z-]+ [^`]+)`")

// verbWordRe is a verb's word: lower case, no placeholder, no flag.
var verbWordRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// fold collapses every run of white space to one space, so a usage line the
// help wraps over two lines reads as the line the page quotes.
func fold(s string) string { return strings.Join(strings.Fields(s), " ") }

// playbookHelp runs one built tool with args and returns its output and exit
// code; the cache keeps each distinct call to one run. -h prints and runs
// nothing (docs/CLI.md), and the run gets a clean environment and its own HOME.
type playbookHelp struct {
	bin, home string
	cache     map[string]helpRun
}

type helpRun struct {
	out  string
	code int
}

func (h *playbookHelp) run(t *testing.T, tool string, args ...string) helpRun {
	t.Helper()
	key := tool + " " + strings.Join(args, " ")
	if r, ok := h.cache[key]; ok {
		return r
	}
	require.NotEmpty(t, h.bin, "no canned help for %q and no built tools to ask", key)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(h.bin, tool), args...)
	cmd.Dir = h.home
	env := []string{"HOME=" + h.home}
	for _, kv := range goenv.Clean(os.Environ()) {
		if strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	r := helpRun{out: string(out)}
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "%s: %v", key, err)
		r.code = exit.ExitCode()
	}
	h.cache[key] = r
	return r
}

// verbWords is the words of the verb a command names, up to two (nova-config
// friend set, nova-update release cut); nil when the command names none.
func verbWords(words []string) []string {
	var verb []string
	for _, w := range words[1:] {
		if len(verb) == 2 || !verbWordRe.MatchString(w) {
			break
		}
		verb = append(verb, w)
	}
	return verb
}

// checkToolsPlaybook is every problem of the page's text, asking the built
// tools under h.
func checkToolsPlaybook(t *testing.T, h *playbookHelp, text string) []string {
	t.Helper()
	var problems []string
	usages := 0
	for _, m := range playbookUsageRe.FindAllStringSubmatch(text, -1) {
		words := strings.Fields(m[1])
		if slices.Contains(builtElsewhere, words[0]) {
			continue
		}
		usages++
		hay := fold(h.run(t, words[0], "help").out)
		if v := verbWords(words); len(v) > 0 {
			hay += " " + fold(h.run(t, words[0], append(v, "-h")...).out)
			hay += " " + fold(h.run(t, words[0], append([]string{"help"}, v...)...).out)
		}
		if !strings.Contains(hay, fold(m[1])) {
			problems = append(problems, "usage line not in "+words[0]+"'s help: "+m[1])
		}
	}
	if usages == 0 {
		problems = append(problems, "the page quotes no usage line of a tool built here")
	}
	for _, s := range playbookSpanRe.FindAllStringSubmatch(text, -1) {
		words := strings.Fields(s[1])
		if len(words) < 2 || !strings.HasPrefix(words[0], "nova-") || slices.Contains(builtElsewhere, words[0]) {
			continue
		}
		if words[1] == "help" || strings.HasPrefix(words[1], "<") || strings.HasPrefix(words[1], "[") {
			continue // every tool's help, and a usage line's placeholder or optional verb
		}
		if i := slices.Index(words, "--"); i >= 0 {
			words = words[:i] // the command after -- is the one the tool runs
		}
		verb := verbWords(words)
		if len(verb) == 0 {
			continue
		}
		r := h.run(t, words[0], append(verb, "-h")...)
		if r.code != 0 && len(verb) == 2 {
			verb = verb[:1] // the second word was the verb's argument
			r = h.run(t, words[0], append(verb, "-h")...)
		}
		if r.code != 0 {
			problems = append(problems, words[0]+" has no verb "+strings.Join(verb, " ")+" (`"+s[1]+"`)")
			continue
		}
		hay := r.out + "\n" + h.run(t, words[0], "help").out
		for _, w := range words[1:] {
			flag, ok := strings.CutPrefix(w, "--")
			if !ok {
				continue
			}
			flag, _, _ = strings.Cut(flag, "=")
			flag = strings.TrimRight(flag, ")]}.,;:|")
			if flag == "" {
				continue
			}
			if !regexp.MustCompile(`--` + regexp.QuoteMeta(flag) + `([^a-zA-Z0-9_-]|$)`).MatchString(hay) {
				problems = append(problems, words[0]+" "+strings.Join(verb, " ")+" has no flag --"+flag+" (`"+s[1]+"`)")
			}
		}
	}
	return problems
}

// cannedPlaybookHelp is help as the two witness tools print it, enough for
// the checker's witnesses without a build: each key is a call the checker
// makes, and a call with no key fails the test.
func cannedPlaybookHelp() *playbookHelp {
	friendHelp := "usage:\n  nova-friend lanes --as <me> [--clear-faults]\n      [--dir <d>] [--state-dir <d>]\n"
	lanes := "usage: nova-friend lanes --as <me> [--clear-faults] [--dir <d>] [--state-dir <d>]\n" +
		"  --as string\n  --clear-faults\n  --dir string\n  --state-dir string\n"
	doctorHelp := "usage:\n  nova-doctor run [--check <name>]\n  nova-doctor version\n"
	return &playbookHelp{cache: map[string]helpRun{
		"nova-friend help":         {out: friendHelp},
		"nova-friend lanes -h":     {out: lanes},
		"nova-friend help lanes":   {out: lanes},
		"nova-friend fly -h":       {out: "nova-friend: unknown verb fly", code: 2},
		"nova-doctor help":         {out: doctorHelp},
		"nova-doctor run -h":       {out: "usage: nova-doctor run [--check <name>]\n  --check string\n"},
		"nova-doctor version -h":   {out: "usage: nova-doctor version\n"},
		"nova-doctor help version": {out: "usage: nova-doctor version\n"},
	}}
}

// TestToolsPlaybookCheckerRefusesWhatTheHelpLacks holds the checker to canned
// help: real usage lines, verbs and flags pass, and each witness is refused
// for its own reason, so a check that passes everything is red at unit time
// as well as against the built tools.
func TestToolsPlaybookCheckerRefusesWhatTheHelpLacks(t *testing.T) {
	t.Parallel()

	h := cannedPlaybookHelp()
	good := "usage: `nova-friend lanes --as <me> [--clear-faults] [--dir <d>] [--state-dir <d>]`\n" +
		"run `nova-friend lanes --as <me> --clear-faults` and `nova-doctor run --check harness`\n"
	assert.Empty(t, checkToolsPlaybook(t, h, good), "real usage lines and verbs are refused")
	for text, want := range map[string]string{
		"usage: `nova-friend lanes --fly`":                              "usage line not in nova-friend's help",
		"usage: `nova-doctor version`\n`nova-friend fly --as <me>`":     "nova-friend has no verb fly",
		"usage: `nova-doctor version`\n`nova-friend lanes --fly`":       "nova-friend lanes has no flag --fly",
		"no usage line at all, only `nova-sprint where` from elsewhere": "quotes no usage line of a tool built here",
	} {
		ps := checkToolsPlaybook(t, h, text)
		assert.NotEmpty(t, ps, "the text is not refused: %s", text)
		assert.Contains(t, strings.Join(ps, "\n"), want, "the text is refused for another reason: %s", text)
	}
}
