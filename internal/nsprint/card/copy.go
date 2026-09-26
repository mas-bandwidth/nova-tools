package card

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A consumer copy's card (nova-tools #3929; rowan-new specs/table-moves.md):
// card deal cuts a copy of a stream's primary onto a consumer (a bench or a
// friend), and the copy's record (task:<primary>~<n>) carries the primary's
// fields. RenderCopy turns that record into a card file the card linter
// accepts, so a bench runs a copy like any card: its KIND decides the
// brief (read: read the PR at its head and end with a score; fix: close the
// read's finding; work: the primary's own task). No copy's model runs
// `nova-sprint card end` (#4227, #4270): a sandboxed swarm model cannot,
// and the boundary says it never talks to Redis. The wrapper's end
// (CopyLedger.End) does it from what the model left: a work copy's commit
// (push the branch, open the PR, end with the PR and the head); a read
// copy's RESULT.md line 2, the SCORE line (end with the score, the gates
// and the finding; ABSTAIN or no line is a typed fail); a fix copy's commit
// on the PR's branch (push it to that branch, end with the PR and the new
// head).

// CopyCard is the fields of a copy's record the card renders from.
type CopyCard struct {
	ID, Primary, Leg, Kind           string
	Repo, PR, Head, Base, BaseSHA    string
	Paths, DoneWhen, Title, Origin   string
	Stream, Finding, Route, Consumer string
	// Review is the REVIEW line of the verdict that moved the primary out
	// of review (#4072), carried to its next copy; "" when it never failed.
	Review string
	// Body is the issue text the primary carries (TM.CARRY takes it to the copy).
	Body string
	// Branch is the PR's head branch, the work copy's branch the primary
	// carries once its PR is open (TM.CARRY takes it to the copy): what a
	// fix copy commits on and the wrapper pushes to.
	Branch string
}

// CopyCardFrom reads a copy's record (HGETALL task:<copy>) as a CopyCard.
func CopyCardFrom(id string, rec map[string]string) CopyCard {
	return CopyCard{ID: id, Primary: rec["primary"], Leg: rec["leg"], Kind: rec["kind"], Repo: rec["repo"],
		PR: rec["pr"], Head: rec["head"], Base: rec["base"], BaseSHA: rec["base_sha"], Paths: rec["paths"],
		DoneWhen: rec["done_when"], Title: rec["title"], Origin: rec["origin"], Stream: rec["stream"],
		Finding: rec["finding"], Route: rec["route"], Consumer: rec["consumer"], Review: rec["review"], Body: rec["body"],
		Branch: rec["branch"]}
}

// ScoreLine is the read copy's one typed line, RESULT.md line 2 (#4270):
//
//	SCORE N/10 gates=ci:<green|red>,base:<ok|behind>,scope:<ok|over> finding=<one line>
//
// N is 1-10; gates and finding may be absent (a 10 has no gap to name),
// but gates present must be the three named gates in that order.
const ScoreLine = "SCORE N/10 gates=ci:<green|red>,base:<ok|behind>,scope:<ok|over> finding=<one line>"

// Score is a parsed ScoreLine.
type Score struct {
	N       int
	Gates   string
	Finding string
}

var (
	scoreRE = regexp.MustCompile(`^SCORE\s+([0-9]{1,2})/10(?:\s+gates=(\S+))?(?:\s+finding=(.*))?\s*$`)
	gatesRE = regexp.MustCompile(`^ci:(green|red),base:(ok|behind),scope:(ok|over)$`)
)

