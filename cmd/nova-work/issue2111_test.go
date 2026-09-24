package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue2111 pins what nova-tools#2111 titles: "visualize: the pipeline as linked
// views: source record → card → attempt → result → PR → reads → batch → dev, each
// stage showing its data and pointing at its neighbours".
//
// The record below is this card's own journey, the way the issue means it: the eight
// stages the title names, one item each, joined by uid -- the one key every stage
// joins on -- with a second, unrelated source and card beside them so a selection
// has something not to show. The five tables rebuilt by hand on 2026-09-20 (the
// funnel, the read table, the sprint table, the adoption table, the waiting table)
// were five projections of this one data set; the linked view is the projection that
// shows the whole chain at once.
const issue2111Pipeline = `{
  "stages": [
    {"stage": "source record", "items": [
      {"uid": "issue:2111", "label": "nova-tools#2111 visualize the pipeline as linked views", "to": ["card:fix3-nova-tools-2111"]},
      {"uid": "issue:42", "label": "nova-tools#42 an unrelated ask", "to": ["card:other-42"]}
    ]},
    {"stage": "card", "items": [
      {"uid": "card:fix3-nova-tools-2111", "label": "rowan/fix3-nova-tools-2111", "from": ["issue:2111"], "to": ["attempt:2"]},
      {"uid": "card:other-42", "label": "rowan/other-42", "from": ["issue:42"]}
    ]},
    {"stage": "attempt", "items": [
      {"uid": "attempt:2", "label": "attempt 2 on the bench", "from": ["card:fix3-nova-tools-2111"], "to": ["result:done"]}
    ]},
    {"stage": "result", "items": [
      {"uid": "result:done", "label": "DONE", "from": ["attempt:2"], "to": ["pr:2112"]}
    ]},
    {"stage": "PR", "items": [
      {"uid": "pr:2112", "label": "mas-bandwidth/nova-tools#2112", "from": ["result:done"], "to": ["reads:live-head"]}
    ]},
    {"stage": "reads", "items": [
      {"uid": "reads:live-head", "label": "reads at the live head", "from": ["pr:2112"], "to": ["batch:integration-2026-09-23"]}
    ]},
    {"stage": "batch", "items": [
      {"uid": "batch:integration-2026-09-23", "label": "BATCH OK 8 pull requests", "from": ["reads:live-head"], "to": ["dev:09fbedc"]}
    ]},
    {"stage": "dev", "items": [
      {"uid": "dev:09fbedc", "label": "dev at 09fbedc90521", "from": ["batch:integration-2026-09-23"]}
    ]}
  ]
}`

