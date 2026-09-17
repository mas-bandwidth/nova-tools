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
// Five kinds, five line-1 shapes, and line 1 is the contract the harvest matches:
//
//	read    RESULT: CARD-<n> read of <repo> PR<pr> at <head> (<title>)
//	fix     RESULT: CARD-<n> <repo> #<issue> fixed with its red test first: <title>
//	replay  RESULT: CARD-<n> <repo> replays <names> named at spec lines <lines>, red first
//	spec    RESULT: CARD-<n> <repo> spec: <title>
//	rebase  RESULT: CARD-<n> <repo> PR #<pr> rebased onto <base> with its conflicts resolved and its tests green: <title>
//
// `cut` without `--kind` is the pool-driven cutter in cut.go and is untouched by any of this.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CutKinds are the kinds this cutter knows, in the order help prints them.
var CutKinds = []string{"read", "fix", "replay", "spec", "rebase"}

// CutKindInput is everything `cut --kind` takes. Flag parsing lives in cmd/nova-pulse.
type CutKindInput struct {
	Kind      string
	Repo      string // owner/name; line 1 names the repo for every kind
	PR        int    // read, rebase
	Head      string // read
	Issue     int    // fix
	Title     string // fix, spec, rebase, and the parenthesised title of a read
	Branch    string // rebase: the branch rebased onto the base
	Base      string // rebase: the branch it is rebased onto
	BodyFile  string // fix, spec: the numbered steps this card carries
	Prior     string // fix: what a prior attempt did, so the worker never repeats it
	Names     string // replay: the replay names, comma separated
	SpecLines string // replay: the spec lines the replays are named at
	Out       string // the directory the card is written into
	Queue     string // the queue directory holding the state file and its lock
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
	n, err := NextCardNumber(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s (the number comes only from the state file under %s)\n", oneline.Err(err), oneline.Field(in.Queue))
		return 2
	}
	card := renderKindCard(in, n, body)
	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}
	name := fmt.Sprintf("card-%d.md", n)
	if err := os.WriteFile(filepath.Join(in.Out, name), []byte(card), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s (pass a writable --out directory)\n", oneline.Field(name), oneline.Err(err))
		return 2
	}
	fmt.Fprintf(in.Stdout, "CUT CARD card=%s kind=%s number=%d out=%s\n", oneline.Field(name), oneline.Field(in.Kind), n, oneline.Field(in.Out))
	return 0
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
	case "rebase":
		switch {
		case in.PR <= 0:
			return "--pr is required for a rebase card (pass the pull request number)"
		case strings.TrimSpace(in.Branch) == "":
			return "--branch is required for a rebase card; the card must name the branch it checks out (pass --branch <name>)"
		case strings.TrimSpace(in.Base) == "":
			return "--base is required for a rebase card; the card names the branch it is rebased onto (pass --base <branch>)"
		case strings.TrimSpace(in.Title) == "":
			return "--title is required for a rebase card (pass the pull request's own title)"
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
	case "rebase":
		fmt.Fprintf(&b, "RESULT: CARD-%d %s PR #%d rebased onto %s with its conflicts resolved and its tests green: %s\n",
			n, repo, in.PR, oneline.Field(in.Base), oneline.Escape(in.Title))
		fmt.Fprintf(&b, "SOURCE: %s %s#%d\n", in.Repo, in.Repo, in.PR)
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
	case "rebase":
		return fmt.Sprintf(rebaseSteps,
			rebasePreamble, in.Base, in.Branch, in.Branch, in.Branch, in.Base,
			in.Base, in.Base, in.Base, in.Base, in.Branch, in.Base)
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

// rebasePreamble is the one paragraph every rebase card opens with: no push, no PR, no
// GitHub calls; the harvester pushes the branch from ./repo. It is the hand loop's own
// words, kept because a worker already reads them.
const rebasePreamble = "You are a Go engineer resolving a rebase. Your working directory is the one printed by pwd at STEP 1; write everything under it: the clone at ./repo, notes at <working directory>/scratch/ using that absolute path. No push, no PR, no GitHub calls; the branch is pushed by the harvester from ./repo. Finish within 15 minutes."

// rebaseSteps is the rebase card's steps, with the branch and the base filled in. The
// conflict rule is the whole point: BOTH sides survive, no rule and no test is dropped,
// and a red test is fixed in the branch's own files rather than by deleting the base's.
const rebaseSteps = `%s
STEP 1. pwd && mkdir -p scratch && { [ -d repo ] || git clone -q https://github.com/mas-bandwidth/nova-tools.git repo; } && cd repo && git fetch -q origin %s %s && git checkout -q -b %s origin/%s && git log --oneline -1 | cat && git rebase origin/%s 2>&1 | tail -3
STEP 2. Resolve every conflict so that BOTH survive: %s's text (other PRs that landed) and this branch's additions; in a spec or a Lisp test file keep both in document order and never drop a rule or a deftest; in Go keep both changes and make it compile. After each file: git add <file>; then GIT_EDITOR=true git rebase --continue. Repeat until the rebase finishes. Record the conflicted files in scratch/conflicts.txt (absolute path).
STEP 3. If Go files changed: test -z "$(gofmt -l .)" && go vet ./... 2>&1 | tail -3 && go test $(git diff --name-only origin/%s -- "*.go" | xargs -n1 dirname | sort -u | sed "s|^|./|" | tr "\n" " ") 2>&1 | tail -6. If lisp/nova-work changed: (cd lisp/nova-work && ./run-tests.sh 2>&1 | tail -4). A red test in a touched package is fixed in this branch's own files, never by deleting %s's tests.
STEP 4. git log --oneline origin/%s..HEAD | cat && git status --short | head -5
STEP 5. Write RESULT.md (cd back to your working directory first): line 1 the RESULT line above; BRANCH: %s at <sha> in ./repo; REBASED: onto <%s sha>; conflicts: <files>; green: <the test tail, one line>. Nothing else.
`
