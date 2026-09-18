package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// Stella's re-review of #1327 at 833514e1, at the verb: R3 (the typed wait
// reaches the command line) and R5 (the provider's usage reaches the shared
// accounting the rest of the fleet writes).

// fake is a Decider that answers one rung with one usage and never dials.
type fake struct {
	conf  float64
	usage decide.Usage
	err   error
	calls int
	pick  int // which offered option to choose, by position
}

func (f *fake) Decide(_ context.Context, _ string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.calls++
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	var names []string
	for name := range qs[decide.RungQuestion].Choice {
		names = append(names, name)
	}
	sort.Strings(names)
	choice := ""
	if f.pick < len(names) {
		choice = names[f.pick]
	}
	return map[string]decide.Answer{decide.RungQuestion: {Type: "choice", Choice: choice, Confidence: f.conf}}, f.usage, nil
}

// useFake puts a fake provider behind the verb for one test.
func useFake(t *testing.T, f *fake) {
	t.Helper()
	was := deciderOpener
	deciderOpener = func(string, string) (decide.Decider, error) { return f, nil }
	t.Cleanup(func() { deciderOpener = was })
}

// (R3) An unresolved timeout is a WAIT at the command line: the typed field on
// the line, an exit that is not permission, and the owner still named.
func TestRouteWaitIsTypedAndNotPermission(t *testing.T) {
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	for name, args := range map[string][]string{
		"ordinary": {"--kind", "fix-with-red-test", "--files", "3", "--packages", "1", "--attempt", "sol:timeout"},
		"security": {"--kind", "guard", "--files", "1", "--attempt", "johnny:timeout"},
		"touch":    {"--kind", "fleet-chore", "--files", "1", "--touches", "deploy-keys", "--attempt", "johnny:timeout"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"route", "--no-jev", "--unit-id", "w-1", "--log", log}, args...), &stdout, &stderr)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1 (a wait is not permission to dispatch); stdout=%q", name, code, stdout.String())
		}
		line := stdout.String()
		if !strings.Contains(line, "wait=awaiting_termination") {
			t.Errorf("%s: the line carries no typed wait: %s", name, line)
		}
		if strings.HasPrefix(name, "security") || name == "touch" {
			if !strings.Contains(line, "rung=johnny") {
				t.Errorf("%s: the designated owner must stand beside the wait: %s", name, line)
			}
		}
	}
	// Every row of the log says whether it was a wait.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e decide.Entry
		if err := json.Unmarshal([]byte(row), &e); err != nil {
			t.Fatal(err)
		}
		if e.Wait != decide.WaitAwaitingTermination || !e.AwaitingTermination {
			t.Errorf("row %d lost the wait: %s", i+1, row)
		}
	}
}

// A decision that is not a wait still carries the field, and still exits 0.
func TestRouteWithoutAWaitExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--packages", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "wait=-") {
		t.Errorf("every line carries the field, an absence as a dash: %s", stdout.String())
	}
}

// (R5) The provider's usage is written to the shared accounting: one TSV row in
// the fleet's usage columns, with the tokens it reported.
func TestRouteWritesTheUsageRecord(t *testing.T) {
	useFake(t, &fake{conf: 0.97, usage: decide.Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}})
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u-1", "--kind", "new-verb", "--files", "3", "--packages", "1",
		"--lanes", "1", "--usage", usage, "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	raw, err := os.ReadFile(usage)
	if err != nil {
		t.Fatalf("no usage was written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("a header and one row, got %d:\n%s", len(lines), raw)
	}
	head := strings.Split(lines[0], "\t")
	row := strings.Split(lines[1], "\t")
	if len(head) != len(row) {
		t.Fatalf("the row does not match the header:\n%s", raw)
	}
	got := map[string]string{}
	for i, name := range head {
		got[name] = row[i]
	}
	for field, want := range map[string]string{
		"tokens_in": "937", "tokens_out": "12", "provider": "typesafe", "model": "jev-latest", "rc": "0",
	} {
		if got[field] != want {
			t.Errorf("usage %s = %q, want %q (row: %v)", field, got[field], want, got)
		}
	}
	// A number the provider did not report is an absence, never a zero.
	for _, field := range []string{"cache_read", "cache_write", "reasoning", "usd"} {
		if got[field] != "-" {
			t.Errorf("usage %s = %q, want \"-\": a zero is a measurement and a dash is an absence", field, got[field])
		}
	}
	// And the JSONL row carries the same spend.
	logged, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var e decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(logged), &e); err != nil {
		t.Fatal(err)
	}
	if e.TokensIn == nil || *e.TokensIn != 937 || e.Calls != 1 {
		t.Errorf("the log row lost the spend: %s", logged)
	}
}

// A call that failed is evidence too: a row with an unknown cost, never a
// silent zero and never no row at all.
func TestRouteWritesUsageForAFailedCall(t *testing.T) {
	useFake(t, &fake{err: errors.New("provider down")})
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u-2", "--kind", "new-verb", "--files", "3", "--packages", "1",
		"--lanes", "1", "--usage", usage, "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("a provider error falls back to the rules, exit = %d (stderr=%q)", code, stderr.String())
	}
	raw, err := os.ReadFile(usage)
	if err != nil {
		t.Fatalf("a failed call left no evidence: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("a header and one row, got %d:\n%s", len(lines), raw)
	}
	head, row := strings.Split(lines[0], "\t"), strings.Split(lines[1], "\t")
	got := map[string]string{}
	for i, name := range head {
		got[name] = row[i]
	}
	if got["rc"] == "0" {
		t.Errorf("a failed call is not an rc of 0: %v", got)
	}
	for _, field := range []string{"tokens_in", "tokens_out"} {
		if got[field] != "-" {
			t.Errorf("usage %s = %q, want \"-\": the cost of a failed call is UNKNOWN, not zero", field, got[field])
		}
	}
}

// No call, no row: the rules alone spend nothing, and an empty usage file would
// be a claim that a call was made.
func TestRouteWritesNoUsageWithoutACall(t *testing.T) {
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "2",
		"--packages", "1", "--usage", usage}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if _, err := os.Stat(usage); err == nil {
		raw, _ := os.ReadFile(usage)
		t.Errorf("no provider call was made, so there is nothing to account for:\n%s", raw)
	}
}
