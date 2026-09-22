package cutrule

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOneCardPerNumberedRule is the DONE-WHEN control: `cut --kind rule --spec
// docs/SPEC-<X>.md` writes one lint-clean read card per numbered rule, the count
// equal to the lines `grep -cE "^[0-9]+\. "` counts on the file, and every card
// names the rule's own number, its line in the spec file and the package the
// rule's text names.
func TestOneCardPerNumberedRule(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "SPEC-DEMO.md")
	text := "# SPEC-DEMO — the demo spec\n" +
		"\n" +
		"Prose before the rules.\n" +
		"\n" +
		"1. The bus refuses a note whose body names internal/bus paths twice, with the file and the line named.\n" +
		"\n" +
		"Prose between the rules.\n" +
		"\n" +
		"2. A card cut for internal/pulse numbers from the queue state file, under the queue's lock, never by hand.\n" +
		"\n" +
		"3. The harvest of internal/pulse/wire.go matches line 1 byte for byte,\n" +
		"   with the reason on its own line and nothing else after it.\n" +
		"\n" +
		"Closing prose.\n"
	if err := os.WriteFile(spec, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	// The count the card is measured by, counted the way grep counts it, from the
	// file's own bytes and not from the cutter's parser.
	raw, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	grepCount := len(regexp.MustCompile(`(?m)^[0-9]+\. `).FindAllString(string(raw), -1))
	if grepCount != 3 {
		t.Fatalf("the fixture carries %d numbered rules, want 3", grepCount)
	}

	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	var stdout, stderr bytes.Buffer
	code := CutRule(CutRuleInput{Spec: spec, Repo: "mas-bandwidth/nova-tools", Out: out, Queue: queue, Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}

	matches, _ := filepath.Glob(filepath.Join(out, "card-*.md"))
	if len(matches) != grepCount {
		t.Fatalf("%d card files, want %d (one per numbered rule)", len(matches), grepCount)
	}
	for i, e := range []struct {
		rule int
		line int
		pkg  string
	}{
		{1, 5, "internal/bus"},
		{2, 9, "internal/pulse"},
		{3, 11, "internal/pulse"},
	} {
		n := i + 1
		cardPath := filepath.Join(out, fmt.Sprintf("card-%d.md", n))
		card, err := os.ReadFile(cardPath)
		if err != nil {
			t.Fatal(err)
		}
		body := string(card)
		wantLine1 := fmt.Sprintf("RESULT: CARD-%d read of nova-tools SPEC-DEMO.md rule %d at line %d (%s)", n, e.rule, e.line, e.pkg)
		if got := strings.SplitN(body, "\n", 2)[0]; got != wantLine1 {
			t.Errorf("card-%d line 1 = %q\nwant            %q", n, got, wantLine1)
		}
		if !strings.Contains(strings.ToLower(body), "do not run go build") {
			t.Errorf("card-%d is a read card and must say so:\n%s", n, body)
		}
		verdict := fmt.Sprintf("rule%d: APPROVE|HOLD line=%d package=%s", e.rule, e.line, e.pkg)
		if !strings.Contains(body, verdict) {
			t.Errorf("card-%d carries no verdict line %q:\n%s", n, verdict, body)
		}
	}

	// The numbers come only from the queue state file: three cards advanced
	// next_card from 1 to 4.
	state, err := os.ReadFile(filepath.Join(queue, "state.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "next_card\t4") {
		t.Errorf("state.tsv = %q, want next_card\t4 after three cards", string(state))
	}
	// A read card is an already-approved read: it lands in the green lane.
	if lane, _ := filepath.Glob(filepath.Join(queue, "lanes", "green", "*.card")); len(lane) != grepCount {
		t.Errorf("%d cards in the green lane, want %d", len(lane), grepCount)
	}
	// One line per card and one summary line, nothing else.
	got := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(got) != grepCount+1 {
		t.Errorf("stdout is %d lines, want %d:\n%s", len(got), grepCount+1, stdout.String())
	}
	if !strings.Contains(stdout.String(), "CUT RULE cards=3") {
		t.Errorf("no CUT RULE summary line:\n%s", stdout.String())
	}
}

// Every refusal names its remedy: the flags the cutter needs, a spec that cannot
// be read, and a readable file that carries no numbered rules at all.
func TestCutRuleRefusalsNameTheirRemedy(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "EMPTY.md")
	if err := os.WriteFile(empty, []byte("# no rules\n\nprose only, nothing numbered.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		want string
		in   CutRuleInput
	}{
		{"no spec", "--spec", CutRuleInput{Repo: "o/n", Out: dir, Queue: dir}},
		{"no repo", "--repo", CutRuleInput{Out: dir, Queue: dir}},
		{"no queue", "--queue", CutRuleInput{Repo: "o/n", Out: dir}},
		{"no out", "--out", CutRuleInput{Repo: "o/n", Queue: dir}},
		{"unreadable spec", "no such file", CutRuleInput{Spec: filepath.Join(dir, "ABSENT.md"), Repo: "o/n", Out: dir, Queue: dir}},
		{"no numbered rules", "no numbered rules", CutRuleInput{Spec: empty, Repo: "o/n", Out: dir, Queue: dir}},
	} {
		var stdout, stderr bytes.Buffer
		in := c.in
		in.Stdout, in.Stderr = &stdout, &stderr
		if code := CutRule(in); code != 2 {
			t.Errorf("%s: exit = %d, want 2", c.name, code)
		}
		if !strings.Contains(stderr.String(), "CUT REFUSED") || !strings.Contains(stderr.String(), c.want) || !strings.Contains(stderr.String(), "(") {
			t.Errorf("%s: refusal = %q, want CUT REFUSED naming %s and a remedy", c.name, stderr.String(), c.want)
		}
		if stdout.Len() > 0 {
			t.Errorf("%s: a refusal printed %q on stdout", c.name, stdout.String())
		}
	}
}
