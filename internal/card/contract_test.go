package card

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// twentyCards are the 20 cards the measure renders (docs/SPEC-CARD-CONTRACT.md section 7):
// six serial-tests ledger cards, four dead-code ledger cards, six findings cards and four
// help cards, the generator's three sources, each card's PATHS computed as nova-card
// generate computes them.
func twentyCards(t *testing.T) []cardgen.Card {
	t.Helper()
	var cards []cardgen.Card
	serial, _ := cardgen.ParseLedger(cardgen.Ledgers["serial-tests"], "cmd/nova-bus/a_test.go:TestOne serial: t.Setenv\ncmd/nova-bus/b_test.go:TestTwo serial: t.Chdir\ninternal/swarm/c_test.go:TestThree serial: os.Setenv\ninternal/sprint/d_test.go:TestFour serial: swaps package var\ninternal/friend/e_test.go:TestFive serial: t.Setenv\ninternal/member/f_test.go:TestSix serial: t.Chdir\n")
	cards = append(cards, cardgen.PlanLedger(cardgen.Ledgers["serial-tests"], serial, "", "", 0).Cards...)
	dead, _ := cardgen.ParseLedger(cardgen.Ledgers["dead-code"], "internal/bus 3\ninternal/cairn 1\ninternal/tool 2\ncmd/nova-ci 4\n")
	cards = append(cards, cardgen.PlanLedger(cardgen.Ledgers["dead-code"], dead, "", "", 0).Cards...)
	fs, _ := cardgen.ParseFindings("internal/bus/send.go:12\tthe receipt is not fsynced\tcall f.Sync before close\tinternal/bus TestReceiptIsFsynced\n" +
		"internal/bus/recv.go:40\tthe error is swallowed\treturn it\t\n" +
		"cmd/nova-bus/main.go:9\tthe banner names a verb that is gone\tdrop the line\t\n" +
		"internal/cairn/index.go:77\tthe index is rewritten whole\tappend the row\tinternal/cairn TestIndexAppends\n" +
		"internal/tool/out.go:5\tthe JSON drops the remedy\tencode it\tinternal/tool TestRemedyInJSON\n" +
		"fleet/roles/a.yml:3\tthe role names a host\tread it from the inventory\tinternal/fleet TestRole\n")
	cards = append(cards, cardgen.PlanFindings(fs, "", "", 0).Cards...)
	for _, tool := range []string{"nova-bus", "nova-sprint", "nova-card", "nova-friend"} {
		cards = append(cards, cardgen.PlanHelp(tool, tool+": a tool\n\n"+strings.Repeat("x", 120)+"\n", "", "", ""))
	}
	require.Len(t, cards, 20)
	for i := range cards {
		cards[i].Paths = Paths(header, cards[i])
	}
	return cards
}

// measure is the before-and-after of the 20 briefs, as the contract file states it: the
// bytes and the tokens (Tokens) of the long form, the full frame in every brief, against
// the brief by reference, and the contract a lane reads once beside it.
func measure(cards []cardgen.Card) string {
	full, ref := 0, 0
	fullTokens, refTokens := 0, 0
	for _, c := range cards {
		long := cardgen.Render(cardgen.Header{Repo: header.Repo, Base: header.Base, Sha: header.Sha, Full: true}, c)
		short := cardgen.Render(header, c)
		full, ref = full+len(long), ref+len(short)
		fullTokens, refTokens = fullTokens+Tokens(long), refTokens+Tokens(short)
	}
	contract, _ := cardgen.HeldContract(cardgen.ContractVersion)
	return fmt.Sprintf("MEASURE briefs=%d full_bytes=%d full_tokens=%d ref_bytes=%d ref_tokens=%d contract_bytes=%d contract_tokens=%d",
		len(cards), full, fullTokens, ref, refTokens, len(contract), Tokens(contract))
}

