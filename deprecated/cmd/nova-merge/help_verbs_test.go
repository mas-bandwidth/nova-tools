package main

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

// keptVerbs is what nova-merge is after the per-PR lander role was retired (stream is
// the unit): the evidence a stream lands on, and nothing that lands one PR at a time.
var keptVerbs = []string{"batch", "classify", "fold", "gate", "read"}

// retiredVerbs left with the lander role. Each is an unknown subcommand now, so a stale
// script that still calls one stops on its first line rather than half-running.
var retiredVerbs = []string{
	"stack", "wait", "rebase", "run", "stop", "status", "dry-run", "packet", "integrate",
	"simulate", "queue", "react", "sweep", "receipt", "land", "init", "quickstart", "add",
	"add-branch",
}

// TestHelpListsExactlyTheKeptVerbs is the DONE-WHEN of the retirement: `nova-merge help`
// names exactly batch, classify, fold, gate and read (plus version, which prints the
// build identity and is no work verb).
func TestHelpListsExactlyTheKeptVerbs(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, Deps{}); code != 0 {
		t.Fatalf("help: exit %d\n%s", code, errb.String())
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "  nova-merge ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] == "version" {
			continue
		}
		seen[fields[1]] = true
	}
	var got []string
	for v := range seen {
		got = append(got, v)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(keptVerbs, " ") {
		t.Fatalf("help lists %v, want exactly %v", got, keptVerbs)
	}
}

// TestEveryRetiredVerbIsAnUnknownSubcommand: none of the lander verbs dispatches.
func TestEveryRetiredVerbIsAnUnknownSubcommand(t *testing.T) {
	t.Parallel()
	for _, verb := range retiredVerbs {
		var out, errb bytes.Buffer
		code := run([]string{verb, "--lane", t.TempDir()}, &out, &errb, Deps{})
		if code != 2 || !strings.Contains(errb.String(), `unknown subcommand "`+verb+`"`) {
			t.Errorf("%s: exit %d, stderr %q; want exit 2 and unknown subcommand", verb, code, errb.String())
		}
	}
}
