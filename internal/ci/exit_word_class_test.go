package ci

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

// exitWordLabelRe finds the exit-codes label of a help text, in either the long
// `exit codes:` form or the short `exit:` that opens a line (the two verbflag
// itself reads).
var exitWordLabelRe = regexp.MustCompile(`(?i)\bexit codes?:|^\s*exit\s*:`)

// exitWordCodeRe finds one code, or a `lo-hi` range, in an exit-table line.
var exitWordCodeRe = regexp.MustCompile(`\b(\d+)(?:\s*-\s*(\d+))?`)

// exitWordExitCodes reads the codes a help text publishes: every code on its
// `exit codes:` line and the lines under it, up to the first blank line, so a
// hand-rolled tool's multi-line paragraph is read whole. A range `a-b` names
// every code it spans. The walk passes the result to exitWordAnswers as the
// table a code above 2 must appear in (docs/STANDARD.md section 2, "Exit codes
// tell the truth and the banner states them"). It reads text, not a tool.
func exitWordExitCodes(help string) []int {
	var codes []int
	lines := strings.Split(help, "\n")
	for i := 0; i < len(lines); i++ {
		if !exitWordLabelRe.MatchString(lines[i]) {
			continue
		}
		for ; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
			for _, m := range exitWordCodeRe.FindAllStringSubmatch(lines[i], -1) {
				lo, _ := strconv.Atoi(m[1])
				hi := lo
				if m[2] != "" {
					hi, _ = strconv.Atoi(m[2])
				}
				if hi < lo || hi-lo > 4096 {
					hi = lo
				}
				for c := lo; c <= hi; c++ {
					codes = append(codes, c)
				}
			}
		}
	}
	return codes
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

// exitWordHelpFixture is a tool's help whose exit paragraph lists code 125 and
// a 0-124 range across several lines, the hand-rolled shape, so the walk can be
// shown reading the published table rather than a table passed in.
const exitWordHelpFixture = `nova-demo: runs work in a disposable place
usage:
  nova-demo run -- <cmd>

exit codes: 0 done, 1 it ran and said no (findings, a threshold), 2 could not run;
  the wrapped command's own status, 0-124, passed through, and a
  DONE line says which came back;
  125 nova-demo refused before the command ran, a usage error included

example:
  nova-demo run -- true
`

// TestExitWordReadsExitTable is the witness for the table the walk reads from
// help: a multi-line paragraph and a `lo-hi` range are read whole, a code above
// 2 passes only where the help publishes it, and an unlisted code is refused
// naming the site.
func TestExitWordReadsExitTable(t *testing.T) {
	t.Parallel()

	codes := exitWordExitCodes(exitWordHelpFixture)
	assert.Contains(t, codes, 125, "an explicit code above 2 is read")
	assert.Contains(t, codes, 42, "a range names every code it spans")

	assert.Empty(t, exitWordAnswers(125, "RUN FAILED\n", "", codes), "a listed code above 2 passes")
	assert.Contains(t, exitWordAnswers(130, "RUN FAILED\n", "", codes), "is above 2 and not in the verb's exit table", "an unlisted code above 2 is refused")
	assert.Contains(t, exitWordAnswers(3, "RUN FAILED\n", "", exitWordExitCodes("usage: nova-demo run\n")), "is above 2 and not in the verb's exit table", "help that publishes no table refuses every code above 2")
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
