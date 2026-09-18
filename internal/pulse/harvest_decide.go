package pulse

// The harvest's one typed decision per finished job (nova-tools #896, card 8336).
//
// A done card is read by its own two lines and its BRANCH line, and then, before
// any push, classified by one typed choice behind a floor: fixed, already-fixed,
// no-change, failed or off-branch. The decision advises; the harvest decides.
// fixed and failed push as today; no-change and already-fixed push nothing and
// are marked harvested, already-fixed naming the test the RESULT.md red: line
// carries; off-branch pushes nothing and prints the remedy. Below the floor the
// class is unknown and the harvest runs the path it always ran.
//
// It makes no model call of its own: every decision comes through Decider, the
// shipped *decide.Client or a test's fake, so no test reaches the network and the
// key is only ever read by decide.New from the environment.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The harvest asks its one class question through Decider, the same typed
// decision interface the gate declares in gate.go: one call, one class choice.
// The shipped one is *decide.Client; a test passes a fake, so no test reaches
// the network.

// harvestClassOptions is the closed set of classes one finished job can be.
var harvestClassOptions = [...]string{"fixed", "already-fixed", "no-change", "failed", "off-branch"}

// harvestClassQuestions is the one typed choice harvest asks: which class the
// finished job's bounded state belongs to.
func harvestClassQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"class": {
			Instructions: "Classify this finished job from its RESULT.md: fixed (the branch carries the work the card asked for); already-fixed (the fix already landed on the base and the branch adds nothing); no-change (the branch changes no file); failed (the job did not finish the work); off-branch (the commits belong to another card or branch).",
			Choice: map[string]string{
				"fixed":         "the branch carries the work the card asked for",
				"already-fixed": "the fix already landed on the base; the branch adds nothing",
				"no-change":     "the branch changes no file",
				"failed":        "the job did not finish the work",
				"off-branch":    "the commits belong to another card or branch",
			},
		},
	}
}

// harvestClassState is the bounded public state one class decision is asked over:
// the RESULT.md first line, its BRANCH line, the commits the branch carries past
// its base and its files line. Nothing else is sent: never a secret, never a
// private body, only the record the harvest already read.
func harvestClassState(jobDir, branch string, resultLines []string) string {
	var b strings.Builder
	b.WriteString("RESULT.md first line: " + strings.TrimSpace(firstNonEmpty(resultLines)) + "\n")
	b.WriteString("BRANCH " + branch + "\n")
	b.WriteString("commits past base: " + commitsPastBase(jobDir) + "\n")
	b.WriteString("files: " + resultFiles(resultLines) + "\n")
	return b.String()
}

// resultFiles is the text after a RESULT.md files: line, else "-".
func resultFiles(lines []string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "files:") {
			return strings.TrimSpace(t[len("files:"):])
		}
		if strings.HasPrefix(low, "files ") {
			return strings.TrimSpace(t[len("files "):])
		}
	}
	return "-"
}

// resultNamedTest is the test an already-fixed RESULT.md names for the closer: the
// test before the -- separator on its red: line, else the first token of its
// green: line, else "-".
func resultNamedTest(lines []string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(strings.ToLower(t), "red:") {
			continue
		}
		rest := strings.TrimSpace(t[len("red:"):])
		if i := strings.Index(rest, " -- "); i >= 0 {
			rest = strings.TrimSpace(rest[:i])
		}
		if rest != "" {
			return rest
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(strings.ToLower(t), "green:") {
			continue
		}
		if f := strings.Fields(strings.TrimSpace(t[len("green:"):])); len(f) > 0 {
			return f[0]
		}
	}
	return "-"
}

// commitsPastBase is the count the branch carries past its base, from the job's
// clone: the first base ref that resolves to a count, else "-". An unreadable
// clone is an unknown count, never a guess.
func commitsPastBase(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	for _, ref := range []string{"origin/dev", "dev", "origin/main", "main"} {
		cmd := exec.CommandContext(ctx, "git", "rev-list", "--count", ref+"..HEAD")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if n := strings.TrimSpace(string(out)); n != "" {
			return n
		}
	}
	return "-"
}

// harvestClass is one finished job's decision: the class, its confidence, the
// below list the line prints and the test an already-fixed closer uses.
type harvestClass struct {
	kind  string
	conf  float64
	below string
	test  string
}

// decideClass asks the one typed class question about a finished job. A provider
// error, a missing answer or an option outside the closed set leaves the class
// unknown: the harvest then runs today's path unchanged.
func (in HarvestInput) decideClass(jobDir, branch string, resultLines []string) harvestClass {
	c := harvestClass{kind: "unknown", below: "class"}
	if in.Decider == nil {
		return c
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	answers, _, err := in.Decider.Decide(ctx, harvestClassState(jobDir, branch, resultLines), harvestClassQuestions())
	if err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE class decision unavailable: %s\n", oneline.Err(err))
		return c
	}
	a, ok := answers["class"]
	if !ok || a.Type != "choice" {
		return c
	}
	c.conf = a.Confidence
	if a.Confidence < in.Floor {
		return c
	}
	for _, opt := range harvestClassOptions {
		if a.Choice == opt {
			c.kind, c.below = opt, "-"
			if opt == "already-fixed" {
				c.test = resultNamedTest(resultLines)
			}
			return c
		}
	}
	return c
}

// classFields is the class= conf= floor= below= tail every HARVEST line carries
// once the decision route is on. A decision below the floor prints unknown and
// names the question under it.
func classFields(c harvestClass, floor float64) string {
	return fmt.Sprintf("class=%s conf=%.2f floor=%.2f below=%s",
		field(c.kind), c.conf, floor, field(c.below))
}
