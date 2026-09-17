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
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
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

// Decider is the one typed-decision call the scorer makes. *decide.Client
// satisfies it, and a test hands in a fake, so no test reaches the network.
type Decider interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// Scorer asks one typed score in [0,1] for a PR's likelihood to land clean.
// A nil Scorer on SweepInput leaves the sweep's enqueue order alone.
type Scorer interface {
	Score(f OrderFeatures) (decide.Answer, error)
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
}

// Score asks the provider the one land question and returns its typed answer.
func (d DecideScorer) Score(f OrderFeatures) (decide.Answer, error) {
	if d.Ask == nil {
		return decide.Answer{}, fmt.Errorf("pulse: order scorer has no provider")
	}
	answers, _, err := d.Ask.Decide(context.Background(), orderState(f), map[string]decide.Question{
		orderQuestion: {
			Instructions: "Score from 0 to 1 the likelihood this pull request lands clean on its first try, from the changed-files count, the additions and deletions, the packages it touches, its red head runs in the last day, its age in hours, and whether a group it joined failed before. 0 is a poison candidate; 1 is a small green PR that cannot fail.",
			Score:        []string{"0 poison", "1 unlikely", "2 even", "3 likely", "4 clean", "5 certain"},
		},
	})
	if err != nil {
		return decide.Answer{}, err
	}
	a, ok := answers[orderQuestion]
	if !ok {
		return decide.Answer{}, fmt.Errorf("pulse: the provider answered no %s score", orderQuestion)
	}
	return a, nil
}

// orderCandidate is one fresh green approval waiting to be enqueued, with the
// score the ordering decision gave it.
type orderCandidate struct {
	row   LedgerRow
	view  PRView
	score float64
}

// scoreBatch asks one typed score per candidate, prints one ORDER line each,
// and returns the batch in descending score. Ties keep the incoming order, so
// below the floor the existing (oldest-first) order stands exactly as it was.
func scoreBatch(batch []orderCandidate, scorer Scorer, floor float64, out io.Writer) []orderCandidate {
	for i := range batch {
		score, conf, below := orderScore(batch[i], scorer, floor)
		batch[i].score = score
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
// (or when the provider could not be asked) the PR scores 0.5 and the existing
// order decides among its ties.
func orderScore(c orderCandidate, scorer Scorer, floor float64) (score, conf float64, below bool) {
	a, err := scorer.Score(orderFeatures(c))
	if err != nil {
		return orderFallbackScore, 0, true
	}
	if a.Confidence < floor {
		return orderFallbackScore, a.Confidence, true
	}
	score = a.Score
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, a.Confidence, false
}

// orderFeatures lifts a candidate's public features out of its view.
func orderFeatures(c orderCandidate) OrderFeatures {
	return OrderFeatures{
		PR: c.row.PR, ChangedFiles: c.view.ChangedFiles, Additions: c.view.Additions,
		Deletions: c.view.Deletions, Packages: c.view.Packages, RedRuns: c.view.RedRuns,
		AgeHours: c.view.AgeHours, GroupFailed: c.view.GroupFailed,
	}
}

// orderState is the bounded, public state the question is asked over: counts
// and package names only, never a diff, never a private body.
func orderState(f OrderFeatures) string {
	pkgs := "-"
	if len(f.Packages) > 0 {
		pkgs = strings.Join(f.Packages, ",")
	}
	return fmt.Sprintf("pr=%d changed_files=%d additions=%d deletions=%d packages=%s red_head_runs_24h=%d age_hours=%.1f group_failed_before=%t",
		f.PR, f.ChangedFiles, f.Additions, f.Deletions, pkgs, f.RedRuns, f.AgeHours, f.GroupFailed)
}
