package decide

import (
	"context"
	"strings"
	"testing"
)

// pickFake answers the effort question with one choice and confidence, and
// never dials anything.
type pickFake struct {
	choice string
	conf   float64
	err    error
	usage  Usage
	calls  int
}

func (f *pickFake) Decide(_ context.Context, _ string, qs map[string]Question) (map[string]Answer, Usage, error) {
	f.calls++
	if f.err != nil {
		return nil, Usage{}, f.err
	}
	q, ok := qs[EffortQuestion]
	if !ok || len(q.Choice) == 0 {
		return nil, Usage{}, nil
	}
	return map[string]Answer{EffortQuestion: {Type: "choice", Choice: f.choice, Confidence: f.conf}}, f.usage, nil
}

func TestDefaultPickTableParses(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatalf("the embedded pick table is broken: %v", err)
	}
	if table.Version != 1 {
		t.Errorf("version = %d, want 1", table.Version)
	}
	for _, effort := range Efforts {
		if _, ok := table.ModelFor(effort); !ok {
			t.Errorf("effort %s has no model id", effort)
		}
	}
	if len(table.Picks) == 0 {
		t.Errorf("the table holds no default picks")
	}
}

func TestPickForDerivesTheModelFromTheEffort(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := table.PickFor(KindFixWithRedTest)
	if !ok {
		t.Fatalf("kind %s has no pick", KindFixWithRedTest)
	}
	if p.Effort != "sonnet" || p.Model != "claude-sonnet-5" {
		t.Errorf("fix-with-red-test pick = %s/%s, want sonnet/claude-sonnet-5", p.Effort, p.Model)
	}
	p, ok = table.PickFor(KindDesign)
	if !ok {
		t.Fatalf("kind %s has no pick", KindDesign)
	}
	if p.Effort != "fable" || p.Model != "claude-fable-5-1" {
		t.Errorf("design pick = %s/%s, want fable/claude-fable-5-1", p.Effort, p.Model)
	}
}

func TestPickWithTableAnswersDeterministically(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	first, err := PickWithTable(table, KindRebase)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PickWithTable(table, KindRebase)
	if err != nil {
		t.Fatal(err)
	}
	if first.Line() != second.Line() {
		t.Errorf("the table alone is deterministic:\n%s\n%s", first.Line(), second.Line())
	}
	if first.Source != PickSourceTable || first.HasConfidence || first.Calls != 0 {
		t.Errorf("a table answer has no confidence, no call and names its source: %+v", first)
	}
}

func TestPickJevChoosesTheEffort(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	fake := &pickFake{choice: "opus", conf: 0.9, usage: Usage{InputTokens: 10, HasInput: true}}
	res, err := PickJev(context.Background(), fake, table, KindGuard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Effort != "opus" || res.Model != "claude-opus-5-5" || res.Source != PickSourceJev || !res.HasConfidence {
		t.Errorf("jev pick = %+v, want opus/claude-opus-5-5 from jev", res)
	}
	if res.Calls != 1 || !res.Usage.HasInput || res.Usage.InputTokens != 10 {
		t.Errorf("the usage travels with the answer: calls=%d usage=%+v", res.Calls, res.Usage)
	}
}

func TestPickJevFallsBackToTheTable(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	for name, fake := range map[string]*pickFake{
		"provider error": {err: errNoRungQuestion},
		"unknown effort": {choice: "vibes", conf: 0.99},
		"no answer":      {conf: 0.99},
	} {
		res, err := PickJev(context.Background(), fake, table, KindRebase)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Source != PickSourceTable || res.Effort != "haiku" {
			t.Errorf("%s: the table's default stands, got %+v", name, res)
		}
		if res.Calls != 1 {
			t.Errorf("%s: a call was made and accounted for, got calls=%d", name, res.Calls)
		}
	}
}

func TestPickRefusesUnknownKind(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PickWithTable(table, "vibes"); err == nil {
		t.Errorf("an unknown kind is a refusal, never a guess")
	}
	if _, err := PickJev(context.Background(), nil, table, "vibes"); err == nil {
		t.Errorf("an unknown kind is a refusal, never a guess")
	}
}

func TestParsePickRefusesBadTables(t *testing.T) {
	for name, body := range map[string]string{
		"not json":            `{"version":`,
		"no version":          `{"models":{"haiku":"claude-haiku-5-1"},"picks":{"rebase":"haiku"}}`,
		"no models":           `{"version":1,"picks":{"rebase":"haiku"}}`,
		"missing effort":      `{"version":1,"models":{"haiku":"claude-haiku-5-1"},"picks":{"rebase":"haiku"}}`,
		"unknown pick effort": `{"version":1,"models":{"haiku":"c","sonnet":"c","opus":"c","fable":"c"},"picks":{"rebase":"vibes"}}`,
	} {
		if _, err := ParsePick([]byte(body)); err == nil {
			t.Errorf("%s: a bad table is a refusal, got none", name)
		}
	}
}

func TestPickLineIsOneLine(t *testing.T) {
	table, err := DefaultPick()
	if err != nil {
		t.Fatal(err)
	}
	res, err := PickWithTable(table, KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	line := res.Line()
	if strings.Contains(line, "\n") {
		t.Errorf("the receipt is one line: %q", line)
	}
	for _, want := range []string{"PICK ", "kind=spec", "effort=opus", "model=claude-opus-5-5", "version=1", "conf=-", "source=table"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
}
