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
// and never changes the lint's verdict: a bar is add's, read from the sprint row. When
// the backend fails, the lint's verdict is printed all the same and then why the
// decision was not made, exit 2.

// lintDecideWait bounds the backend's answer, as nova-decide's own --timeout default.
const lintDecideWait = time.Minute

// lintBackend is the backend --decide asks: the fixed one from --decide-answers, else
// Jev with the key; no key and no answers file is refused before anything is linted.
func lintBackend(answers string, getenv func(string) string) (decide.Backend, error) {
	switch key := getenv(decide.JevSecret); {
	case answers != "":
		text, err := os.ReadFile(answers)
		if err != nil {
			return nil, fmt.Errorf("--decide-answers: %w", err)
		}
		f, err := decide.ParseFixed(text)
		if err != nil {
			return nil, fmt.Errorf("--decide-answers: %w", err)
		}
		return f, nil
	case key != "":
		return decide.JevHTTP(key), nil
	}
	return nil, fmt.Errorf("Jev is asked with %s, which this environment does not hold", decide.JevSecret)
}

// lintDecide is the card's LINT DECIDE line, asked of b.
func lintDecide(name string, raw []byte, b decide.Backend, record string, now time.Time) (string, error) {
	id, brief := strings.TrimSuffix(name, ".md"), strings.TrimSuffix(string(raw), "\n")
	if record != "" {
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), lintDecideWait)
	defer cancel()
	made, err := decide.Briefs(ctx, b, map[string]string{id: brief}, record, now, 1, 0) // an empty record keeps nothing
	if err != nil {
		return "", err
	}
	if made[0].Err != nil {
		return "", fmt.Errorf("the brief decision (backend %s): %w", b.Name(), made[0].Err)
	}
	d, recorded := made[0].Decision, map[bool]string{true: "existing", false: "new"}[made[0].Existing]
	if record == "" {
		recorded = "no"
	}
	return fmt.Sprintf("LINT DECIDE card=%s op=%s %s recorded=%s", oneline.Field(name), oneline.Field(d.ID), oneline.Escape(decide.BriefOf(d).Line()), oneline.Field(recorded)), nil
}
