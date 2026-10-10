package ci

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exit-word rule: a tool's last status word and exit code agree (exit codes
// tell the truth, docs/STANDARD.md section 2; skeleton contract 1.2 and 1.3).
// OK requires exit 0, FAILED requires exit 1, REFUSED requires exit 2, and an
// exit code above 2 must be listed in the verb's exit table.
// The walk is functional (exit_word_functional_test.go); the judge below is
// proved in the unit tier.

// exitWordStatusRe finds a line's status word: OK, FAILED, or REFUSED,
// either at the line start or preceded by tool/verb tokens in the skeleton
// grammar (<TOKEN> STATUS).
var exitWordStatusRe = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9_-]*(?: [A-Za-z0-9][A-Za-z0-9_-]*)*\s+)?(OK|FAILED|REFUSED)(?:[^A-Za-z0-9_-]|$)`)

var (
	exitTableLabel  = regexp.MustCompile(`(?i)(?:\bexit codes?\s*:|^\s*exit\s*:)`)
	exitInlineLabel = regexp.MustCompile(`(?i)^\s*exit\s+\d+`)
	exitHeaderRe    = regexp.MustCompile(`^(?:flags|example|setup|effect|usage|kinds|how it works|first run):\s*`)
	exitRangeRe     = regexp.MustCompile(`\b(\d+)\s*-\s*(\d+)\b`)
	exitCodeNumRe   = regexp.MustCompile(`\b(\d+)\b`)
)

// parseExitTable parses published exit codes from help text or an exit table
// string. It extracts the exit codes paragraph, handles ranges (e.g. 0-124),
// and returns the sorted, unique list of allowed exit codes. If the text only
// points to the tool help (e.g. "exit codes: see `...`"), it returns nil.
func parseExitTable(help string) []int {
	if strings.TrimSpace(help) == "" {
		return nil
	}
	lines := strings.Split(help, "\n")
	start := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if exitTableLabel.MatchString(l) || exitInlineLabel.MatchString(trimmed) {
			start = i
			break
		}
	}
	var text string
	if start >= 0 {
		var para []string
		para = append(para, lines[start])
		for j := start + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" || exitHeaderRe.MatchString(trimmed) {
				break
			}
			para = append(para, lines[j])
		}
		text = strings.Join(para, "\n")
	} else {
		text = help
	}

	// A pointer to tool help states no codes of its own.
	if strings.Contains(text, "exit codes: see `") || strings.Contains(text, "exit codes: see ") {
		return nil
	}

	seen := map[int]bool{}
	for _, m := range exitRangeRe.FindAllStringSubmatch(text, -1) {
		lo, err1 := strconv.Atoi(m[1])
		hi, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && lo <= hi && hi-lo <= 256 {
			for c := lo; c <= hi; c++ {
				seen[c] = true
			}
		}
	}
	for _, m := range exitCodeNumRe.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			seen[n] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	codes := make([]int, 0, len(seen))
	for c := range seen {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}

