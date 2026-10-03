package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// lint --decide is the brief decision of the card (docs/SPEC-NOVA-DECIDE.md section 9),
// the one nova-sprint add asks before it admits a card: p(converges), the minutes, and
// the questions a flash child with no memory would need answered that the card leaves
// open. It is asked through Jev with JEV_API_KEY from the environment, or answered from
// --decide-answers (the fixed backend, no network, no key), and recorded in
// --decide-record when one is named. It prints one LINT DECIDE line after the lint's own
// and never changes the lint's verdict: a bar is add's, read from the sprint row.

// lintDecideWait bounds the backend's answer, as nova-decide's own --timeout default.
const lintDecideWait = time.Minute

// lintDecide is the card's LINT DECIDE line.
func lintDecide(name string, raw []byte, answers, record string, getenv func(string) string, now time.Time) (string, error) {
	var b decide.Backend
	switch key := getenv(decide.JevSecret); {
	case answers != "":
		text, err := os.ReadFile(answers)
		if err != nil {
			return "", fmt.Errorf("--decide-answers: %w", err)
		}
		f, err := decide.ParseFixed(text)
		if err != nil {
			return "", fmt.Errorf("--decide-answers: %w", err)
		}
		b = f
	case key != "":
		b = decide.JevHTTP(key)
	default:
		return "", fmt.Errorf("Jev is asked with %s, which this environment does not hold", decide.JevSecret)
	}
	id, brief := strings.TrimSuffix(name, ".md"), strings.TrimSuffix(string(raw), "\n")
	ctx, cancel := context.WithTimeout(context.Background(), lintDecideWait)
	defer cancel()
	d, recorded := decide.Decision{ID: decide.BriefOp(id, brief)}, "no"
	if record == "" {
		answers, _, err := decide.Ask(ctx, b, decide.BriefSchema(), decide.BriefState(brief))
		if err != nil {
			return "", fmt.Errorf("the brief decision (backend %s): %w", b.Name(), err)
		}
		d.Answers = answers
	} else {
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			return "", err
		}
		made, err := decide.Briefs(ctx, b, map[string]string{id: brief}, record, now, 1, 0)
		if err != nil {
			return "", err
		}
		if made[0].Err != nil {
			return "", fmt.Errorf("the brief decision (backend %s): %w", b.Name(), made[0].Err)
		}
		d, recorded = made[0].Decision, map[bool]string{true: "existing", false: "new"}[made[0].Existing]
	}
	return fmt.Sprintf("LINT DECIDE card=%s op=%s %s recorded=%s", oneline.Field(name), oneline.Field(d.ID), oneline.Escape(decide.BriefOf(d).Line()), oneline.Field(recorded)), nil
}
