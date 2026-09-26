package card

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
)

// QUACK: THE PRIMARY CARDS OF A PROBE RUN (nova-tools#4307).
//
// A quack run is N one-file cards in one stream, each a primary the copy
// model fans out to the benches (rowan-new memory: primaries fan out
// consumer copies). The card is the smallest change a model can make in one
// turn: create docs/fixtures/quack-<sprint>-<id>.txt holding one line. This
// file renders that card from the template `nova-sprint quack cut` pushes
// (scratchpad/quack-issue.tmpl of 2026-09-26, the hand loop that pushed the
// morning's 100 primaries); the old per-bench probe that pinned a card to a
// bench and read s:<S>:card records was the pre-copy model and is gone.

// QuackInput is one primary: the run's sprint and stream, the card id
// (QuackID), the tier its ROUTE names, and the repository the card changes at
// BASE (a 40-hex base-sha).
type QuackInput struct {
	Sprint, Stream, ID, Tier string
	Repo, Base, BaseSHA      string
}

// quackDir is where a probe card writes its one file: a catalogued
// directory, so the new file stales no AGENTS map (quack run #3).
const quackDir = "docs/fixtures"

var quackIDRE = regexp.MustCompile(`^quack-[0-9]{3,}$`)

// QuackID is the id of the n-th primary of a run: quack-001, quack-002, ...
func QuackID(n int) string { return fmt.Sprintf("quack-%03d", n) }

// QuackTitle is the task title a primary carries: quack <id> <tier>.
func QuackTitle(id, tier string) string { return "quack " + id + " " + tier }

// QuackFixture is the one file a primary creates and the one line it holds.
func QuackFixture(sprint, id string) (path, line string) {
	return quackDir + "/quack-" + sprint + "-" + id + ".txt", "quack " + sprint + " " + id
}

// QuackIssue renders the primary's issue text, the template the push reads
// through taskcard.ParseIssue: the header lines a card carries (STREAM, WHO,
// KIND, TYPE, REPO, BASE, base-sha, PATHS, TEST, DEPENDS-ON, PRIORITY, ROUTE,
// EST, SOURCE, TASK), then the NO-SUBAGENTS, UNATTENDED, DO and DONE-WHEN
// lines the model reads.
func QuackIssue(in QuackInput) (string, error) {
	if !cardhdr.IsRoute(in.Tier) {
		return "", fmt.Errorf("tier %q is not %s", in.Tier, cardhdr.RouteList)
	}
	if !quackIDRE.MatchString(in.ID) {
		return "", fmt.Errorf("id %q is not quack-NNN", in.ID)
	}
	if !shaRE.MatchString(in.BaseSHA) {
		return "", fmt.Errorf("base-sha %q is not 40 lowercase hex", in.BaseSHA)
	}
	if in.Sprint == "" || in.Stream == "" || in.Repo == "" || in.Base == "" {
		return "", fmt.Errorf("a quack card names its sprint, stream, repo and base")
	}
	path, line := QuackFixture(in.Sprint, in.ID)
	var b strings.Builder
	for _, kv := range [][2]string{
		{"STREAM", in.Stream}, {"WHO", "any"}, {"KIND", "fix"}, {"TYPE", "code"}, {"REPO", in.Repo},
		{"BASE", in.Base}, {"base-sha", in.BaseSHA}, {"PATHS", path}, {"TEST", "none the probe writes one fixture line; the wrapper's diff is the check"}, {"DEPENDS-ON", "none"},
		{"PRIORITY", "100"}, {"ROUTE", in.Tier}, {"EST", "2"}, {"SOURCE", "quack"}, {"TASK", in.ID},
	} {
		b.WriteString(kv[0] + ": " + kv[1] + "\n")
	}
	b.WriteString("\n")
	b.WriteString("NO-SUBAGENTS: work in this session only; do not spawn an Explore, Task or child agent.\n")
	b.WriteString("UNATTENDED: never ask a question and never offer to proceed; decide, and record the decision in RESULT.md.\n")
	fmt.Fprintf(&b, "DO: create %s containing exactly the line %q, commit it, write RESULT.md with CHECK: pass, RED: none, GREEN: none, and exit; do not run tests, do not read other files, do not explore.\n", path, line)
	b.WriteString("\n")
	fmt.Fprintf(&b, "DONE-WHEN: the file %s exists in the commit and its whole content is the single line %q; nothing else changes.\n", path, line)
	return b.String(), nil
}
