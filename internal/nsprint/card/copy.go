package card

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
// read's finding; work: the primary's own task). A read or fix copy ends
// with `nova-sprint card end --id <copy>`; a work copy never runs card end
// (#4227): its model commits on the copy's branch and exits, and the
// wrapper's end (CopyLedger.End) pushes the branch, opens the PR and ends
// the copy with the PR and the head.

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
}

// CopyCardFrom reads a copy's record (HGETALL task:<copy>) as a CopyCard.
func CopyCardFrom(id string, rec map[string]string) CopyCard {
	return CopyCard{ID: id, Primary: rec["primary"], Leg: rec["leg"], Kind: rec["kind"], Repo: rec["repo"],
		PR: rec["pr"], Head: rec["head"], Base: rec["base"], BaseSHA: rec["base_sha"], Paths: rec["paths"],
		DoneWhen: rec["done_when"], Title: rec["title"], Origin: rec["origin"], Stream: rec["stream"],
		Finding: rec["finding"], Route: rec["route"], Consumer: rec["consumer"], Review: rec["review"], Body: rec["body"]}
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
	end := "nova-sprint card end --id " + c.ID
	prRef := prkey.Name(c.Repo) + "#" + c.PR
	done := c.DoneWhen
	var body string
	switch c.Leg {
	case "read":
		done = fmt.Sprintf("%s --score N/10 --gates ci:<green|red>,base:<ok|behind>,scope:<ok|over> --finding '<one line>' records the SCORE line on pr:%s:%s at head %s",
			end, prkey.Name(c.Repo), c.PR, c.Head)
		body = fmt.Sprintf("Read %s at head %s against %s@%s: CI at head, base, scope, then a score 1-10.\n"+
			"A score under 10 names each gap and the work that closes it. A read edits nothing inside PATHS.\n"+
			"End with exactly one call:\n  %s --score N/10 --gates ci:<green|red>,base:<ok|behind>,scope:<ok|over> --finding '<the gap, one line>'\n"+
			"or, when you could not read it:\n  %s --fail '<why>'\n",
			prRef, c.Head, c.Base, c.BaseSHA, end, end)
	case "fix":
		body = fmt.Sprintf("Fix %s at head %s: the read found: %s\n"+
			"Push the fix to the PR's branch, then end with exactly one call:\n  %s --ok --pr %s --head <the new head sha>\n"+
			"or:\n  %s --fail '<why>'\n", prRef, c.Head, oneLine(c.Finding), end, full+"#"+c.PR, end)
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
	line("base-sha", c.BaseSHA)
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
