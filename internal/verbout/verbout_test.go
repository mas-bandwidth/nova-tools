package verbout

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOKTextAndJSON(t *testing.T) {
	t.Parallel()
	v := OK("links")
	v.FactInt("files", 10).
		FactInt("links", 42).
		FactInt("excluded", 1)

	// Text rendering
	var textBuf bytes.Buffer
	if err := v.RenderText(&textBuf); err != nil {
		t.Fatalf("RenderText error: %v", err)
	}
	wantText := "LINKS OK files=10 links=42 excluded=1\n"
	if got := textBuf.String(); got != wantText {
		t.Errorf("RenderText = %q, want %q", got, wantText)
	}

	// JSON rendering
	var jsonBuf bytes.Buffer
	if err := v.RenderJSON(&jsonBuf); err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	res, ok := parsed["result"].(map[string]any)
	if !ok || res["verb"] != "links" || res["status"] != "ok" || res["exit"] != float64(0) {
		t.Errorf("unexpected result in JSON: %v", res)
	}

	facts, ok := parsed["facts"].(map[string]any)
	if !ok || facts["files"] != "10" || facts["links"] != "42" || facts["excluded"] != "1" {
		t.Errorf("unexpected facts in JSON: %v", facts)
	}
}

func TestFactsInsertionOrderPreservedInJSON(t *testing.T) {
	t.Parallel()
	f := NewFacts()
	f.Set("zebra", "1").
		Set("alpha", "2").
		Set("middle", "3")

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	raw := string(data)
	// Check order of keys: zebra before alpha before middle
	zIdx := strings.Index(raw, `"zebra"`)
	aIdx := strings.Index(raw, `"alpha"`)
	mIdx := strings.Index(raw, `"middle"`)

	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("Facts JSON keys not in insertion order: %s", raw)
	}

	// Unmarshal back
	var f2 Facts
	if err := json.Unmarshal(data, &f2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	pairs := f2.Pairs()
	if len(pairs) != 3 || pairs[0].Key != "zebra" || pairs[1].Key != "alpha" || pairs[2].Key != "middle" {
		t.Errorf("Unmarshaled pairs not in insertion order: %v", pairs)
	}
}

func TestFailedWithItemsAndStatusLast(t *testing.T) {
	t.Parallel()
	v := Failed("links", 1)
	v.StatusLast = true
	v.FactInt("files", 2).
		FactInt("links", 5)

	b := v.Bounded(2, "broken", "--fail-max 0")
	b.Line("FAIL", "doc1.md:10: broken link 1")
	b.Line("FAIL", "doc2.md:20: broken link 2")
	b.Line("FAIL", "doc3.md:30: broken link 3")
	b.Finish()

	v.FactInt("broken", b.Total()).
		FactInt("shown", b.Shown()).
		FactInt("excluded", 0)

	if b.Shown() != 2 || b.Total() != 3 || b.Elided() != 1 {
		t.Errorf("Bounded counts wrong: shown=%d total=%d elided=%d", b.Shown(), b.Total(), b.Elided())
	}

	// Text rendering should put items first, more next, and result line last
	text := v.Text()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), text)
	}

	if lines[0] != "LINKS FAIL doc1.md:10: broken link 1" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "LINKS FAIL doc2.md:20: broken link 2" {
		t.Errorf("line 1 = %q", lines[1])
	}
	if lines[2] != "LINKS MORE kind=broken shown=2 total=3 --fail-max 0" {
		t.Errorf("line 2 = %q", lines[2])
	}
	if lines[3] != "LINKS FAIL files=2 links=5 broken=3 shown=2 excluded=0" {
		t.Errorf("line 3 = %q", lines[3])
	}

	// JSON rendering
	var jsonBuf bytes.Buffer
	if err := v.RenderJSON(&jsonBuf); err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}

	var parsed struct {
		Result Result   `json:"result"`
		Facts  Facts    `json:"facts"`
		Items  []Item   `json:"items"`
		More   []More   `json:"more"`
		Notes  []string `json:"notes"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal JSON error: %v", err)
	}

	if parsed.Result.Status != StatusFailed || parsed.Result.Exit != 1 {
		t.Errorf("unexpected Result: %+v", parsed.Result)
	}
	if len(parsed.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(parsed.Items))
	}
	if len(parsed.More) != 1 || parsed.More[0].Total != 3 || parsed.More[0].Shown != 2 {
		t.Errorf("unexpected More: %+v", parsed.More)
	}
}

func TestRefuseFormat(t *testing.T) {
	t.Parallel()
	v := Refuse("links", "dir is required", "nova-check help")

	text := v.Text()
	want := "LINKS REFUSED reason=dir\\x20is\\x20required; run: nova-check help\n"
	if text != want {
		t.Errorf("Text = %q, want %q", text, want)
	}

	if v.Result.Status != StatusRefused || v.Result.Exit != 2 || v.Result.Remedy != "nova-check help" {
		t.Errorf("unexpected Result: %+v", v.Result)
	}
}

func TestRoundTripParse(t *testing.T) {
	t.Parallel()
	v := Failed("quickstart", 1)
	v.FactInt("checks", 2).
		Fact("failed", "links,nocode").
		FactInt("worst-exit", 1).
		Fact("next", "nova-check help").
		Note("first run completed").
		AddMore("findings", 5, 10, "--fail-max 0")

	v.Item("STEP", "links checked")
	v.Item("STEP", "nocode checked")

	text := v.Text()

	parsed, err := ParseText(text)
	if err != nil {
		t.Fatalf("ParseText error: %v", err)
	}

	if parsed.Result.Verb != "quickstart" {
		t.Errorf("verb = %q, want quickstart", parsed.Result.Verb)
	}
	if parsed.Result.Status != StatusFailed {
		t.Errorf("status = %q, want failed", parsed.Result.Status)
	}
	if parsed.Result.Exit != 1 {
		t.Errorf("exit = %d, want 1", parsed.Result.Exit)
	}

	if val, _ := parsed.Facts.Get("checks"); val != "2" {
		t.Errorf("facts[checks] = %q, want 2", val)
	}
	if val, _ := parsed.Facts.Get("failed"); val != "links,nocode" {
		t.Errorf("facts[failed] = %q, want links,nocode", val)
	}

	if len(parsed.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(parsed.Items))
	}
	if len(parsed.More) != 1 || parsed.More[0].Total != 10 {
		t.Errorf("more = %+v", parsed.More)
	}
	if len(parsed.Notes) != 1 || parsed.Notes[0] != "first run completed" {
		t.Errorf("notes = %+v", parsed.Notes)
	}
}

func TestEmitRouting(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	// OK routes to stdout
	vOK := OK("test").Fact("status", "fine")
	exit := vOK.Emit(&stdout, &stderr, false)
	if exit != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Errorf("OK emit: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()

	// FAIL routes to stderr
	vFail := Failed("test", 1).Fact("err", "boom")
	exit = vFail.Emit(&stdout, &stderr, false)
	if exit != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Errorf("Fail emit: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()

	// asJSON routes to stdout regardless of status
	exit = vFail.Emit(&stdout, &stderr, true)
	if exit != 1 || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Errorf("JSON emit: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