// exitWordLastStatus finds the last status word (OK, FAILED, REFUSED) and line
// in stdout and stderr (results on stdout, refusals on stderr).
func exitWordLastStatus(stdout, stderr string) (word string, line string) {
	var lines []string
	if stdout != "" {
		lines = append(lines, strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")...)
	}
	if stderr != "" {
		lines = append(lines, strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")...)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if m := exitWordStatusRe.FindStringSubmatch(trimmed); m != nil {
			return m[1], trimmed
		}
	}
	return "", ""
}

// exitWordAnswers is "" when the observed exit code agrees with the last
// status word according to the status contract (OK→0, FAILED→1, REFUSED→2,
// and code > 2 listed in exitTable); otherwise it names the breach.
func exitWordAnswers(code int, stdout, stderr string, exitTable []int) string {
	word, line := exitWordLastStatus(stdout, stderr)
	if word == "" {
		return ""
	}
	if word == "OK" {
		if code != 0 {
			return fmt.Sprintf("exit code %d disagrees with status word OK (line %q); align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table", code, line)
		}
		return ""
	}
	if code > 2 {
		if slices.Contains(exitTable, code) {
			return ""
		}
		return fmt.Sprintf("exit code %d is above 2 and not in the verb's exit table (line %q); align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table", code, line)
	}
	switch word {
	case "FAILED":
		if code != 1 {
			return fmt.Sprintf("exit code %d disagrees with status word FAILED (line %q); align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table", code, line)
		}
	case "REFUSED":
		if code != 2 {
			return fmt.Sprintf("exit code %d disagrees with status word REFUSED (line %q); align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table", code, line)
		}
	}
	return ""
}

// TestExitWordJudges is the witness: a fixture that breaks the rule once is
// refused naming the site and remedy, and the fixed fixture passes.
func TestExitWordJudges(t *testing.T) {
	t.Parallel()

	const quickstartOK = "QUICKSTART RUN dir=./self checks=2: links, then nocode\n" +
		"LINKS OK dir=./self files=4 links=3 excluded=0 broken=0\n" +
		"NOCODE OK dir=./self files=5 deny-list=floor-list findings=0\n" +
		"QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus\n"

	const hygieneFailed = "HYGIENE FAILED repo=. base=main head=card paths=sign/** findings=4\n" +
		"HYGIENE FINDING reason=identity at=0a19082d2973 why=\"author someone@elsewhere.example\"\n" +
		"HYGIENE FINDING reason=out-of-path at=elsewhere.go why=\"path matches none of declared\"\n" +
		"HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo .\n"

	const busRefused = "SEND REFUSED: --to is required; run: nova-bus help send\n"

	for _, tc := range []struct {
		name           string
		code           int
		stdout, stderr string
		exitTable      []int
		want           string
	}{
		{"good OK fixture", 0, "VERB OK\n", "", nil, ""},
		{"good FAILED fixture", 1, "VERB FAILED findings=2\n", "", nil, ""},
		{"good REFUSED fixture", 2, "", "VERB REFUSED: why; run: remedy\n", nil, ""},
		{"good quickstart transcript (docs/TESTS.md:297-302)", 0, quickstartOK, "", nil, ""},
		{"good hygiene transcript (docs/TESTS.md:318-324)", 1, hygieneFailed, "", nil, ""},
		{"good refusal transcript", 2, "", busRefused, nil, ""},
		{"good exit code above 2 in exit table", 3, "LOCK FAILED\n", "", []int{3}, ""},
		{"the witness: OK with exit 1", 1, "QUICKSTART OK done=2 worst-exit=1\n", "", nil, "exit code 1 disagrees with status word OK"},
		{"the witness: OK with exit 2", 2, "VERB OK\n", "", nil, "exit code 2 disagrees with status word OK"},
		{"the witness: FAILED with exit 0", 0, "HYGIENE FAILED\n", "", nil, "exit code 0 disagrees with status word FAILED"},
		{"the witness: FAILED with exit 2", 2, "HYGIENE FAILED\n", "", nil, "exit code 2 disagrees with status word FAILED"},
		{"the witness: REFUSED with exit 0", 0, "", "SEND REFUSED: --to is required; run: nova-bus help send\n", nil, "exit code 0 disagrees with status word REFUSED"},
		{"the witness: REFUSED with exit 1", 1, "", "SEND REFUSED: --to is required; run: nova-bus help send\n", nil, "exit code 1 disagrees with status word REFUSED"},
		{"the witness: exit code 3 not in exit table", 3, "VERB FAILED\n", "", nil, "is above 2 and not in the verb's exit table"},
		{"the witness: bare OK with exit 1", 1, "OK\n", "", nil, "exit code 1 disagrees with status word OK"},
		{"multi-line run with intermediate OK and final FAILED at exit 1", 1, "LINKS OK\nNOCODE FAILED\nQUICKSTART FAILED\n", "", nil, ""},
		{"multi-line run with intermediate FAILED and final OK at exit 1 is a breach", 1, "LINKS FAILED\nQUICKSTART OK\n", "", nil, "exit code 1 disagrees with status word OK"},
		{"run with no status word (e.g. help)", 0, "usage: nova-tool help\n", "", nil, ""},
		{"empty run", 0, "", "", nil, ""},
		{"listed code 3 parsed from help table passes", 3, "LOCK FAILED\n", "", parseExitTable("exit codes: 0 done, 1 failed, 2 usage, 3 lock held\n"), ""},
		{"unlisted code 4 parsed from help table is refused", 4, "LOCK FAILED\n", "", parseExitTable("exit codes: 0 done, 1 failed, 2 usage, 3 lock held\n"), "is above 2 and not in the verb's exit table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := exitWordAnswers(tc.code, tc.stdout, tc.stderr, tc.exitTable)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
		})
	}
}

// TestExitWordReadsFixtures is the witness test: a fixture that breaks the rule once is refused naming the site,
// and the fixed fixture passes.
func TestExitWordReadsFixtures(t *testing.T) {
	t.Parallel()

	breachStdout := "nova-check OK dir=./self files=4\n"
	breachErr := exitWordAnswers(1, breachStdout, "", nil)
	assert.Contains(t, breachErr, "disagrees with status word OK")

	fixedErr := exitWordAnswers(0, breachStdout, "", nil)
	assert.Empty(t, fixedErr)
}

// TestExitWordParsesHelpTable is the witness for listed and unlisted codes:
// it parses the applicable exit table from help and proves that a listed code
// above 2 passes while an unlisted code is refused naming the site and remedy.
func TestExitWordParsesHelpTable(t *testing.T) {
	t.Parallel()

	const loopRunHelp = "usage: nova-config loop run [flags]\n" +
		"exit codes: the command's own, 128+N when a signal ended it; 1 the row is not runnable; 2 usage, or the command did not start; 3 another copy holds the lock\n"

	table := parseExitTable(loopRunHelp)
	require.True(t, slices.Contains(table, 3), "parseExitTable must parse listed code 3 from help; got %v", table)
	require.False(t, slices.Contains(table, 4), "code 4 is not in the table; got %v", table)

	// Listed code 3: passes.
	listedErr := exitWordAnswers(3, "LOOP RUN FAILED lock held\n", "", table)
	assert.Empty(t, listedErr, "listed exit code 3 must pass")

	// Unlisted code 4: the witness refused naming the site and remedy.
	unlistedErr := exitWordAnswers(4, "LOOP RUN FAILED error\n", "", table)
	assert.Contains(t, unlistedErr, "exit code 4 is above 2 and not in the verb's exit table")
}
