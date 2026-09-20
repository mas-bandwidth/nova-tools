package swarm

import (
	"strings"
	"testing"
)

// #855: the two cheapest template cuts are the read-family bound (turns ≤ 8,
// REASONING: low, no exploration) and the writing-family turn bound (turns ≤ 20,
// no exploration, default reasoning). The baked-in pulse templates carry the
// bounds so a templates dir built from `nova-swarm template --name` cannot
// re-introduce a 30-turn grep-around card.
func TestPulseTemplatesBoundTurnsAndReasoning(t *testing.T) {
	readFamily := []string{"read", "text", "tone"}
	writeFamily := []string{"fix", "replay", "drift"}
	for _, name := range readFamily {
		body, err := Template(name)
		if err != nil {
			t.Fatalf("template --name %s: %s", name, err)
		}
		if !strings.Contains(body, "TURNS: 8") {
			t.Errorf("%s template names no 8-turn budget:\n%s", name, body)
		}
		if !strings.Contains(body, "REASONING: low") {
			t.Errorf("%s template names no low reasoning setting:\n%s", name, body)
		}
		if !strings.Contains(body, "Do not grep around") {
			t.Errorf("%s template still invites exploration:\n%s", name, body)
		}
		if n := pulseTemplateSteps(body); n < 1 || n > 8 {
			t.Errorf("%s template has %d STEP lines, want 1..8", name, n)
		}
	}
	for _, name := range writeFamily {
		body, err := Template(name)
		if err != nil {
			t.Fatalf("template --name %s: %s", name, err)
		}
		if !strings.Contains(body, "TURNS: 20") {
			t.Errorf("%s template names no 20-turn budget:\n%s", name, body)
		}
		if strings.Contains(body, "REASONING: low") {
			t.Errorf("%s template lowered reasoning; only the read family does:\n%s", name, body)
		}
		if !strings.Contains(body, "Do not grep around") {
			t.Errorf("%s template still invites exploration:\n%s", name, body)
		}
		if n := pulseTemplateSteps(body); n < 1 || n > 20 {
			t.Errorf("%s template has %d STEP lines, want 1..20", name, n)
		}
	}
}

func pulseTemplateSteps(body string) int {
	n := 0
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "STEP ") {
			n++
		}
	}
	return n
}
