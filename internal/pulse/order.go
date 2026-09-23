package pulse

// #896: the sweep's enqueue is ordered by one typed score per PR. Before it
// enqueues a batch it asks TypeSafe Jev, once per candidate, how likely that PR
// is to land clean, from its size, the packages it touches, its head-run
// history and its age. The decision advises; the sweep decides. A nil Scorer
// leaves the enqueue order exactly as it was.

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultOrderFloor is the floor a sweep applies to the ordering score when it
// names none. Below it the answer is a suggestion: the PR scores 0.5 and keeps
// its place in the existing order.
const DefaultOrderFloor = 0.9

// orderQuestion is the one typed score the sweep asks per candidate PR.
const orderQuestion = "land"

// orderFallbackScore is what a PR scores when the decision is below the floor
// (or could not be asked): the middle, so the existing order stands among ties.
const orderFallbackScore = 0.5

// orderFileName is the durable decision record beside the ledger: one row per
// attempted ordering call, with its answer, the usage the provider reported and
// what the sweep then did with the PR.
const orderFileName = "order.tsv"

const orderHeader = "at\tpr\thead\tscore\tconf\tfloor\tbelow\tinput_tokens\toutput_tokens\terror\toutcome\n"

// orderFloor resolves the floor a sweep applies: an explicit value in [0,1]
// stands, 0 included (every answer stands); a negative, NaN or out-of-range
// value is DefaultOrderFloor.
func orderFloor(f float64) float64 {
	if math.IsNaN(f) || f < 0 || f > 1 {
		return DefaultOrderFloor
	}
	return f
}

// Scorer asks one typed score in [0,1] for a PR's likelihood to land clean.
// A nil Scorer on SweepInput leaves the sweep's enqueue order alone.
// It returns the usage the provider reported for the call, so the sweep can
// account for every attempted call, answered or not.
type Scorer interface {
	Score(f OrderFeatures) (decide.Answer, decide.Usage, error)
}

// DecideScorer is the shipped Scorer: one land score through the typed-decision
// route. The state is public-shaped features only (SPEC-DECIDE rule 4).
type DecideScorer struct {
	Ask Decider
}

// OrderFeatures are the bounded, public features of one candidate PR that the
// ordering question is built from.
type OrderFeatures struct {
	PR           int
	ChangedFiles int
	Additions    int
	Deletions    int
	Packages     []string
	RedRuns      int
	AgeHours     float64
	GroupFailed  bool
	HistoryKnown bool // RedRuns and GroupFailed were observed, not defaulted
}

// Score asks the provider the one land question and returns its typed answer.
func (d DecideScorer) Score(f OrderFeatures) (decide.Answer, decide.Usage, error) {
	if d.Ask == nil {
		return decide.Answer{}, decide.Usage{}, fmt.Errorf("pulse: order scorer has no provider")
	}
	answers, usage, err := d.Ask.Decide(context.Background(), orderState(f), map[string]decide.Question{
		orderQuestion: {
			Instructions: "Score from 0 to 1 the likelihood this pull request lands clean on its first try, from the changed-files count, the additions and deletions, the packages it touches, its red head runs in the last day, its age in hours, and whether a group it joined failed before; a feature given as unknown was not observed and says nothing either way. 0 is a poison candidate; 1 is a small green PR that cannot fail.",
			Score:        []string{"0 poison", "1 unlikely", "2 even", "3 likely", "4 clean", "5 certain"},
		},
	})
	if err != nil {
		return decide.Answer{}, usage, err
	}
	a, ok := answers[orderQuestion]
	if !ok {
		return decide.Answer{}, usage, fmt.Errorf("pulse: the provider answered no %s score", orderQuestion)
	}
	return a, usage, nil
}

// orderCandidate is one fresh green approval waiting to be enqueued, with the
// score the ordering decision gave it.
type orderCandidate struct {
	row     LedgerRow
	view    PRView
	score   float64
	scored  bool
	conf    float64
	floor   float64
	below   bool
	usage   decide.Usage
	err     error
	outcome string // enqueued | enqueue-failed, set by the sweep after the enqueue
}

