package pulse

// `cut --kind`: the one cutter, and the only place a card number comes from.
//
// Pit stop 3, bugs 1 and 12 (issue #828, classes B and F): read and replay cards were
// written by hand and by four different shell scripts, each with its own numbering, its own
// line 1 and its own idea of where the card goes. Two of them collided. The rule that
// retires the class: every coordinator action has a verb, `cut` is the only cutter, the
// number comes only from the state file under the lock (number.go), and there is no
// `--number` flag to pass one in.
//
// Four kinds, four line-1 shapes, and line 1 is the contract the harvest matches:
//
//   read    RESULT: CARD-<n> read of <repo> PR<pr> at <head> (<title>)
//   fix     RESULT: CARD-<n> <repo> #<issue> fixed with its red test first: <title>
//   replay  RESULT: CARD-<n> <repo> replays <names> named at spec lines <lines>, red first
//   spec    RESULT: CARD-<n> <repo> spec: <title>
//
// `cut` without `--kind` is the pool-driven cutter in cut.go and is untouched by any of this.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CutKinds are the four kinds this cutter knows, in the order help prints them.
var CutKinds = []string{"read", "fix", "replay", "spec"}

// CutKindInput is everything `cut --kind` takes. Flag parsing lives in cmd/nova-pulse.
type CutKindInput struct {
	Kind     string
	Repo     string // owner/name; line 1 names the repo for every kind
	PR       int    // read
	Head     string // read
	Issue    int    // fix
	Title    string // fix, spec, and the parenthesised title of a read
	BodyFile string // fix, spec: the numbered steps this card carries
	Prior    string // fix: what a prior attempt did, so the worker never repeats it
	// PriorCard is the card this one supersedes. When it is set and Prior is not, the
	// prior-attempts line is read from that card's own failure history (cause.go), which is
	// how a refill cuts the next attempt WITHOUT a person retyping what the last one hit.
	PriorCard string
	Names     string // replay: the replay names, comma separated
	SpecLines string // replay: the spec lines the replays are named at
	Out       string // the directory the card is written into
	Queue     string // the queue directory holding the state file and its lock
	Version   string // this build's identity; it goes on the card's CUT stamp (stamp.go)
	Stdout    io.Writer
	Stderr    io.Writer
}

// CutKind writes one card of one kind under the next number and prints one line. It returns
// 0 when the card was written and 2 when the invocation was refused.
func CutKind(in CutKindInput) int {
	if problem := cutKindProblem(in); problem != "" {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", problem)
		return 2
	}
	body := ""
	if in.BodyFile != "" {
		raw, err := os.ReadFile(in.BodyFile)
		if err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: --body-file %s: %s (pass a readable file of the card's numbered steps)\n", oneline.Field(in.BodyFile), oneline.Err(err))
			return 2
		}
		body = strings.TrimRight(string(raw), "\n")
	}
	// Class Q (#828): a step that says "whole" is a step that costs a 5,000-line spec every
	// time it runs. A spec reaches a card as a line range or as a rule, never entire.
	if problem := wholeProblem(body); problem != "" {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", problem)
		return 2
	}
	if strings.TrimSpace(in.Prior) == "" && strings.TrimSpace(in.PriorCard) != "" {
		in.Prior = PriorLine(in.Queue, in.PriorCard)
	}
	n, err := NextCardNumber(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s (the number comes only from the state file under %s)\n", oneline.Err(err), oneline.Field(in.Queue))
		return 2
	}
	card := Stamp(renderKindCard(in, n, body), in.Version)
	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}
	name := fmt.Sprintf("card-%d.md", n)
	if err := os.WriteFile(filepath.Join(in.Out, name), []byte(card), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s (pass a writable --out directory)\n", oneline.Field(name), oneline.Err(err))
		return 2
	}
	fmt.Fprintf(in.Stdout, "CUT CARD card=%s kind=%s number=%d out=%s stamp=%s\n",
		oneline.Field(name), oneline.Field(in.Kind), n, oneline.Field(in.Out), CheckStamp(card).Version)
	return 0
}

