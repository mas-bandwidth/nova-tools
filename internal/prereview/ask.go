package prereview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// DiffCap is how much of the diff travels in the state. 64 KiB is not a round
// number picked for looking like one: it is the measured cap that covers 73% of
// harvested diffs whole, where 16 KiB covered 51% (Glenn 2026-09-21, "do not
// guess, measure"). A diff past it is truncated and the state SAYS it was, so a
// score given over part of a change is never mistaken for one given over all of
// it.
const DiffCap = 64 << 10

// ScoreLevels are the ten levels of the one question, in the fixed order they
// must always be sent in. Jev is order-sensitive: the same ten levels shuffled
// are a different question, and a score from one ordering cannot be compared
// with a score from another. Nothing may reorder this list without a new
// calibration run.
var ScoreLevels = []string{
	"1: the change does not do what its RESULT claims, or its test cannot fail",
	"2: the test asserts its own arithmetic rather than the code under test",
	"3: the claim is real but the evidence for it is not",
	"4: it tests something, but not the law the card names",
	"5: it tests the law weakly; a real regression could pass",
	"6: correct and narrow, with a control that does not clearly bite",
	"7: correct, with a control that bites, but scope or hygiene is off",
	"8: correct, in scope, exercises the generated code, control bites",
	"9: correct, in scope, well-derived, negative control verified",
	"10: exemplary: the law, the production path and a biting control, nothing else",
}

// LevelsVersion is the sha8 of a level list, the same hash RubricVersion is
// of ScoreLevels: a prompt that sends its own levels prints their version.
func LevelsVersion(levels []string) string {
	if levels == nil {
		levels = ScoreLevels
	}
	sum := sha256.Sum256([]byte(strings.Join(levels, "\x00")))
	return hex.EncodeToString(sum[:])[:8]
}

// RubricVersion is the sha8 of ScoreLevels (first 8 hex characters of sha256
// of the canonical ScoreLevels joined with NUL, matching TestScoreLevelOrderIsPinned).
func RubricVersion() string {
	sum := sha256.Sum256([]byte(strings.Join(ScoreLevels, "\x00")))
	return hex.EncodeToString(sum[:])[:8]
}

// SeedInstructions is the score question's instructions as the first posted
// run asked them. It is the seed prompt of the calibration harness
// (internal/jevcalib/prompts/fd94795e.txt is this string plus one newline), so
// it never changes: a new prompt is a new file, not an edit here.
const SeedInstructions = "Score this pull request from 1 to 10 as a reviewer would, where 1 is the worst and 10 the best. " +
	"The change is one cell of a conformance matrix: a single added test that must exercise the generated code " +
	"for one law, in the paths its card declares, with a negative control that really goes red. " +
	"A test that builds the bytes it then asserts, re-declares the type it claims to test, compares against a " +
	"reference pasted into the file, or asserts a literal true scores 3 or below however well it is written."

// ScoreQuestion is THE question set: exactly one score question. One question,
// one call, one price. The instructions restate the numbering the levels carry
// so that a provider answering with a number answers in 1-10 and not in level
// indexes; the raw answer is kept in the ledger either way (ScoreFromAnswer).
func ScoreQuestion() map[string]decide.Question {
	return ScoreQuestionWith(SeedInstructions, nil)
}

// ScoreQuestionWith is the one score question asked with a prompt file's
// instructions (nova-tools #2536). levels nil means ScoreLevels; a prompt that
// carries its own ten levels sends those, in its order.
func ScoreQuestionWith(instructions string, levels []string) map[string]decide.Question {
	if levels == nil {
		levels = ScoreLevels
	}
	return map[string]decide.Question{
		"score": {
			Instructions: instructions,
			Score:        levels,
		},
	}
}

// State is the evidence the one question is asked over: the card (or the pull
// request's RESULT when no card was given) and the diff. It carries no secret
// and no session transcript -- a reader reads the RESULT and the diff, and so
// does this.
func State(pr PR, card Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pull request %s#%d at head %s\n", pr.Repo, pr.Number, pr.Head)
	fmt.Fprintf(&b, "title: %s\n", oneLine(pr.Title))
	if card.Path != "" {
		fmt.Fprintf(&b, "card: %s\n", card.Path)
	} else {
		fmt.Fprintf(&b, "card: none; the RESULT below is the pull request body\n")
	}
	if len(card.Paths) > 0 {
		fmt.Fprintf(&b, "declared paths (%s): %s\n", card.PathsFrom, strings.Join(card.Paths, ", "))
	}
	if card.Symbol != "" {
		fmt.Fprintf(&b, "declared symbol: %s\n", card.Symbol)
	}
	fmt.Fprintf(&b, "changed files: %s\n", strings.Join(pr.Files, ", "))
	b.WriteString("\n--- RESULT ---\n")
	b.WriteString(resultText(pr, card))
	b.WriteString("\n--- DIFF ---\n")
	diff := pr.Diff
	if len(diff) > DiffCap {
		diff = diff[:DiffCap] + fmt.Sprintf("\n[diff truncated at %d bytes of %d]\n", DiffCap, len(pr.Diff))
	}
	b.WriteString(diff)
	return b.String()
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Asker is the one provider call a pass makes. It is an interface so that a
// test replays a recorded response and never dials anything.
type Asker interface {
	Ask(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, error)
}

// ClientAsker is the real one: internal/decide's client, which reads its key
// from the environment and never prints it. Last, when set, accumulates the
// usage of every call -- a refused call's too, because it cost what it cost --
// so the line can say what the score was bought for; the caller zeroes it.
type ClientAsker struct {
	Client *decide.Client
	Last   *decide.Usage
}

// Ask asks the provider once.
func (a ClientAsker) Ask(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, error) {
	answers, usage, err := a.Client.Decide(ctx, state, qs)
	if a.Last != nil {
		// Summed, not overwritten: in tool-PR mode one pull request is one
		// call per file group, and the line's cost is all of them.
		a.Last.InputTokens += usage.InputTokens
		a.Last.OutputTokens += usage.OutputTokens
		a.Last.HasInput = a.Last.HasInput || usage.HasInput
		a.Last.HasOutput = a.Last.HasOutput || usage.HasOutput
	}
	return answers, err
}

// Score asks the one question and returns the raw score and the confidence. An
// error is a pass with no score, not a pass with a zero.
func Score(ctx context.Context, a Asker, pr PR, card Card) (raw, conf float64, err error) {
	return ScoreWith(ctx, a, ScoreQuestion(), pr, card)
}

// ScoreWith asks the given score question (ScoreQuestionWith) over the pull
// request, per file group (ScoreGroups), and returns the lowest group's answer.
func ScoreWith(ctx context.Context, a Asker, qs map[string]decide.Question, pr PR, card Card) (raw, conf float64, err error) {
	g, err := ScoreGroups(ctx, a, qs, pr, card)
	return g.Raw, g.Conf, err
}
