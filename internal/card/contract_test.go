package card

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A brief by reference is its header lines and one Contract: line, and nothing of the
// frame: no paragraph, no RULES, no STEP (docs/SPEC-CARD-CONTRACT.md section 7). The
// contract is one versioned block of the contract file, it carries what every brief
// used to repeat, and the brief read with it in place of the line passes the lint every
// brief is held to; read with none, or at a version the file does not hold, it is the
// finding. Its START is Render's, so its PATHS are the same packages, and it weighs a
// fraction of the brief it replaces.
func TestABriefIsFiveLinesAndAContractReference(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile("../../" + ContractPath)
	require.NoError(t, err)
	contract, err := ContractText(doc, ContractVersion)
	require.NoError(t, err)
	assert.Contains(t, contract, strings.TrimSuffix(swarm.ChildRulesParagraph(), "\n"), "the contract carries the RULES")
	assert.Contains(t, contract, swarm.GateNamesWhoseFile)
	for _, step := range []string{"STEP 1.", "STEP 2.", "STEP 3.", "STEP 4.", "STEP 5.", "STEP 6.", "You are a child of the coordinator", "Libraries considered:"} {
		assert.Contains(t, contract, step)
	}
	_, err = ContractText(doc, "v0")
	assert.Error(t, err, "a version the file does not hold is no contract")
	read := func(version string) (string, error) { return ContractText(doc, version) }

	fs, _ := cardgen.ParseFindings("internal/bus/send.go:12\tlost\tkeep it\tinternal/ci TestX\n")
	cards := append(cardgen.PlanFindings(fs, "", "", 0).Cards, cardgen.PlanHelp("nova-x", "x\n", "TestExamples", "", ""))
	keyLine := regexp.MustCompile(`^(RESULT|REPO|BASE|KIND|DEPENDS-ON|PATHS|NEW|TEST|START|STOP|Deadline): \S`)
	for _, c := range cards {
		c.Paths = Paths(header, c)
		brief := Brief(header, c)
		lines := strings.Split(strings.TrimSuffix(brief, "\n"), "\n")
		require.NotEmpty(t, lines)
		assert.Equal(t, ContractLine(), lines[len(lines)-1], "%s: the Contract: line is last", c.ID)
		keys := map[string]bool{}
		for _, line := range lines[:len(lines)-1] {
			assert.Regexp(t, keyLine, line, "%s: every line but the reference is a header line", c.ID)
			key, _, _ := strings.Cut(line, ":")
			keys[key] = true
		}
		for _, key := range []string{"REPO", "BASE", "START", "STOP", "PATHS", "TEST"} {
			assert.True(t, keys[key], "%s: the brief carries %s:", c.ID, key)
		}
		assert.Contains(t, brief, "STOP: "+strings.Join(strings.Fields(c.Task), " "), "%s: the task rides on STOP", c.ID)
		for _, frame := range []string{"RULES.", "You are a child", "Libraries considered", "THE TASK."} {
			assert.NotContains(t, brief, frame, "%s: the frame is the contract's", c.ID)
		}
		assert.NotRegexp(t, `(?m)^STEP \d\.`, brief, "%s: no STEP rides in the brief", c.ID)
		file, version, ok := ContractRef(brief)
		assert.True(t, ok)
		assert.Equal(t, [2]string{ContractPath, ContractVersion}, [2]string{file, version})
		assert.Equal(t, c.Paths, PackagePaths(Start(brief), Docs(c.Paths)), "%s: the same packages from either brief", c.ID)

		assert.Empty(t, Lint(c.ID, brief, Options{Contract: read}), "%s: read as the lane reads it, the brief is admitted", c.ID)
		handed := WithContract(brief, contract)
		assert.Equal(t, lines[0], strings.SplitN(handed, "\n", 2)[0], "line 1 says what it said")
		assert.NotContains(t, handed, ContractLine())
		assert.Contains(t, handed, contract)

		checks := func(o Options, b string) []string {
			var out []string
			for _, f := range Lint(c.ID, b, o) {
				out = append(out, f.Check)
			}
			return out
		}
		assert.Equal(t, []string{"contract-unread"}, checks(Options{}, brief), "no text to read is the finding")
		assert.Equal(t, []string{"contract-version"}, checks(Options{Contract: read}, strings.Replace(brief, " "+ContractVersion+"\n", " v0\n", 1)))

		full := cardgen.Render(header, c)
		assert.Less(t, Tokens(brief)*2, Tokens(full), "%s: a brief by reference weighs under half the brief it replaces", c.ID)
		compact := Compact(full, ContractVersion)
		assert.True(t, strings.HasSuffix(compact, "\n"+ContractLine()+"\n"), "%s: a brief on file cuts to its header and the line", c.ID)
		assert.NotRegexp(t, `(?m)^STEP \d\.`, compact)
	}
	assert.Equal(t, 0, Tokens(""))
	assert.Equal(t, 2, Tokens("12345"))
}
