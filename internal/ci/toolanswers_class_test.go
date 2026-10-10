package ci

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tool-answers rule: every tool answers a mistake with the way forward,
// because its reader is an AI that acts on one reading (tool ledger X2, X3,
// X4, X11, X12). The walk is functional, inside
// TestEveryCommandMeetsTheOnboardingStandard, on the binary that test builds;
// the judges below are plain functions, proved here in the unit tier.

// stockFlagLine is the flag package's own answer to an unknown flag.
const stockFlagLine = "flag provided but not defined"

// bareAnswers is "" when a bare command's refusal carries the REFUSED word.
func bareAnswers(stderr string) string {
	if !strings.Contains(stderr, "REFUSED") {
		return fmt.Sprintf("the bare refusal carries no REFUSED word: %q", firstLine(stderr))
	}
	return ""
}

// unknownVerbAnswers is "" when an unknown verb is refused (exit 2, the REFUSED
// word, at most two lines) naming at least three of the tool's verbs (all of
// them when it has fewer), by their first words.
func unknownVerbAnswers(code int, said string, verbs []string) string {
	var firsts []string
	for _, v := range verbs {
		if w := strings.Fields(v); len(w) > 0 && w[0] != "help" && !slices.Contains(firsts, w[0]) {
			firsts = append(firsts, w[0])
		}
	}
	named := 0
	for _, w := range firsts {
		if regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(w) + `($|[^a-z0-9-])`).MatchString(said) {
			named++
		}
	}
	switch want := min(3, len(firsts)); {
	case code != 2:
		return fmt.Sprintf("an unknown verb exits %d, not 2: %q", code, firstLine(said))
	case !strings.Contains(said, "REFUSED"):
		return fmt.Sprintf("an unknown verb is answered without the REFUSED word: %q", firstLine(said))
	case strings.Count(strings.TrimSuffix(said, "\n"), "\n") > 1:
		return fmt.Sprintf("an unknown verb is answered in %d lines, not one: %q", strings.Count(said, "\n"), firstLine(said))
	case named < want:
		return fmt.Sprintf("an unknown verb is answered naming %d of the verbs, want %d: %q", named, want, firstLine(said))
	}
	return ""
}

// unknownFlagAnswers is "" when an unknown flag is refused with the REFUSED
// word, without the flag package's stock line, naming one of the verb's flags
// when its -h lists any.
func unknownFlagAnswers(code int, said string, flags []string) string {
	switch {
	case code == 0:
		return "an unknown flag exits 0"
	case strings.Contains(said, stockFlagLine):
		return fmt.Sprintf("an unknown flag is answered with the flag package's stock line: %q", firstLine(said))
	case !strings.Contains(said, "REFUSED"):
		return fmt.Sprintf("an unknown flag is answered without the REFUSED word: %q", firstLine(said))
	}
	for _, f := range flags {
		if regexp.MustCompile(`--` + regexp.QuoteMeta(f) + `($|[^a-z0-9-])`).MatchString(said) {
			return ""
		}
	}
	if len(flags) == 0 {
		return ""
	}
	return fmt.Sprintf("an unknown flag is answered naming none of the verb's flags (%s): %q", strings.Join(flags, ", "), firstLine(said))
}

// groupHelpAnswers is "" when a group's -h is help (exit 0, on stdout, nothing
// on stderr) that names every verb of the group (members are its verbs' full
// names), as the group form `usage: <tool> <group> <a|b> [flags]` does.
func groupHelpAnswers(code int, stdout, stderr string, members []string) string {
	if code != 0 || strings.TrimSpace(stdout) == "" || stderr != "" {
		return fmt.Sprintf("exits %d with %q on stdout and %q on stderr", code, firstLine(stdout), firstLine(stderr))
	}
	var missing []string
	for _, m := range members {
		w := strings.Fields(m)
		if !regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(w[len(w)-1]) + `($|[^a-z0-9-])`).MatchString(stdout) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("its help does not name %s: %q", strings.Join(missing, ", "), firstLine(stdout))
	}
	return ""
}

// groupMembers is the verbs of a group: the verbs whose first word it is.
func groupMembers(group string, verbs []string) []string {
	var out []string
	for _, v := range verbs {
		if w := strings.Fields(v); len(w) > 1 && w[0] == group {
			out = append(out, v)
		}
	}
	return out
}

// helpFlagRe reads a flag line of a verb's -h: `  --name <type>  text`.
var helpFlagRe = regexp.MustCompile(`(?m)^\s+--?([a-z][a-z0-9-]*)\b`)

