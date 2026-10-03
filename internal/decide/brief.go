package decide

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The brief decision (SPEC-NOVA-DECIDE section 9): a card's text alone, read as
// a flash child with no memory reads it, before the card is added. Six nouls ask
// whether the card says what such a child needs (the repository and branch, the
// files and lines, the gate, the commit message, what to report, one task), a
// choice names the step a child could read two ways, a choice estimates the
// minutes, and converges is the probability that the child ends with a commit
// that does the task and passes the gate on its first attempt. Its outcome is
// the card's end in the sprint: landed on its first attempt, landed after a
// rework, or dropped.

// BriefName is the brief decision's name in the record.
const BriefName = "brief"

// The brief's outcome labels: how the card ended in the sprint.
const (
	BriefLanded   = "landed"   // landed on its first attempt
	BriefReworked = "reworked" // landed on a later attempt
	BriefDropped  = "dropped"  // dropped by the coordinator
)

// briefNeeds are the nouls a card fails when their p of yes is under 0.5, in the
// order a refusal names them.
var briefNeeds = []string{"repo_branch", "files_named", "gate_stated", "commit_stated", "report_stated", "one_thing"}

// noAmbiguity is the ambiguous_step option that names no step.
const noAmbiguity = "none"

// briefSteps is how many numbered steps ambiguous_step names one by one.
const briefSteps = 12

// BriefWidth is how many cards a brief batch asks at once, in nova-sprint add and as
// nova-decide brief's --width default.
const BriefWidth = 8

// BriefSchema is the brief's nine questions.
func BriefSchema() Schema {
	steps := map[string]string{
		noAmbiguity:  "no step is ambiguous: every step says what to do and when it is done",
		"unnumbered": "an instruction outside any numbered STEP, or in a step past STEP " + strconv.Itoa(briefSteps) + ", is ambiguous",
	}
	for n := 1; n <= briefSteps; n++ {
		steps["step-"+strconv.Itoa(n)] = fmt.Sprintf("STEP %d, or one of its sub-steps (STEP %d.1, ...), is the first ambiguous step", n, n)
	}
	return Schema{Name: BriefName, Questions: map[string]Question{
		"repo_branch": {Type: Noul, Instructions: "The CARD names the repository the work is in and the branch or base it starts from " +
			"(a REPO: and a BASE: line, or the same in words)."},
		"files_named": {Type: Noul, Instructions: "The CARD names where the work is: the files it changes, or a PATHS glob " +
			"of them, and inside them the functions, tests, sections or lines to change, so the child does not have to " +
			"search for where the work is. A glob in PATHS names files as well as a list does."},
		"gate_stated": {Type: Noul, Instructions: "The CARD states the gate: the exact commands the child runs to check its work " +
			"(go test, go vet, gofmt, make, a script), written out to run as given."},
		"commit_stated": {Type: Noul, Instructions: "The CARD states the commit message the child commits with " +
			"(a COMMIT: line, or the message itself in words)."},
		"report_stated": {Type: Noul, Instructions: "The CARD states what the child reports at its end: the result's shape, " +
			"or the pull request's title and what its body carries."},
		"one_thing": {Type: Noul, Instructions: "The CARD's task is one thing: one change toward one end (a card whose steps " +
			"each make one change, in order, toward one end is one thing), not several unrelated tasks."},
		"ambiguous_step": {Type: Choice, Instructions: "The first step of the CARD a flash child with no memory could read two ways, " +
			"or could not tell what to do or when it is done, if any.", Criteria: steps},
		"minutes": {Type: Choice, Instructions: "How long a capable flash child, given only this CARD and a checkout at its base, " +
			"takes to finish it, the gate included.", Criteria: map[string]string{
			"under-10": "under 10 minutes",
			"10-20":    "10 to 20 minutes",
			"20-45":    "20 to 45 minutes",
			"45-90":    "45 to 90 minutes",
			"90-180":   "90 minutes to 3 hours",
			"over-180": "over 3 hours",
		}},
		"converges": {Type: Noul, Instructions: "A flash child with no memory, given this CARD alone and a checkout at its base, " +
			"ends with a commit that does the task and passes the gate on its first attempt, without asking anything."},
	}}
}

