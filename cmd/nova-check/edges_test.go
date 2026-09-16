package main

// `nova-check edges` end to end over a real ledger, through a fake creator: no network.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/edges"
)

type fakeCreator struct{ opened []edges.Issue }

func (f *fakeCreator) Create(repo string, issue edges.Issue) (string, error) {
	f.opened = append(f.opened, issue)
	return "https://example.invalid/issues/1", nil
}

// TestEdgesVerbFilesOneIssuePerDistinctRow is the verb's round trip: rows in, issues out,
// rows marked, second run silent.
func TestEdgesVerbFilesOneIssuePerDistinctRow(t *testing.T) {
	queue := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := edges.Record(queue, "nova-pulse", "reap", "nova-pulse reap: --queue is required", "the verb to say what it wants"); err != nil {
			t.Fatal(err)
		}
	}
	if err := edges.Record(queue, "nova-swarm", "batch", "nova-swarm batch: --pool is required", "a default of ./pool"); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCreator{}
	var out, errs strings.Builder
	if code := cmdEdgesWith([]string{"--queue", queue, "--repo", "mas-bandwidth/nova-tools"}, &out, &errs, fake); code != 0 {
		t.Fatalf("edges = %d: %s", code, errs.String())
	}
	if len(fake.opened) != 2 {
		t.Fatalf("opened %d issues, want 2", len(fake.opened))
	}
	if !strings.Contains(out.String(), "filed=2") {
		t.Errorf("the EDGES line:\n%s", out.String())
	}

	second := &fakeCreator{}
	out.Reset()
	if code := cmdEdgesWith([]string{"--queue", queue, "--repo", "mas-bandwidth/nova-tools"}, &out, &errs, second); code != 0 {
		t.Fatalf("the second run = %d", code)
	}
	if len(second.opened) != 0 {
		t.Errorf("the second run opened %d issues", len(second.opened))
	}
}

// TestEdgesVerbRefusesWithoutItsFlags: the no-guessing law, unchanged.
func TestEdgesVerbRefusesWithoutItsFlags(t *testing.T) {
	var out, errs strings.Builder
	if code := cmdEdgesWith(nil, &out, &errs, &fakeCreator{}); code != 2 {
		t.Fatalf("edges with no flags = %d, want 2", code)
	}
	for _, want := range []string{"--queue is required", "--repo is required"} {
		if !strings.Contains(errs.String(), want) {
			t.Errorf("stderr has no %q:\n%s", want, errs.String())
		}
	}
}

// TestEdgesVerbIsReachableFromRun: the verb is wired into the dispatcher and the usage.
func TestEdgesVerbIsReachableFromRun(t *testing.T) {
	var out, errs strings.Builder
	if code := run([]string{"edges"}, &out, &errs); code != 2 {
		t.Fatalf("`nova-check edges` with no flags = %d, want 2 (not an unknown subcommand)", code)
	}
	if strings.Contains(errs.String(), "unknown subcommand") {
		t.Errorf("edges is not wired into run:\n%s", errs.String())
	}
	if !strings.Contains(usage, "nova-check edges") {
		t.Error("the usage banner does not name the edges verb")
	}
}