// scoreBatch asks one typed score per candidate, prints one ORDER line each,
// and returns the batch in descending score. Ties keep the incoming order, so
// below the floor the existing (oldest-first) order stands exactly as it was.
func scoreBatch(batch []orderCandidate, scorer Scorer, floor float64, out io.Writer) []orderCandidate {
	for i := range batch {
		score, conf, below, usage, err := orderScore(batch[i], scorer, floor)
		c := &batch[i]
		c.score, c.conf, c.below, c.usage, c.err, c.floor, c.scored = score, conf, below, usage, err, floor, true
		line := fmt.Sprintf("ORDER pr=%d score=%.2f conf=%.2f floor=%.2f", batch[i].row.PR, score, conf, floor)
		if below {
			line += " below=" + orderQuestion
		}
		fmt.Fprintln(out, line)
	}
	sort.SliceStable(batch, func(i, j int) bool { return batch[i].score > batch[j].score })
	return batch
}

// orderScore applies the floor: above it the provider's score stands, below it
// (or when the provider could not be asked, or answered NaN) the PR scores 0.5
// and the existing order decides among its ties. A NaN confidence never passes
// the floor.
func orderScore(c orderCandidate, scorer Scorer, floor float64) (score, conf float64, below bool, usage decide.Usage, err error) {
	a, usage, err := scorer.Score(orderFeatures(c))
	if err != nil {
		return orderFallbackScore, 0, true, usage, err
	}
	if math.IsNaN(a.Confidence) {
		return orderFallbackScore, 0, true, usage, nil
	}
	if a.Confidence < floor || math.IsNaN(a.Score) {
		return orderFallbackScore, a.Confidence, true, usage, nil
	}
	score = a.Score
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, a.Confidence, false, usage, nil
}

// AppendOrderRecords appends one row per scored candidate to <queue>/order.tsv
// under the queue's lock: the decision, the reported usage ("-" where the
// provider reported none, never a zero) and the outcome the sweep reached.
func AppendOrderRecords(queue, at string, batch []orderCandidate) error {
	var b strings.Builder
	for _, c := range batch {
		if !c.scored {
			continue
		}
		in, out := "-", "-"
		if c.usage.HasInput {
			in = fmt.Sprint(c.usage.InputTokens)
		}
		if c.usage.HasOutput {
			out = fmt.Sprint(c.usage.OutputTokens)
		}
		errText := "-"
		if c.err != nil {
			errText = oneline.Err(c.err)
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\t%.4f\t%.4f\t%.4f\t%t\t%s\t%s\t%s\t%s\n", oneline.Field(at), c.row.PR,
			oneline.Field(orDash(c.row.Head)), c.score, c.conf, c.floor, c.below, in, out,
			oneline.Field(errText), oneline.Field(orDash(c.outcome)))
	}
	if b.Len() == 0 {
		return nil
	}
	unlock, err := lockQueue(queue)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(queue, orderFileName)
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("the order record cannot be written: %w", err)
	}
	defer f.Close()
	if os.IsNotExist(statErr) {
		if _, err := io.WriteString(f, orderHeader); err != nil {
			return fmt.Errorf("the order record header cannot be written: %w", err)
		}
	}
	if _, err := io.WriteString(f, b.String()); err != nil {
		return fmt.Errorf("the order record row cannot be written: %w", err)
	}
	return nil
}

// orderFeatures lifts a candidate's public features out of its view.
func orderFeatures(c orderCandidate) OrderFeatures {
	return OrderFeatures{
		PR: c.row.PR, ChangedFiles: c.view.ChangedFiles, Additions: c.view.Additions,
		Deletions: c.view.Deletions, Packages: c.view.Packages, RedRuns: c.view.RedRuns,
		AgeHours: c.view.AgeHours, GroupFailed: c.view.GroupFailed, HistoryKnown: c.view.HistoryKnown,
	}
}

// orderState is the bounded, public state the question is asked over: counts
// and package names only, never a diff, never a private body.
func orderState(f OrderFeatures) string {
	pkgs := "-"
	if len(f.Packages) > 0 {
		pkgs = strings.Join(f.Packages, ",")
	}
	red, group := "unknown", "unknown"
	if f.HistoryKnown {
		red, group = fmt.Sprint(f.RedRuns), fmt.Sprint(f.GroupFailed)
	}
	return fmt.Sprintf("pr=%d changed_files=%d additions=%d deletions=%d packages=%s red_head_runs_24h=%s age_hours=%.1f group_failed_before=%s",
		f.PR, f.ChangedFiles, f.Additions, f.Deletions, pkgs, red, f.AgeHours, group)
}
