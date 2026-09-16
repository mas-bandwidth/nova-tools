package pulse

// Class L (#828): the brief is rendered from the bench, so it cannot drift from it, and it
// obeys the rules a brief has to obey -- one page, short lines, and a log read by tail.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// briefQueue is a queue with a configuration and a rule table, as a bench has.
func briefQueue(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile),
		[]byte("[slots]\nstudio = 64\nspace = 128\n\n[routes]\ntext = [\"opencode/deepseek-v4-flash\"]\ncode = [\"opencode/deepseek-v4-flash\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []RuleRow{
		{Kind: "stop", Condition: "dev ci is failure and no STOP stands", Verdict: "write queue/STOP naming the sha; revert first", Since: "2026-09-16", Source: "POLICY 03:50Z"},
		{Kind: "hold", Condition: "a read verdict is HOLD", Verdict: "nothing: the loop cuts the next read at the next head", Since: "2026-09-16", Source: "POLICY 01:40Z"},
		{Kind: "admission", Condition: "a card carries no cut stamp", Verdict: "REFUSE it and name the remedy", Since: "2026-09-16", Source: "828 class P"},
		{Kind: "record", Condition: "pit stop 2 ended at 16:00Z", Verdict: "(no action)", Since: "2026-09-16", Source: "POLICY 16:00Z"},
	}
	if err := WriteRules(dir, rows); err != nil {
		t.Fatal(err)
	}
	return dir
}

func renderBrief(t *testing.T, in BriefInput) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	return Brief(in), out.String(), errs.String()
}

// brief-carries-every-decidable-row: a pulse decides the stop, hold and admission rows and
// nothing else, so every one of them is on the page it reads.
func TestBriefCarriesEveryDecidableRuleRow(t *testing.T) {
	queue := briefQueue(t)
	exit, out, errs := renderBrief(t, BriefInput{As: "Rowan", Queue: queue, Roots: "./swarm-root,./swarm-root-space"})
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, errs)
	}
	for _, want := range []string{
		"dev ci is failure and no STOP stands",
		"a read verdict is HOLD",
		"a card carries no cut stamp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the brief does not carry the row %q:\n%s", want, out)
		}
	}
	// A record is not a rule a pulse may act on, so it is not on the page.
	if strings.Contains(out, "pit stop 2 ended") {
		t.Errorf("the brief carries a record row:\n%s", out)
	}
	// The bench's own numbers come from the configuration, never from a person's memory.
	if !strings.Contains(out, "space:128") || !strings.Contains(out, "opencode/deepseek-v4-flash") {
		t.Errorf("the brief does not carry the configured bench:\n%s", out)
	}
	// One page: every line fits a brief.
	for i, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(l) > briefLineMax {
			t.Errorf("line %d is %d bytes, over %d:\n%s", i+1, len(l), briefLineMax, l)
		}
	}
	// The log is read by tail -n and never by a clock: the 2026-09-16 bug was a filter that
	// matched yesterday's lines and reported a busy hour as silent.
	if !strings.Contains(out, "tail -n ") {
		t.Errorf("the brief does not read the log with tail -n:\n%s", out)
	}
	for _, clock := range []string{"date -u -v-", "--since", "awk -v t=", `grep "$(date`} {
		if strings.Contains(out, clock) {
			t.Errorf("the brief filters the log by clock time (%q), which matched yesterday's lines", clock)
		}
	}
}

// brief-friend-shape: a friend's pulse is the wake, the one note, the one thing, the reply.
func TestBriefFriendShape(t *testing.T) {
	queue := briefQueue(t)
	exit, out, errs := renderBrief(t, BriefInput{As: "Stella", Queue: queue, Friend: true})
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, errs)
	}
	for _, want := range []string{"nova-bus wake --bus . --as Stella", "nova-review packet --pr", "PULSE Stella <time>Z note="} {
		if !strings.Contains(out, want) {
			t.Errorf("the friend brief does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "INBOX OPEN\n") {
		t.Errorf("the friend brief tells the pulse to open the inbox:\n%s", out)
	}
	if !strings.Contains(out, "dev ci is failure") {
		t.Errorf("the friend brief carries no rule rows:\n%s", out)
	}
	for i, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(l) > briefLineMax {
			t.Errorf("line %d is %d bytes, over %d:\n%s", i+1, len(l), briefLineMax, l)
		}
	}
}

// brief-refuses-what-it-cannot-render, each refusal naming its remedy; and a rule row too
// long for a page is a refusal naming the file, not a page that breaks its own bound.
func TestBriefRefusals(t *testing.T) {
	queue := briefQueue(t)
	for _, c := range []struct {
		name string
		in   BriefInput
		want string
	}{
		{"no name", BriefInput{Queue: queue, Roots: "."}, "--as is required"},
		{"no queue", BriefInput{As: "Rowan", Roots: "."}, "--queue is required"},
		{"no roots", BriefInput{As: "Rowan", Queue: queue}, "--roots is required"},
	} {
		exit, _, errs := renderBrief(t, c.in)
		if exit != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, stderr %q, want %q", c.name, exit, errs, c.want)
		}
	}
}
