package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE BATCH CONTRACT A CALLER READS (#3202, found dogfooding #1615).
//
// `batch --cards` requires --tokens since #1615, but the two places a caller reads it from
// still described the old contract: the help synopsis for the cards form did not name the
// flag, and the CLI's own flag gathering refused with `add`'s "for this job" sentence, so
// swarm.NoBatchTokensRefusal -- the one that says the word is per card and never divided --
// never reached a caller of the binary.
func TestBatchCardsHelpNamesTokens(t *testing.T) {
	exit, stdout, stderr := runSwarm(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-swarm help` must exit 0, got %d; stderr: %s", exit, stderr)
	}
	found := false
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.Contains(line, "--cards <file>") {
			continue
		}
		found = true
		if !strings.Contains(line, "--tokens <n>|unmetered") {
			t.Errorf("the help line for `batch --cards` must name `--tokens <n>|unmetered`, got:\n%s", line)
		}
	}
	if !found {
		t.Fatalf("`nova-swarm help` has no line naming `--cards <file>`:\n%s", stdout)
	}
}

func TestBatchCardsWithoutTokensPrintsTheBatchRefusal(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("RESULT: c1 nova-tools rebase onto dev\nKIND: rebase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(cards, []byte("c1\t1\tvendor/low-1\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	exit, _, stderr := runSwarm(t, "batch", "--id", "x", "--cards", cards,
		"--deadline", "30", "--runner", "true", "--root", root,
		"--no-route", "--reason", "test")
	if exit != 2 {
		t.Fatalf("`batch --cards` without --tokens must exit 2, got %d; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, swarm.NoBatchTokensRefusal) {
		t.Fatalf("`batch --cards` without --tokens must print swarm.NoBatchTokensRefusal verbatim, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "for this job") {
		t.Errorf("the batch refusal must not also print `add`'s per-job sentence, got:\n%s", stderr)
	}
	if _, err := os.Stat(root); err == nil {
		t.Errorf("the refusal must come before any card starts, but %s was made", root)
	}
}