// A brief is its header lines and one line, `Contract: docs/SPEC-CARD-CONTRACT.md <version>`,
// that the lane reads once from the repository (docs/SPEC-CARD-CONTRACT.md section 7): the
// generator writes that shape by default, the task on its STOP line, no paragraph, no RULES
// and no STEP; the contract carries what every brief used to repeat; card.Lint passes it
// with no option, reading it with the held contract in place of the line, and a version
// this build does not hold is the finding. Its PATHS are the same packages as the long
// form's, and the measure of 20 briefs is the one the contract file states.
func TestABriefIsFiveLinesAndAContractReference(t *testing.T) {
	t.Parallel()
	contract, ok := cardgen.HeldContract(cardgen.ContractVersion)
	require.True(t, ok, "this build holds the contract a brief written now names")
	for _, frame := range []string{strings.TrimSuffix(swarm.ChildRulesParagraph(), "\n"), swarm.GateNamesWhoseFile,
		strings.TrimSuffix(cardgen.Attribution, "\n"), strings.TrimSuffix(cardgen.AsARead, "\n"),
		"STEP 1.", "STEP 2.", "STEP 3.", "STEP 4.", "STEP 5.", "STEP 6.", "You are a child of the coordinator", "Libraries considered:"} {
		assert.Contains(t, contract, frame, "the contract carries the frame")
	}
	cards := twentyCards(t)
	keyLine := regexp.MustCompile(`^(RESULT|REPO|BASE|KIND|DEPENDS-ON|PATHS|NEW|TEST|START|STOP|Deadline): \S`)
	for _, c := range cards {
		brief := cardgen.Render(header, c)
		lines := strings.Split(strings.TrimSuffix(brief, "\n"), "\n")
		assert.Equal(t, cardgen.ContractLine(), lines[len(lines)-1], "%s: the Contract: line is last", c.ID)
		assert.Equal(t, "Contract: docs/SPEC-CARD-CONTRACT.md v2", cardgen.ContractLine())
		keys := map[string]bool{}
		for _, line := range lines[:len(lines)-1] {
			assert.Regexp(t, keyLine, line, "%s: every line but the reference is a header line", c.ID)
			key, _, _ := strings.Cut(line, ":")
			keys[key] = true
		}
		for _, key := range []string{"REPO", "BASE", "START", "STOP", "PATHS", "TEST"} {
			assert.True(t, keys[key], "%s: the brief carries %s:", c.ID, key)
		}
		stop := "Done when the test "
		if c.Kind == swarm.LedgerKind {
			stop = "Done when the ledger row"
			if c.Counted {
				stop = "Done when the count on the ledger row"
			}
		}
		assert.Contains(t, brief, "\nSTOP: "+strings.Join(strings.Fields(c.Task), " ")+" "+stop, "%s: the task and its stop condition ride on STOP", c.ID)
		for _, frame := range []string{"RULES.", "You are a child", "Libraries considered", "THE TASK.", "ATTRIBUTION:", "AS A READ"} {
			assert.NotContains(t, brief, frame, "%s: the frame is the contract's", c.ID)
		}
		assert.NotRegexp(t, `(?m)^STEP \d\.`, brief, "%s: no STEP rides in the brief", c.ID)
		assert.Equal(t, c.Paths, PackagePaths(Start(brief), Docs(c.Paths)), "%s: the same packages from either form", c.ID)

		assert.Empty(t, Lint(c.ID, brief, Options{}), "%s: admitted with no contract option", c.ID)
		read, why := cardgen.AsRead(brief)
		assert.Empty(t, why)
		assert.Equal(t, lines[0], strings.SplitN(read, "\n", 2)[0], "%s: read, line 1 says what it said", c.ID)
		assert.Contains(t, read, contract)
		assert.NotContains(t, read, cardgen.ContractLine())

		checks := func(b string) []string {
			var out []string
			for _, f := range Lint(c.ID, b, Options{}) {
				out = append(out, f.Check)
			}
			return out
		}
		assert.Contains(t, checks(strings.Replace(brief, " v2\n", " v0\n", 1)), "contract-version", "%s: a version this build does not hold", c.ID)
		assert.Contains(t, checks(strings.Replace(brief, " v2\n", "\n", 1)), "contract-version", "%s: a reference with no version", c.ID)

		long := cardgen.Render(cardgen.Header{Repo: header.Repo, Base: header.Base, Sha: header.Sha, Full: true}, c)
		assert.Contains(t, long, "\nRULES.\n", "%s: the long form carries the frame, on its flag", c.ID)
		assert.NotContains(t, long, cardgen.ContractLine())
		assert.Empty(t, Lint(c.ID, long, Options{}), c.ID)
		assert.Less(t, Tokens(brief)*2, Tokens(long), "%s: a brief by reference weighs under half the long form", c.ID)
	}
	// a Contract: line that names no contract file is the brief's own prose
	prose := cardgen.Render(cardgen.Header{Repo: header.Repo, Base: header.Base, Sha: header.Sha, Full: true}, cards[0]) + "Contract: the lane keeps its tests green\n"
	_, at := cardgen.ContractRef(prose)
	assert.Zero(t, at)
	assert.Empty(t, Lint(cards[0].ID, prose, Options{}))

	doc, err := os.ReadFile("../../" + cardgen.ContractPath)
	require.NoError(t, err)
	got := measure(cards)
	t.Log(got)
	assert.Contains(t, string(doc), "\n    "+got+"\n", "the contract file states the measure of the 20 briefs the generator renders; write the line it prints")
	assert.Equal(t, 0, Tokens(""))
	assert.Equal(t, 2, Tokens("12345"))
}

// The contract text lives in one file (docs/SPEC-CARD-CONTRACT.md section 7), and this
// build's copy of each version is that file's block byte for byte; a version once
// published keeps its text, so a change is a new block beside the old, never an edit of
// v1. The digest is v1's text as published.
func TestTheHeldContractIsTheDocsBlock(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile("../../" + cardgen.ContractPath)
	require.NoError(t, err)
	for version, digest := range map[string]string{"v1": "1454b189c47367bfe17f9b4ce33618797457a8a5f732842210d3fcf145f8491a", "v2": "31ad75b3aba0942fc471c02f519793dc72e8568cc5d2f673789e5cc6960ed282"} {
		text, err := cardgen.ContractText(doc, version)
		require.NoError(t, err)
		held, ok := cardgen.HeldContract(version)
		require.True(t, ok, version)
		assert.Equal(t, text, held, "the held copy of %s is the file's block", version)
		assert.Equal(t, digest, fmt.Sprintf("%x", sha256.Sum256([]byte(text))), "contract %s was edited; write a new version beside it", version)
	}
	_, err = cardgen.ContractText(doc, "v0")
	assert.Error(t, err, "a version the file does not hold is no contract")
	_, ok := cardgen.HeldContract("../v1")
	assert.False(t, ok)
}
