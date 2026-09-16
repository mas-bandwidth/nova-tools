package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ISSUE #644. A card the harness's own fence stopped is NOT a card whose model published
// nothing, and the two were one token -- `no-result` -- for every one of the eight cards
// that died this way on 2026-09-16.

// TestAFenceRejectionIsNeverNoResult: a card whose runner reported `fence=rejected` on its
// NATIVE OK line and published no RESULT.md scores `fence`, and the path it was stopped at
// is the remedy the line carries. RED WITHOUT THE CLASSIFIER: the same job scored
// `no-result`, which sends a reader to the model.
func TestAFenceRejectionIsNeverNoResult(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "1", "jobs", "a")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	line := "NATIVE OK label=a job=" + job + " tmp=/t rc=0 wall=1.00s sandbox=none-by-flag " +
		"card_sha256=aa binary_sha256=bb config=cc harness=ok fence=rejected path=/x/jobs/scratch/*\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	state, reason, tail, _ := scoreCard(root, batchCard{label: "a", slot: 1, contract: "a card line 1"}, false, false, 0, 0, filepath.Join(root, "1", "native.log"), "")
	if state != "abstain" || reason != "fence" {
		t.Fatalf("a card the fence stopped scores ABSTAIN reason=fence, got %s reason=%s", state, reason)
	}
	if tail != "path=/x/jobs/scratch/*" {
		t.Errorf("the reason carries the path the card was stopped at, got %q", tail)
	}
}

// TestFenceComesBeforeHarnessSilent: a run that was fenced AND left no word of its own scores
// `fence`, not `harness-silent`. The order matters because the two say opposite things to a
// reader: `harness-silent` sends them to the harness and the model (it never ran the card),
// and `fence` names the path this tool's own machinery shut. RED WITHOUT THE ORDERING: the
// silent check ran first and the card was read as a harness that never spoke.
func TestFenceComesBeforeHarnessSilent(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "1", "jobs", "a")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	line := "NATIVE OK label=a job=" + job + " tmp=/t rc=0 wall=1.00s sandbox=none-by-flag " +
		"card_sha256=aa binary_sha256=bb config=cc harness=silent fence=rejected path=/sys/kernel/security/*\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	state, reason, tail, _ := scoreCard(root, batchCard{label: "a", slot: 1, contract: "a card line 1"}, false, false, 0, 0, filepath.Join(root, "1", "native.log"), "")
	if state != "abstain" || reason != "fence" {
		t.Fatalf("a fenced card scores reason=fence even when the harness also left no words, got %s reason=%s", state, reason)
	}
	if tail != "path=/sys/kernel/security/*" {
		t.Errorf("the reason carries the path the card was stopped at, got %q", tail)
	}
}

// TestFenceRejectionReadsTheHarnesssOwnWords: the line OpenCode 1.18.20 prints, colours and
// all, is the one this parses; a capture with no rejection in it says so.
func TestFenceRejectionReadsTheHarnesssOwnWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "the harness's own line",
			in:   "\x1b[33;1m!\x1b[0m  permission requested: external_directory (/Users/glenn/rowan-working/swarm-root/1/jobs/*); auto-rejecting\nError: The user rejected permission to use this specific tool call.\n",
			want: "/Users/glenn/rowan-working/swarm-root/1/jobs/*", ok: true,
		},
		{
			name: "the no-wall bench's /sys read",
			in:   "permission requested: external_directory (/sys/kernel/security/*); auto-rejecting\n",
			want: "/sys/kernel/security/*", ok: true,
		},
		{
			name: "several patterns: the first is where the model stopped",
			in:   "permission requested: external_directory (/a/*, /b/*); auto-rejecting\n",
			want: "/a/*", ok: true,
		},
		{name: "a quiet capture", in: "the model said things\nand wrote a result\n"},
		{name: "an empty capture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FenceRejection([]byte(tc.in))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("FenceRejection = %q,%v; want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestFencePermissionNamesTheWholeJobAndNothingAboveIt: the block a job runs under names the
// job directory in both wildcard spellings and the read-only paths the card asked for, still
// asks about everything else, and NAMES NOTHING ABOVE THE JOB -- a sibling job in the same
// slot is another card's work, and a fence rule is no place to hand it over.
func TestFencePermissionNamesTheWholeJobAndNothingAboveIt(t *testing.T) {
	block := FencePermission("/root/1/jobs/a", []string{"/sys/kernel/security/lsm"})
	external, ok := block[FenceExternalDirectory].(map[string]any)
	if !ok {
		t.Fatalf("the block is keyed by the permission the harness asks under: %v", block)
	}
	for _, want := range []string{
		"/root/1/jobs/a/*", "/root/1/jobs/a/**",
		"/sys/kernel/security/lsm", "/sys/kernel/security/*",
	} {
		if external[want] != FenceAllow {
			t.Errorf("%s is allowed; the block holds %v", want, external)
		}
	}
	for _, never := range []string{"/root/1/jobs/*", "/root/1/jobs/**", "/root/1/*", "/root/*"} {
		if _, named := external[never]; named {
			t.Errorf("%s is ABOVE the job and is never named: %v", never, external)
		}
	}
	if external["*"] != FenceAsk {
		t.Errorf("every other path is still asked about: %v", external)
	}
}

// TestMergeFencePermissionKeepsTheCarriedConfig: a provider config a caller carried keeps
// its providers and its own rules, and the job's fence rules are added to them. A config
// this side cannot parse is returned unchanged, and says so.
func TestMergeFencePermissionKeepsTheCarriedConfig(t *testing.T) {
	carried := []byte(`{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}},"permission":{"read":{"*":"allow"},"external_directory":{"/opt/toolchains/*":"allow"}}}`)
	out, ok := MergeFencePermission(carried, "/root/1/jobs/a", nil)
	if !ok {
		t.Fatalf("a JSON object config merges: %s", out)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("the merged config is JSON: %v\n%s", err, out)
	}
	if _, named := cfg["provider"].(map[string]any)["ollama"]; !named {
		t.Errorf("the carried provider survives the merge:\n%s", out)
	}
	perm := cfg["permission"].(map[string]any)
	if _, named := perm["read"]; !named {
		t.Errorf("a person's own rules survive the merge:\n%s", out)
	}
	external := perm[FenceExternalDirectory].(map[string]any)
	if external["/opt/toolchains/*"] != FenceAllow {
		t.Errorf("a pattern the caller allowed is still allowed:\n%s", out)
	}
	if external["/root/1/jobs/a/**"] != FenceAllow {
		t.Errorf("the job's own directory is allowed:\n%s", out)
	}

	if got, ok := MergeFencePermission([]byte("not json at all"), "/root/1/jobs/a", nil); ok || string(got) != "not json at all" {
		t.Errorf("a config this side cannot read is carried verbatim and says so, got %q,%v", got, ok)
	}
}

// TestCardReadPathsReadsTheCardsOwnLine: the `READ:` line, and nothing inferred from prose.
func TestCardReadPathsReadsTheCardsOwnLine(t *testing.T) {
	card := []byte("do the thing\n" +
		"READ: /sys/kernel/security/lsm /proc/self/status\n" +
		"- READ: `/etc/os-release`\n" +
		"this card reads relative paths like repo/x too\n" +
		"READ: repo/x\n" +
		"READ: /sys/kernel/security/lsm\n")
	got := CardReadPaths(card)
	want := []string{"/sys/kernel/security/lsm", "/proc/self/status", "/etc/os-release"}
	if len(got) != len(want) {
		t.Fatalf("CardReadPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CardReadPaths = %v, want %v", got, want)
		}
	}
}