// writeIssue2111Record writes one pipeline record inside the test's own TempDir and
// returns its path: every path a test writes is named there and nowhere else.
func writeIssue2111Record(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pipeline.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIssue2111(t *testing.T) {
	path := writeIssue2111Record(t, issue2111Pipeline)

	// The whole pipeline: every stage shows its population right now -- a count and
	// one line per item -- and every item points at its neighbours, where it came
	// from and where it goes. The eight stages print in the order the title names:
	// source record → card → attempt → result → PR → reads → batch → dev.
	t.Run("every stage shows its data and points at its neighbours", func(t *testing.T) {
		code, out, errOut := invoke("visualize", "--file", path)
		if code != 0 {
			t.Fatalf("visualize exit = %d, stderr:\n%s", code, errOut)
		}
		if errOut != "" {
			t.Fatalf("visualize wrote stderr:\n%s", errOut)
		}
		want := []string{
			`VIEW stage="source record" rows=2`,
			`ITEM stage="source record" uid=issue:2111 to=card:fix3-nova-tools-2111 label=nova-tools#2111 visualize the pipeline as linked views`,
			`ITEM stage="source record" uid=issue:42 to=card:other-42 label=nova-tools#42 an unrelated ask`,
			`VIEW stage="card" rows=2`,
			`ITEM stage="card" uid=card:fix3-nova-tools-2111 from=issue:2111 to=attempt:2 label=rowan/fix3-nova-tools-2111`,
			`ITEM stage="card" uid=card:other-42 from=issue:42 label=rowan/other-42`,
			`VIEW stage="attempt" rows=1`,
			`ITEM stage="attempt" uid=attempt:2 from=card:fix3-nova-tools-2111 to=result:done label=attempt 2 on the bench`,
			`VIEW stage="result" rows=1`,
			`ITEM stage="result" uid=result:done from=attempt:2 to=pr:2112 label=DONE`,
			`VIEW stage="PR" rows=1`,
			`ITEM stage="PR" uid=pr:2112 from=result:done to=reads:live-head label=mas-bandwidth/nova-tools#2112`,
			`VIEW stage="reads" rows=1`,
			`ITEM stage="reads" uid=reads:live-head from=pr:2112 to=batch:integration-2026-09-23 label=reads at the live head`,
			`VIEW stage="batch" rows=1`,
			`ITEM stage="batch" uid=batch:integration-2026-09-23 from=reads:live-head to=dev:09fbedc label=BATCH OK 8 pull requests`,
			`VIEW stage="dev" rows=1`,
			`ITEM stage="dev" uid=dev:09fbedc from=batch:integration-2026-09-23 label=dev at 09fbedc90521`,
		}
		assertIssue2111Lines(t, out, want)
	})

	// Selecting any item highlights its ancestors and descendants across all
	// columns: the card's whole chain reads across the view, each item marked with
	// which side of the selection it is on, and the unrelated source and card are
	// not shown at all.
	t.Run("selecting the card highlights its chain across every stage", func(t *testing.T) {
		code, out, errOut := invoke("visualize", "--file", path, "--select", "card:fix3-nova-tools-2111")
		if code != 0 {
			t.Fatalf("visualize --select exit = %d, stderr:\n%s", code, errOut)
		}
		if errOut != "" {
			t.Fatalf("visualize --select wrote stderr:\n%s", errOut)
		}
		want := []string{
			`VIEW stage="source record" rows=1`,
			`ITEM stage="source record" uid=issue:2111 to=card:fix3-nova-tools-2111 link=ancestor label=nova-tools#2111 visualize the pipeline as linked views`,
			`VIEW stage="card" rows=1`,
			`ITEM stage="card" uid=card:fix3-nova-tools-2111 from=issue:2111 to=attempt:2 link=self label=rowan/fix3-nova-tools-2111`,
			`VIEW stage="attempt" rows=1`,
			`ITEM stage="attempt" uid=attempt:2 from=card:fix3-nova-tools-2111 to=result:done link=descendant label=attempt 2 on the bench`,
			`VIEW stage="result" rows=1`,
			`ITEM stage="result" uid=result:done from=attempt:2 to=pr:2112 link=descendant label=DONE`,
			`VIEW stage="PR" rows=1`,
			`ITEM stage="PR" uid=pr:2112 from=result:done to=reads:live-head link=descendant label=mas-bandwidth/nova-tools#2112`,
			`VIEW stage="reads" rows=1`,
			`ITEM stage="reads" uid=reads:live-head from=pr:2112 to=batch:integration-2026-09-23 link=descendant label=reads at the live head`,
			`VIEW stage="batch" rows=1`,
			`ITEM stage="batch" uid=batch:integration-2026-09-23 from=reads:live-head to=dev:09fbedc link=descendant label=BATCH OK 8 pull requests`,
			`VIEW stage="dev" rows=1`,
			`ITEM stage="dev" uid=dev:09fbedc from=batch:integration-2026-09-23 link=descendant label=dev at 09fbedc90521`,
		}
		assertIssue2111Lines(t, out, want)
		for _, gone := range []string{"issue:42", "card:other-42"} {
			if strings.Contains(out, gone) {
				t.Errorf("the selected view still shows the unrelated %s:\n%s", gone, out)
			}
		}
	})

	// A link is a join, not a label: a record whose item goes to an item no stage
	// holds refuses rather than rendering a dangling label -- the 2026-09-20
	// failure mode the issue names -- and a selection naming no item in the record
	// refuses the same way, each naming the uid and the remedy.
	t.Run("a link is a join not a label", func(t *testing.T) {
		dangling := writeIssue2111Record(t, `{"stages":[{"stage":"card","items":[{"uid":"c1","label":"a card","to":["a:missing"]}]}]}`)
		code, out, errOut := invoke("visualize", "--file", dangling)
		if code != 2 {
			t.Fatalf("a dangling link exits %d, want 2 (stdout %q, stderr %q)", code, out, errOut)
		}
		if out != "" {
			t.Fatalf("a refusal wrote stdout: %q", out)
		}
		if !strings.Contains(errOut, "a:missing") || !strings.Contains(errOut, "no stage holds") || !strings.HasSuffix(strings.TrimSpace(errOut), "run: nova-work help") {
			t.Fatalf("the refusal does not name the dangling uid and the remedy:\n%s", errOut)
		}

		code, out, errOut = invoke("visualize", "--file", path, "--select", "uid:nobody")
		if code != 2 {
			t.Fatalf("an unknown selection exits %d, want 2 (stdout %q, stderr %q)", code, out, errOut)
		}
		if out != "" {
			t.Fatalf("a refusal wrote stdout: %q", out)
		}
		if !strings.Contains(errOut, "uid:nobody") || !strings.Contains(errOut, "names no item") || !strings.HasSuffix(strings.TrimSpace(errOut), "run: nova-work help") {
			t.Fatalf("the refusal does not name the unknown selection and the remedy:\n%s", errOut)
		}
	})
}

// assertIssue2111Lines pins a run's whole output line for line: the linked view is a
// shape a reader greps, so the test holds the exact specimen rather than a sample.
func assertIssue2111Lines(t *testing.T, out string, want []string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("the view is %d lines, want %d:\n%s", len(lines), len(want), out)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, lines[i], w)
		}
	}
}