// ParseScore reads a ScoreLine; false for anything else (a missing line,
// another word, N outside 1-10, gates of another shape).
func ParseScore(line string) (Score, bool) {
	m := scoreRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Score{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > 10 {
		return Score{}, false
	}
	if m[2] != "" && !gatesRE.MatchString(m[2]) {
		return Score{}, false
	}
	return Score{N: n, Gates: m[2], Finding: oneLine(m[3])}, true
}

var labelBad = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// CopyLabel is the card label of a copy id: <primary>~<n> as <primary>.c<n>,
// any other character a label may not hold as '-'.
func CopyLabel(id string) string {
	return labelBad.ReplaceAllString(strings.Replace(id, "~", ".c", 1), "-")
}

// copyKind is the card KIND a copy runs as: read and fix are RESULT kinds;
// a work copy keeps its primary's kind when card push would accept it,
// maps a classification kind through KindMap, else runs as a model card.
func copyKind(c CopyCard) string {
	switch c.Leg {
	case "read":
		return typedrec.KindRead
	case "fix":
		return typedrec.KindFix
	}
	if checkKind(c.Kind) == nil && c.Kind != "" {
		return c.Kind
	}
	if k, ok := KindMap[c.Kind]; ok {
		return k
	}
	return KindModel
}

// RenderCopy is the card file of a copy. It refuses a record that lacks
// what the card needs (#3911: the primary carries it from push and from
// its work copy's end), never guessing a field.
func RenderCopy(c CopyCard) ([]byte, error) {
	var missing []string
	need := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	need("repo", c.Repo)
	need("base", c.Base)
	need("base_sha", c.BaseSHA)
	need("paths", c.Paths)
	if c.Leg == "read" || c.Leg == "fix" {
		need("pr", c.PR)
		need("head", c.Head)
	} else {
		need("done_when", c.DoneWhen)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("copy %s has no %s (the primary carries them: #3911)", c.ID, strings.Join(missing, ", "))
	}
	full, err := prkey.Full(c.Repo)
	if err != nil {
		return nil, err
	}
	label := CopyLabel(c.ID)
	prRef := prkey.Name(c.Repo) + "#" + c.PR
	done := c.DoneWhen
	// baseSHA is the sha the job's repo/ is staged at (the stage checks out
	// base-sha): the primary's base, or the PR's head for a fix copy, which
	// commits on top of the PR.
	baseSHA := c.BaseSHA
	var body string
	switch c.Leg {
	case "read":
		// The model writes one typed line and never runs nova-sprint
		// (#4270): the wrapper (CopyLedger.End) records the SCORE line on
		// the PR record at the head and ends the copy from RESULT.md.
		done = fmt.Sprintf("RESULT.md line 2 is the SCORE line for %s at head %s; the wrapper records it on pr:%s:%s and ends this copy",
			prRef, c.Head, prkey.Name(c.Repo), c.PR)
		body = fmt.Sprintf("Read %s at head %s against %s@%s: CI at head, base, scope, then a score 1-10.\n"+
			"A score under 10 names each gap and the work that closes it. A read edits nothing inside PATHS.\n"+
			"RESULT-FORMAT: RESULT.md in the job dir, outside repo/: line 1 is line 1 of this card verbatim; line 2 is exactly\n"+
			"  %s\n"+
			"or, when you could not read it:\n"+
			"  ABSTAIN <why>\n"+
			"Write RESULT.md and exit. Never run nova-sprint, never push, never comment on the PR: the wrapper reads line 2 and ends this copy.\n",
			prRef, c.Head, c.Base, c.BaseSHA, ScoreLine)
	case "fix":
		// The model commits the fix on the PR's branch in repo/ and exits
		// (#4270): repo/ is staged at the PR's head (base-sha below), the
		// wrapper pushes the commit to the PR's branch and ends the copy
		// with the new head.
		baseSHA = c.Head
		branch := oneLine(c.Branch)
		if branch == "" {
			branch = "the PR's branch"
		}
		done = fmt.Sprintf("the fix is committed on top of %s's head %s in repo/; the wrapper pushes it to %s and ends this copy with the new head",
			prRef, c.Head, branch)
		var sb strings.Builder
		fmt.Fprintf(&sb, "Fix %s at head %s: the read found: %s\n", prRef, c.Head, oneLine(c.Finding))
		sb.WriteString("NO-SUBAGENTS: " + taskcard.LineNoSubagents + "\n")
		sb.WriteString("WALL: " + taskcard.LineWall + "\n")
		sb.WriteString("TESTS: " + taskcard.LineTests + "\n")
		sb.WriteString("UNATTENDED: " + taskcard.LineUnattended + "\n")
		fmt.Fprintf(&sb, "COMMIT: repo/ is checked out at the PR's head %s, which is %s; change only PATHS, close the finding and commit on top of that head with the finding's summary as the first line; never push and never open a PR; the wrapper pushes your commit to %s and ends this copy with the new head; an uncommitted change counts as NO-COMMIT and the card fails.\n",
			c.Head, branch, branch)
		sb.WriteString("RESULT-FORMAT: RESULT.md in the job dir, outside repo/: line 1 is line 1 of this card verbatim; line 2 is DONE, ABSTAIN <why> or BLOCKED <why>.\n")
		body = sb.String()
	default:
		// The work copy's card is the primary's harness card (#3911): the
		// standard lines, the commit contract and the issue text quoted. The
		// wrapper ends the copy from the commit (#4227, CopyLedger.End:
		// push, PR, card end --ok --pr --head), so the model is not told to
		// run card end, which a sandboxed swarm model cannot; the COMMIT
		// line is its whole contract.
		n, _ := CopyNumber(c.ID)
		branch := WrapperBranch(CopySprint, CopyCardLabel(c.ID), n)
		var sb strings.Builder
		sb.WriteString("NO-SUBAGENTS: " + taskcard.LineNoSubagents + "\n")
		sb.WriteString("WALL: " + taskcard.LineWall + "\n")
		sb.WriteString("TESTS: " + taskcard.LineTests + "\n")
		sb.WriteString("UNATTENDED: " + taskcard.LineUnattended + "\n")
		sb.WriteString("OUTPUT: " + taskcard.LineOutput + "\n")
		fmt.Fprintf(&sb, "COMMIT: make your change in repo/ on a new branch %s (git checkout -b %s) and commit it there with the DONE-WHEN summary as the first line; never push and never open a PR; the wrapper pushes the branch and opens the PR from your commit; an uncommitted change counts as NO-COMMIT and the card fails.\n", branch, branch)
		sb.WriteString("RESULT-FORMAT: RESULT.md in the job dir, outside repo/: line 1 is line 1 of this card verbatim; line 2 is DONE, ABSTAIN <why> or BLOCKED <why>.\n")
		fmt.Fprintf(&sb, "DO: the work is the issue text quoted below (%s): change only PATHS, make DONE-WHEN hold, commit, write RESULT.md per RESULT-FORMAT, and exit; do not read other files, do not explore.\n", oneLine(c.Origin))
		sb.WriteString("\n---\n")
		sb.WriteString(taskcard.Quote(strings.TrimSpace(c.Body)))
		body = sb.String()
	}
	route := c.Route
	if route != RoutePro {
		route = RouteFlash
	}
	if c.Leg == "read" {
		route = RoutePro
	}
	sha := c.Head
	if sha == "" {
		sha = c.BaseSHA
	}
	if len(sha) > 12 {
		sha = sha[:12]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=%s\n", label, sha)
	line := func(k, v string) {
		if v = oneLine(v); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	line("KIND", copyKind(c))
	line("BASE", c.Base)
	line("base-repo", "https://github.com/"+full)
	line("base-sha", baseSHA)
	line("PATHS", c.Paths)
	line("DEPENDS-ON", "none")
	line("DONE-WHEN", done)
	line("ROUTE", route)
	line("STREAM", c.Stream)
	line("ORIGIN", c.Origin)
	line("TASK", c.Title)
	line("COPY", c.ID)
	line("PRIMARY", c.Primary)
	if c.PR != "" {
		line("PR", full+"#"+c.PR)
	}
	line("HEAD", c.Head)
	if c.Leg == "fix" {
		line("BRANCH", c.Branch)
	}
	b.WriteString("\n")
	b.WriteString(body)
	if r := oneLine(c.Review); r != "" {
		// the last copy failed and a review sent the card on: this copy reads why
		fmt.Fprintf(&b, "\nAn earlier copy of this card failed and went to review; the verdict:\n  %s\n", r)
	}
	return []byte(b.String()), nil
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// LintCard is the card linter card push runs (required keys, label,
// base-sha, DEPENDS-ON, ROUTE, KIND, the repository check): nil when the
// body would be admitted.
func LintCard(ctx context.Context, body []byte) error {
	if len(body) == 0 {
		return errors.New("empty card")
	}
	_, err := lint(ctx, body)
	return err
}
