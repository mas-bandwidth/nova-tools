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
	assert.Contains(t, contract, strings.TrimSuffix(cardgen.Attribution, "\n"), "the contract carries the ATTRIBUTION line")
	assert.Contains(t, contract, strings.TrimSuffix(cardgen.AsARead, "\n"), "the contract carries AS A READ")
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
		for _, line := range strings.Split(full, "\n") {
			if frameLine(line) && !strings.HasPrefix(line, "You are a child") && !regexp.MustCompile(`^STEP [245]\.`).MatchString(line) {
				assert.Contains(t, contract, line, "%s: a frame line Render writes the same for every card is the contract's, verbatim", c.ID)
			}
		}
		compact := Compact(full, ContractVersion)
		assert.True(t, strings.HasSuffix(compact, "\n"+ContractLine()+"\n"), "%s: a brief on file cuts to the line last", c.ID)
		assert.Equal(t, 1, strings.Count(compact, ContractKey+":"))
		for _, frame := range []string{"RULES.", "You are a child", "Libraries considered", "ATTRIBUTION:", "AS A READ", "A By: trailer is judged", "\n\n\n"} {
			assert.NotContains(t, compact, frame, "%s: the cut takes the frame out", c.ID)
		}
		assert.NotRegexp(t, `(?m)^STEP \d\.`, compact)
		assert.Contains(t, compact, "THE TASK. "+c.Task, "%s: the task stays as written", c.ID)
		assert.True(t, strings.HasPrefix(compact, strings.SplitN(full, "\n", 2)[0]+"\n"), "line 1 stays")
		assert.Equal(t, compact, Compact(compact, ContractVersion), "a brief by reference cuts to itself")
	}
	assert.Equal(t, 0, Tokens(""))
	assert.Equal(t, 2, Tokens("12345"))
}

// A version once published keeps its text (docs/SPEC-CARD-CONTRACT.md section 7): a brief
// names the version it was written against, so a change to the contract is a new block
// beside the old, never an edit of v1. The digest is v1's text as published.
func TestAPublishedContractVersionKeepsItsText(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile("../../" + ContractPath)
	require.NoError(t, err)
	published := map[string]string{
		"v1": "57a18c629f6538ced4ec20495e1a4bb1f8e2070cd7c1d359733ed9eb5503dd20",
	}
	for version, digest := range published {
		text, err := ContractText(doc, version)
		require.NoError(t, err)
		assert.Equal(t, digest, fmt.Sprintf("%x", sha256.Sum256([]byte(text))), "contract %s was edited; write a new version beside it", version)
	}
	_, err = ContractText(doc, ContractVersion)
	assert.NoError(t, err, "the version a brief written now names is in the file")
}
