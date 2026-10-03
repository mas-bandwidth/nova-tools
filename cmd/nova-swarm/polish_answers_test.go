package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The answers a cold AI reader acts on in one turn (docs/STANDARD.md sections 2 and 3;
// the tool ledger's rows W4, W5, W8, W11, W12, W13, X2, X3, X4, X7, X10).

func TestABareOrUnknownVerbIsRefusedNamingTheVerbs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"zz-no-such-verb"}, {"help", "batch"}} {
		exit, stdout, stderr := runSwarm(t, args...)
		assert.Equal(t, 2, exit, "%v", args)
		assert.Empty(t, stdout, "%v", args)
		assert.True(t, strings.HasPrefix(stderr, "nova-swarm REFUSED: "), stderr)
		assert.Contains(t, stderr, "the verbs are "+strings.Join(verbNames, ", "))
		assert.Contains(t, stderr, "; run: nova-swarm help")
		assert.Equal(t, 1, strings.Count(stderr, "\n"), "one line: %q", stderr)
	}
}

func TestAMisspelledFlagNamesTheVerbsFlags(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runSwarm(t, "lint", "--crad", "x.md")
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Equal(t, "nova-swarm lint REFUSED: unknown flag --crad; the flags of lint are --base-check, --card, --child-rules, --child-rules-file, --decide, --decide-answers, --decide-record, --fleet, --legs, --lineup, --max, --member-injects, --p95, --repo, --rules, --trust and 1 more; did you mean --card?; run: nova-swarm help lint\n", stderr)
	assert.Contains(t, stderr, "; run: nova-swarm help lint\n")
	assert.NotContains(t, stderr, "provided but not defined")

	_, _, stderr = runSwarm(t, "template", "--name", "card", "extra")
	assert.Contains(t, stderr, "nova-swarm template REFUSED: takes no positional arguments, got 1: [\"extra\"]")
}

// flagLineRE is a flag line of a verb's -h with no description after it.
var bareFlagLineRE = regexp.MustCompile(`(?m)^  --[a-z][a-z0-9-]*( <[^>]+>)?$`)

func TestEveryFlagOfEveryVerbSaysWhatItWants(t *testing.T) {
	t.Parallel()
	for _, verb := range [][]string{{"native"}, {"member"}, {"verify"}, {"template"}, {"profile"}, {"doctor"}, {"lint"},
		{"slots", "init"}, {"slots", "take"}, {"slots", "release"}, {"slots", "list"}} {
		help := swarmHelp(t, append(verb, "-h")...)
		assert.Contains(t, help, "flags:\n", "%v", verb)
		assert.Empty(t, bareFlagLineRE.FindAllString(help, -1), "%v -h lists a flag with no description", verb)
	}
}

func TestWorkerCheckSaysNoAtOneAndCouldNotRunAtTwo(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "w.json")
	require.NoError(t, os.WriteFile(bad, []byte("{}\n"), 0o600))
	exit, stdout, _ := runSwarm(t, "worker", "check", bad)
	assert.Equal(t, 1, exit, "a description read and found wanting is the verb saying no")
	assert.Contains(t, stdout, "WORKER DRIFT name:")

	exit, stdout, stderr := runSwarm(t, "worker", "check", filepath.Join(t.TempDir(), "absent.json"))
	assert.Equal(t, 2, exit, "a description that cannot be read is a check that could not run")
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "nova-swarm worker check REFUSED: the worker description cannot be read")
}

func TestSlotsListRefusesWhatIsNoStoreAndCountsAnEmptyOne(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runSwarm(t, "slots", "list", "--store", filepath.Join(t.TempDir(), "nosuch"))
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "is no slot store (it holds no shares.tsv); run: nova-swarm slots init --store")

	store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	exit, stdout, _ = runSwarm(t, "slots", "list", "--store", store)
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, "SLOTS OK store=")
	assert.Contains(t, stdout, " leases=0\n")
}

// The card template's own lines left unfilled are named, one NOTE each, under the OK the
// template's shape earns; a line filled in is not.
func TestAnUnfilledTemplateLineIsNamed(t *testing.T) {
	t.Parallel()
	card, err := swarm.Template("card")
	require.NoError(t, err)
	exit, stdout, _ := runSwarm(t, "lint", "--card", writeLintCard(t, "card.md", card), "--child-rules")
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, "LINT NOTE card=card.md placeholder: 2: REPO: <owner>/<name> remedy=fill it in before the card is handed out")
	assert.Contains(t, stdout, "LINT NOTE card=card.md placeholder: 3: BASE: <branch> ")

	filled := strings.Replace(strings.Replace(card, "REPO: <owner>/<name>", "REPO: acme/widgets", 1), "BASE: <branch>", "BASE: main", 1)
	_, stdout, _ = runSwarm(t, "lint", "--card", writeLintCard(t, "filled.md", filled), "--child-rules")
	assert.NotContains(t, stdout, "placeholder: 2:")
	assert.NotContains(t, stdout, "placeholder: 3:")
	assert.Contains(t, stdout, "placeholder: 1: RESULT: <label> sha=<sha12>", "the lines left unfilled are still named")
	assert.Empty(t, cardPlaceholders([]byte("STEP 1. run nova-sprint add --count <n>\n")), "a <...> of the card's own is not the template's")
}

func TestTheRemediesCiteNothingABinaryUserDoesNotHave(t *testing.T) {
	t.Parallel()
	cites := regexp.MustCompile(`practices? [0-9]|SPEC-[A-Z]+\.md|failed-cards-|STANDARD\.md|\(#[0-9]+|; #[0-9]+`)
	for _, name := range cardLintRuleNames() {
		assert.Empty(t, cites.FindString(cardLintRemedy(name)), "the remedy of %s: %q", name, cardLintRemedy(name))
	}
}

func TestTheReadPRTemplateAsksForOneQuotingRule(t *testing.T) {
	t.Parallel()
	body, err := swarm.Template("read-pr")
	require.NoError(t, err)
	assert.Contains(t, body, "QUOTE EVERY RULE VERBATIM")
	assert.Contains(t, body, "quoted verbatim in at most twelve words")
	assert.NotContains(t, body, "the\nrule in twelve words", "the bound no longer reads as a paraphrase")
}

// A verb's -h carries its own exit codes; nova-swarm help keeps them all at once.
func TestAVerbsHelpCarriesItsOwnExitCodes(t *testing.T) {
	t.Parallel()
	assert.Contains(t, usage, "\n"+exitParagraph+"\n", "the banner's paragraph is the one a verb's -h replaces")
	last := func(args ...string) string {
		lines := strings.Split(strings.TrimSpace(swarmHelp(t, args...)), "\n")
		return lines[len(lines)-1]
	}
	assert.Equal(t, verbExit["lint"], last("lint", "-h"))
	assert.Equal(t, verbExit["worker check"], last("worker", "check", "-h"))
	assert.Equal(t, commonExit, last("template", "-h"))
	assert.NotContains(t, last("template", "-h"), "member", "a template's help is not told of member's exit 3")
}

func TestTheHelpNamesTheLiveVerbsAndTheCardsRepoAndBase(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "help")
	assert.NotContains(t, help, "batch runs", "batch is no verb")
	assert.Contains(t, help, "member runs a sprint's cards on this machine")
	assert.Contains(t, help, "nova-swarm help <verb>")
	tmpl := swarmHelp(t, "template", "-h")
	assert.Contains(t, tmpl, "  REPO:      <owner>/<name>")
	assert.Contains(t, tmpl, "  BASE:      <branch>")
}