// wholeWords are the two ways a card asks for a whole file. Pit stop 3, class Q (#828):
// SPEC-WORK is five thousand lines, and "read the spec" put every one of them in a worker's
// window for a rule that lives on one row. `nova-review packet --rule spec:n` is how spec
// text reaches a card, and `--spec-lines L1-L2` is how a range does; a card that names
// neither is refused here, at the only place cards are made.
var wholeWords = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bwhole\b`),
	regexp.MustCompile(`(?i)\bread\b.{0,80}?\bentirely\b`),
}

// wholeProblem is class Q's one refusal line, naming the step it read it on.
func wholeProblem(body string) string {
	for i, line := range strings.Split(body, "\n") {
		for _, re := range wholeWords {
			if m := re.FindString(line); m != "" {
				return fmt.Sprintf("step line %d says %s: a spec reaches a card by line range or by rule, never entire (pass --spec-lines L1-L2, or name the rule with nova-review packet --rule spec:n)",
					i+1, oneline.Field(m))
			}
		}
	}
	return ""
}

// cutKindProblem is every refusal this cutter has, each naming its remedy.
func cutKindProblem(in CutKindInput) string {
	switch {
	case !cutKindKnown(in.Kind):
		return fmt.Sprintf("--kind %s is not one of %s (pass one of the four kinds)", oneline.Field(in.Kind), strings.Join(CutKinds, "|"))
	case strings.TrimSpace(in.Repo) == "":
		return "--repo is required; line 1 of every card names the repo (pass --repo <owner>/<name>)"
	case strings.TrimSpace(in.Queue) == "":
		return "--queue is required; the card number comes only from its state file (pass --queue <dir>)"
	case strings.TrimSpace(in.Out) == "":
		return "--out is required (pass the directory the card is written into, usually <queue>/pending)"
	}
	switch in.Kind {
	case "read":
		switch {
		case in.PR <= 0:
			return "--pr is required for a read card (pass the pull request number)"
		case strings.TrimSpace(in.Head) == "":
			return "--head is required for a read card; a verdict on an unnamed head cannot be revalidated (pass --head <sha>)"
		}
	case "fix":
		switch {
		case in.Issue <= 0:
			return "--issue is required for a fix card (pass the issue number the fix closes)"
		case strings.TrimSpace(in.Title) == "":
			return "--title is required for a fix card (pass the one-line contract the card is measured by)"
		}
	case "replay":
		switch {
		case strings.TrimSpace(in.Names) == "":
			return "--names is required for a replay card (pass the replay names, comma separated)"
		case strings.TrimSpace(in.SpecLines) == "":
			return "--spec-lines is required for a replay card (pass the spec lines the replays are named at, L1-L2)"
		}
	case "spec":
		if strings.TrimSpace(in.Title) == "" {
			return "--title is required for a spec card (pass the amendment's one-line subject)"
		}
	}
	return ""
}

func cutKindKnown(kind string) bool {
	for _, k := range CutKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// renderKindCard writes line 1, the source line, the prior attempt if there is one, the
// kind's own instruction and the body.
func renderKindCard(in CutKindInput, n int, body string) string {
	repo := repoShort(in.Repo)
	var b strings.Builder
	switch in.Kind {
	case "read":
		fmt.Fprintf(&b, "RESULT: CARD-%d read of %s PR%d at %s (%s)\n", n, repo, in.PR, oneline.Field(in.Head), oneline.Escape(in.Title))
		fmt.Fprintf(&b, "SOURCE: %s %s#%d\n", in.Repo, in.Repo, in.PR)
	case "fix":
		fmt.Fprintf(&b, "RESULT: CARD-%d %s #%d fixed with its red test first: %s\n", n, repo, in.Issue, oneline.Escape(in.Title))
		fmt.Fprintf(&b, "SOURCE: %s %s#%d\n", in.Repo, in.Repo, in.Issue)
	case "replay":
		fmt.Fprintf(&b, "RESULT: CARD-%d %s replays %s named at spec lines %s, red first\n", n, repo, oneline.Field(in.Names), oneline.Field(in.SpecLines))
		fmt.Fprintf(&b, "SOURCE: %s %s\n", in.Repo, oneline.Field(in.Names))
	case "spec":
		fmt.Fprintf(&b, "RESULT: CARD-%d %s spec: %s\n", n, repo, oneline.Escape(in.Title))
		fmt.Fprintf(&b, "SOURCE: %s spec\n", in.Repo)
	}
	if p := strings.TrimSpace(in.Prior); p != "" {
		fmt.Fprintf(&b, "Prior attempts: %s\n", oneline.Escape(p))
	}
	b.WriteString(kindInstruction(in))
	if body != "" {
		b.WriteString(body + "\n")
	}
	return b.String()
}

// kindInstruction is the one paragraph a kind always carries, whatever its body says.
func kindInstruction(in CutKindInput) string {
	switch in.Kind {
	case "read":
		return fmt.Sprintf(`Read pull request %d of %s at head %s. Quote the rule beside every line you hold.
Do not run go build, go test or any toolchain; read and write only.
Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE, then exactly one verdict line:
PR%d: APPROVE|HOLD head=%s repo=%s
`, in.PR, in.Repo, in.Head, in.PR, in.Head, in.Repo)
	case "fix":
		return fmt.Sprintf(`Fix %s #%d with its reproducing test first: the red line, then the green line, one row per item.
A fix whose diff carries no test is not admitted.
Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE or ABSTAIN <why>, then BRANCH <name> and REPO %s.
`, in.Repo, in.Issue, in.Repo)
	case "replay":
		return fmt.Sprintf(`Write the named replays red first: each one proven able to fail by a mutation before it is made green.
The names are %s and the spec lines they are named at are %s.
Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE or ABSTAIN <why>, then BRANCH <name> and REPO %s.
`, in.Names, in.SpecLines, in.Repo)
	default:
		return fmt.Sprintf(`Amend the spec: numbered rules, each with the test that makes it red, and no rule softened to match code.
Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE or ABSTAIN <why>, then BRANCH <name> and REPO %s.
`, in.Repo)
	}
}

// repoShort is owner/name as line 1 says it: the name alone.
func repoShort(repo string) string {
	if _, name, ok := strings.Cut(repo, "/"); ok && name != "" {
		return name
	}
	return repo
}