// briefFrame is what the sprint hands the child beside the card
// (docs/SPEC-CARD-CONTRACT.md section 2), so a card is not asked to say it: the
// first calibration, asked over the card alone, named STEP 1 ("cd into the staged
// checkout JOB.md names") ambiguous on 612 of 613 tree cards.
const briefFrame = "FRAME (what the sprint gives the child beside the card, which the card need not say): " +
	"JOB.md in the job directory names the staged checkout's path (a full clone of the card's REPO at its BASE, " +
	"on the card's own branch), the commit it starts from, and how the child's commit and pull request leave the " +
	"machine (the sprint pushes and opens them); the rules file the card names is appended to the card the child reads."

// BriefState is the text the brief is asked over: the card, under its heading,
// then the frame the sprint gives every child.
func BriefState(card string) string {
	return "CARD (the whole brief a flash child with no memory is handed):\n" + strings.TrimRight(card, "\n") + "\n\n" + briefFrame + "\n"
}

// BriefOp is a card's brief decision's id in the record: <card>@brief-<8 hex>,
// the hex of the schema and the brief, so a brief rewritten after a refusal, or
// asked under a reworded schema, is a decision of its own and never a conflict
// with the one before it.
func BriefOp(card, brief string) string {
	return card + "@brief-" + Sum([]byte(BriefSchema().Hash() + "\n" + BriefState(brief)))[:8]
}

// BriefItem is a card's brief as one item of a batch (MakeAll).
func BriefItem(card, brief string) Item {
	return Item{ID: BriefOp(card, brief), State: BriefState(brief), Inputs: map[string]string{"card": card, "card_sha256": Sum([]byte(brief))}}
}

// Brief is what a brief decision says of its card.
type Brief struct {
	Converges float64  // p(converges)
	Minutes   string   // the minutes option chosen
	Ambiguous string   // the ambiguous_step option chosen: none, step-<n> or unnumbered
	Failed    []string // the questions the card fails: a need under 0.5 as name(p), a step as ambiguous_step:step-<n>(p)
}

// BriefOf reads a brief decision.
func BriefOf(d Decision) Brief {
	b := Brief{Converges: d.Answers["converges"].Prob("yes"), Minutes: d.Answers["minutes"].Value, Ambiguous: d.Answers["ambiguous_step"].Value}
	for _, q := range briefNeeds {
		if p := d.Answers[q].Prob("yes"); p < 0.5 {
			b.Failed = append(b.Failed, fmt.Sprintf("%s(%.2f)", q, p))
		}
	}
	if a := d.Answers["ambiguous_step"]; a.Value != noAmbiguity {
		b.Failed = append(b.Failed, fmt.Sprintf("ambiguous_step:%s(%.2f)", a.Value, a.Prob(a.Value)))
	}
	return b
}

// Line is the brief as one line of fields: p_converges, minutes and the failed
// questions ("-" when none), as add and lint --decide print it, marked
// uncalibrated=true: p(converges) is a rank no outcome of the brief record has
// yet supported a bar on (SPEC-NOVA-DECIDE section 9).
func (b Brief) Line() string {
	failed := strings.Join(b.Failed, ",")
	return fmt.Sprintf("p_converges=%.2f minutes=%s failed=%s uncalibrated=true", b.Converges, cmp.Or(b.Minutes, "-"), cmp.Or(failed, "-"))
}

// BriefBar is the bar on p(converges) a card is added at: Set is false when the
// sprint row's decide_brief_bar is empty (report only).
type BriefBar struct {
	At  float64
	Set bool
}

// ParseBriefBar reads decide_brief_bar as the sprint row holds it: empty is no
// bar, else a probability.
func ParseBriefBar(raw string) (BriefBar, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return BriefBar{}, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return BriefBar{}, fmt.Errorf("decide_brief_bar %q is not a probability in [0, 1]; set a decimal, or empty to report only", raw)
	}
	return BriefBar{At: v, Set: true}, nil
}

