package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 22, the REJECT, RESET, RESTORE, RETRY half, against a real bare remote and
// two real lane clones.
//
// The failure this pins is the one a fixture taught on 2026-09-11: after a rejected push
// the loser's new record was tracked in its local commit, the reset to the winner's tip
// removed it, and the next add failed with `pathspec ... did not match any files`. The
// outbox is what puts the bytes back, so a mutation that drops the restore turns this red.

// outboxBytes reads what a lane's outbox holds right now, keyed by file name. It is how
// this test learns the EXACT bytes a writer's outbox held, before delivery empties it.
func outboxBytes(lane string) map[string][]byte {
	out := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(lane, merge.OutboxDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(lane, merge.OutboxDir, e.Name()))
		if err != nil {
			continue
		}
		out[e.Name()] = body
	}
	return out
}

// destOf reads the `file` field the record itself carries: where these bytes belong in the
// lane branch.
func destOf(t *testing.T, body []byte) string {
	t.Helper()
	var probe struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("an outbox item is not a record: %v", err)
	}
	if probe.File == "" {
		t.Fatalf("an outbox item names no path: %s", body)
	}
	return probe.File
}

// recordFiles is every record file a checkout of the lane branch holds, as slash-separated
// paths: the reads and the gates, never their summaries.
func recordFiles(t *testing.T, checkout string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{merge.ReadsDir, merge.GatesDir} {
		root := filepath.Join(checkout, dir)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			rel, relErr := filepath.Rel(checkout, p)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

// onBranch reports whether the lane branch at the remote holds this path.
func onBranch(t *testing.T, l *lab, path string) bool {
	t.Helper()
	side, err := os.MkdirTemp(l.dir, "verify-branch")
	if err != nil {
		t.Fatal(err)
	}
	l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	_, statErr := os.Stat(filepath.Join(side, filepath.FromSlash(path)))
	return statErr == nil
}

// outboxNames is what the lane's outbox holds right now.
func outboxNames(t *testing.T, lane string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(lane, merge.OutboxDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