// helpFlags is the flags a verb's -h lists, help itself aside.
func helpFlags(help string) []string {
	var out []string
	for _, m := range helpFlagRe.FindAllStringSubmatch(help, -1) {
		if m[1] != "h" && m[1] != "help" && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// effectRe reads the effect line pkg/tool prints last in a verb's -h.
var effectRe = regexp.MustCompile(`(?m)^effect: (.*)$`)

// dryRunAnswers is "" when a verb's -h lists --dry-run, or states an effect
// that is an inspection.
func dryRunAnswers(help string) string {
	if slices.Contains(helpFlags(help), "dry-run") {
		return ""
	}
	m := effectRe.FindStringSubmatch(help)
	switch {
	case m == nil:
		return "its -h states no effect"
	case strings.HasPrefix(m[1], "inspection"):
		return ""
	}
	return fmt.Sprintf("it writes (effect: %s) and takes no --dry-run", m[1])
}

// verbGroups is the first word of every verb of more than one word.
func verbGroups(verbs []string) []string {
	var out []string
	for _, v := range verbs {
		if w := strings.Fields(v); len(w) > 1 && !slices.Contains(out, w[0]) {
			out = append(out, w[0])
		}
	}
	return out
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// TestToolAnswersJudges proves each judge on the answers the tools give today
// and on the answer pkg/tool gives: the good one passes, each bad one is
// named.
func TestToolAnswersJudges(t *testing.T) {
	t.Parallel()
	verbs := []string{"open", "append", "index", "receipt", "fn load", "fn ls", "version"}
	flags := []string{"entry", "json", "session", "store"}
	skeleton := "RECEIPT REFUSED: unknown flag --sesion; did you mean --session? receipt takes --entry, --json, --session, --store; run: nova-cairn receipt -h\n"
	for _, tc := range []struct {
		name, got, want string
	}{
		{"bare with the word", bareAnswers("CAIRN REFUSED: no verb given; run: nova-cairn help\n"), ""},
		{"bare without it", bareAnswers("nova-bus: no verb given; run: nova-bus help\n"), "carries no REFUSED word"},
		{"verb listed", unknownVerbAnswers(2, `CAIRN REFUSED: unknown verb "x"; the verbs are open, append, index, receipt, fn load, version; run: nova-cairn help`, verbs), ""},
		{"verb not listed", unknownVerbAnswers(2, `BUS REFUSED: unknown subcommand "x"; run: nova-bus help`, verbs), "naming 0 of the verbs, want 3"},
		{"verb at exit 1", unknownVerbAnswers(1, "REFUSED open append index", verbs), "exits 1, not 2"},
		{"verb with no word", unknownVerbAnswers(2, "nova-bus: unknown verb; open, append, index", verbs), "without the REFUSED word"},
		{"verb as the banner", unknownVerbAnswers(2, "REFUSED\nopen\nappend\nindex\n", verbs), "in 4 lines"},
		{"a verb named inside a word is not named", unknownVerbAnswers(2, "REFUSED: reopened, appendix, indexes", verbs), "naming 0"},
		{"flag from the skeleton", unknownFlagAnswers(2, skeleton, flags), ""},
		{"flag, stock line", unknownFlagAnswers(2, "RECEIPT REFUSED: flag provided but not defined: -sesion; run: nova-cairn help", flags), "stock line"},
		{"flag at exit 0", unknownFlagAnswers(0, skeleton, flags), "exits 0"},
		{"flag without the word", unknownFlagAnswers(2, "nova-bus send: unknown flag --x; flags: --store", flags), "without the REFUSED word"},
		{"flag, none named", unknownFlagAnswers(2, "SEND REFUSED: unknown flag --x; run: nova-bus help send", flags), "naming none of the verb's flags"},
		{"flag, a verb with no flags", unknownFlagAnswers(2, "RAW REFUSED: unknown flag --x; raw takes no flags", nil), ""},
		{"dry run listed", dryRunAnswers("flags:\n  --dry-run  print the plan\n  --json  as JSON\nexit codes: 0\n"), ""},
		{"inspection", dryRunAnswers("flags:\n  --json  x\neffect: inspection: reads, writes nothing\n"), ""},
		{"a write with no dry run", dryRunAnswers("flags:\n  --json  x\neffect: local write: writes files on this machine\n"), "it writes (effect: local write"},
		{"no effect", dryRunAnswers("usage: nova-bus send [flags]\nflags:\n  --as <string>  who\n"), "states no effect"},
		{"group form", groupHelpAnswers(0, "usage: nova-redis fn <load|ls> [flags]\nexit codes: 0\n", "", []string{"fn load", "fn ls"}), ""},
		{"group refused", groupHelpAnswers(2, "", "FN REFUSED: no subverb", []string{"fn load"}), "exits 2"},
		{"group help on stderr", groupHelpAnswers(0, "usage: x fn <load>\n", "warning\n", []string{"fn load"}), "exits 0"},
		{"group help naming a verb short", groupHelpAnswers(0, "usage: nova-redis fn [flags]\n  nova-redis fn loader\n", "", []string{"fn load", "fn ls"}),
			"does not name fn load, fn ls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.want == "" {
				assert.Empty(t, tc.got)
				return
			}
			assert.Contains(t, tc.got, tc.want)
		})
	}
	assert.Equal(t, []string{"dry-run", "json", "store"}, helpFlags("usage: nova-x put [flags]\nfrom `nova-x help`:\n  nova-x put --store <dir>\nflags:\n  --dry-run  p\n  --json  j\n  -h  help\n  --store <string>  s\n"))
	assert.Equal(t, []string{"fn", "github"}, verbGroups([]string{"open", "fn load", "fn ls", "github receipt"}))
	assert.Equal(t, []string{"fn load", "fn ls"}, groupMembers("fn", []string{"open", "fn load", "fn ls", "github receipt"}))
}