// Refuses says the bar refuses a card whose brief is b: it is set and p(converges) is under it.
func (bar BriefBar) Refuses(b Brief) bool { return bar.Set && b.Converges < bar.At }

// Briefs makes the brief decision of every card (id -> brief text) as one batch,
// in id order, at most width asks at a time, each within wait.
func Briefs(ctx context.Context, b Backend, cards map[string]string, record string, at time.Time, width int, wait time.Duration) ([]Made, error) {
	items := make([]Item, 0, len(cards))
	for _, id := range slices.Sorted(maps.Keys(cards)) {
		items = append(items, BriefItem(id, cards[id]))
	}
	return MakeAll(ctx, b, BriefSchema(), items, record, at, width, wait)
}

// CardPaths is the card files of a directory as nova-sprint add --brief-dir reads
// them: every *.md entry that is not a directory, in byte order of name, none below.
func CardPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// CardFiles is the cards a path names, id -> brief: a file is one card, a
// directory its CardPaths (add --brief-dir's cards); a card's id is its file's
// name without .md, and its brief the file's text with one trailing newline cut,
// as nova-sprint add stores it.
func CardFiles(path string) (map[string]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	files := []string{path}
	if fi.IsDir() {
		if files, err = CardPaths(path); err != nil {
			return nil, err
		}
	}
	cards := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		cards[strings.TrimSuffix(filepath.Base(f), ".md")] = strings.TrimSuffix(string(raw), "\n")
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("%s holds no *.md card file (a directory's cards are its *.md entries, none below it)", path)
	}
	return cards, nil
}

// LandLabel is the outcome a card's landing attaches to its brief: landed at
// attempt 1, reworked at a later one, with the attempt in the note.
func LandLabel(attempt string) (label, note string) {
	if n, err := strconv.Atoi(attempt); err == nil && n > 1 {
		return BriefReworked, "landed at attempt " + attempt
	}
	return BriefLanded, "landed at attempt " + cmp.Or(attempt, "1")
}

// End is how a card ended in the sprint: the outcome label its brief is given
// (BriefLanded, BriefReworked, BriefDropped) and a note.
type End struct{ Label, Note string }

// AttachBriefs attaches each card's end to its brief decision by the decision's
// exact id, the op add stored on the card (op -> End), in one write under the
// record's lock. An op the record does not hold, or a record that does not exist,
// is that op's error in failed and no record is made; the same label again
// changes nothing; another label is that op's *ConflictError; the other ops are
// attached.
func AttachBriefs(record string, ends map[string]End, at time.Time) (attached int, failed map[string]error, err error) {
	failed = map[string]error{}
	ds, err := Load(record) // read under the shared lock first: no record is made where there is none
	if err != nil {
		return 0, nil, err
	}
	missing := func(op string) error {
		return fmt.Errorf("the record %s holds no decision %s: %w", record, op, ErrUnknown)
	}
	if len(ds) == 0 {
		for op := range ends {
			failed[op] = missing(op)
		}
		return 0, failed, nil
	}
	stamp := at.UTC().Format(time.RFC3339)
	err = locked(record, func(ds []Decision) ([]line, error) {
		var add []line
		for _, op := range slices.Sorted(maps.Keys(ends)) {
			d, e := Find(ds, op), ends[op]
			switch {
			case d == nil:
				failed[op] = missing(op)
			case d.Outcome != nil && d.Outcome.Label == e.Label:
			case d.Outcome != nil:
				failed[op] = &ConflictError{fmt.Sprintf("decision %s is labelled %s already (at %s), not %s; an outcome is attached once", op, d.Outcome.Label, d.Outcome.At, e.Label)}
			default:
				add = append(add, line{Outcome: &Outcome{ID: op, Label: e.Label, Note: e.Note, At: stamp}})
			}
		}
		attached = len(add)
		return add, nil
	})
	return attached, failed, err
}
