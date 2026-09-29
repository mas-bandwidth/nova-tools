//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// getHeadCommit resolves the repository's current exact HEAD commit SHA.
func getHeadCommit(t *testing.T, repoRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get git HEAD commit: %v", err)
	}
	head := strings.TrimSpace(string(out))
	if len(head) != 40 {
		t.Fatalf("unexpected git HEAD commit SHA: %q", head)
	}
	return head
}

// setupCardTableWithEpoch initializes a test table schema with standard card lifecycle columns
// and optional epoch configuration through the public create API.
func setupCardTableWithEpoch(t *testing.T, binPath, addr, table, epochKey, epochField string) {
	t.Helper()
	args := []string{"create", table,
		"--columns", "waiting,ready,working,review,merging,landed,done",
		"--member-prefix", "card:",
		"--redis", addr,
	}
	if epochKey != "" {
		args = append(args, "--epoch-key", epochKey)
	}
	if epochField != "" {
		args = append(args, "--epoch-field", epochField)
	}
	createCmd := exec.Command(binPath, args...)
	if out, err := createCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create table %s: %v\noutput:\n%s", table, err, string(out))
	}

	rowCmd := exec.Command(binPath, "row", "add", table, "stream-1", "--redis", addr)
	if out, err := rowCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to add row stream-1 to table %s: %v\noutput:\n%s", table, err, string(out))
	}
}

// setupCardTable initializes a test table schema with standard card lifecycle columns.
func setupCardTable(t *testing.T, binPath, addr, table string) {
	setupCardTableWithEpoch(t, binPath, addr, table, "", "")
}

// runCardCmd executes scripts/card-manager.py with explicit test environment flags.
func runCardCmd(t *testing.T, repoRoot, binPath, addr string, args ...string) (int, string, string) {
	t.Helper()
	scriptPath := filepath.Join(repoRoot, "scripts", "card-manager.py")
	fullArgs := append([]string{scriptPath}, args...)
	fullArgs = append(fullArgs, "--redis", addr)

	cmd := exec.Command("python3", fullArgs...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"NOVA_TABLE_BIN="+binPath,
		"NOVA_REDIS_ADDR="+addr,
		"NOVA_SPRINT_REDIS="+addr,
		"PYTHONDONTWRITEBYTECODE=1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), stdout.String(), stderr.String()
		}
		t.Fatalf("failed to run card-manager.py: %v", err)
	}
	return 0, stdout.String(), stderr.String()
}

// setupCardInReview admits a card into stream-1:waiting and transitions it to stream-1:review.
func setupCardInReview(t *testing.T, repoRoot, binPath, addr, table, cardID, headCommit string) (string, string) {
	t.Helper()
	cardFile := filepath.Join(t.TempDir(), cardID+".card")
	cardContent := fmt.Sprintf(`%s
SCHEMA: v2
ID: %s
ENTRY: work/stream-1/%s
TITLE: Card057 regression fixture
KIND: fix-red
PATHS: internal/card/test.go
DEPENDS-ON: -
TIER: flash
TEST: internal/card:TestFixture
DONE-WHEN: Verified
DOORS: none
PROBES: none

# Brief
Card057 evidence-scope invariant test fixture.
`, cardID, cardID, cardID)
	if err := os.WriteFile(cardFile, []byte(cardContent), 0600); err != nil {
		t.Fatal(err)
	}

	admitFile := filepath.Join(t.TempDir(), "admit.json")
	admitManifest := map[string]any{
		"schema":       1,
		"table":        table,
		"epoch":        "0",
		"operation_id": "op-admit-" + cardID,
		"admissions": []map[string]any{
			{
				"id":     cardID,
				"stream": "stream-1",
				"file":   cardFile,
			},
		},
	}
	admitData, err := json.Marshal(admitManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admitFile, admitData, 0600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "add", "--admissions", admitFile, "--table", table)
	if code != 0 {
		t.Fatalf("card add failed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	moveFile := filepath.Join(t.TempDir(), "move_to_review.json")
	moveManifest := map[string]any{
		"schema":       1,
		"table":        table,
		"epoch":        "0",
		"operation_id": "op-review-" + cardID,
		"events": []map[string]any{
			{
				"id":              cardID,
				"to":              "review",
				"expect_place":    "stream-1:waiting",
				"expect_revision": "1",
				"head":            headCommit,
			},
		},
	}
	mData, err := json.Marshal(moveManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moveFile, mData, 0600); err != nil {
		t.Fatal(err)
	}

	mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", table)
	if mCode != 0 {
		t.Fatalf("card move to review failed: code=%d stdout=%s stderr=%s", mCode, mStdout, mStderr)
	}

	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	digest, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cardID), "digest").Result()
	if err != nil || digest == "" {
		t.Fatalf("failed to retrieve card digest from redis: %v", err)
	}
	return cardID, digest
}

// TestCard057EvidenceInvalidEpoch verifies that recording evidence with invalid epochs
// (non-decimal, negative, overflow > 2^64-1, leading zeroes, non-string) refuses with cause=invalid_epoch.
func TestCard057EvidenceInvalidEpoch(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	cases := []struct {
		name  string
		epoch any
		desc  string
	}{
		{"NonDecimalLetters", "abc", "alphabetical epoch string"},
		{"NonDecimalAlphaNumeric", "12a", "alphanumeric epoch string"},
		{"NonDecimalHex", "0x10", "hexadecimal epoch string"},
		{"NonDecimalEmpty", "", "empty epoch string"},
		{"NonDecimalFloat", "1.5", "floating point epoch string"},
		{"NonDecimalIntegerType", 123, "integer type in json instead of string"},
		{"NegativeMinusOne", "-1", "negative epoch string -1"},
		{"NegativeMinusHundred", "-100", "negative epoch string -100"},
		{"OverflowUint64PlusOne", "18446744073709551616", "2^64 exact overflow"},
		{"OverflowTwentyDigits", "99999999999999999999", "20 digits exceeding 2^64-1"},
		{"OverflowTwentyOneDigits", "184467440737095516150", "21 digits overflow"},
		{"LeadingZeroes01", "01", "leading zero '01'"},
		{"LeadingZeroes00", "00", "leading zeroes '00'"},
		{"LeadingZeroes007", "007", "leading zeroes '007'"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr := throwaway(t)
			ctx := context.Background()
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			t.Cleanup(func() { rdb.Close() })

			tbl := "cards_invalid_epoch"
			setupCardTable(t, binPath, addr, tbl)

			manifestFile := filepath.Join(t.TempDir(), "evidence.json")
			manifestData := map[string]any{
				"schema":       1,
				"table":        tbl,
				"epoch":        tc.epoch,
				"operation_id": "op-ev-" + strings.ToLower(tc.name),
				"evidence": []map[string]any{
					{
						"card_id":     "card-test",
						"head":        headCommit,
						"digest":      "1111111111111111111111111111111111111111111111111111111111111111",
						"reader":      "reviewer-1",
						"disposition": "accepted",
						"ci_status":   "pass",
					},
				},
			}
			data, err := json.Marshal(manifestData)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestFile, data, 0600); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", manifestFile, "--table", tbl)
			if code != 1 {
				t.Fatalf("%s (%s): expected exit code 1, got %d; stdout=%s; stderr=%s", tc.name, tc.desc, code, stdout, stderr)
			}
			if !strings.Contains(stderr, "cause=invalid_epoch") {
				t.Fatalf("%s: expected stderr to contain 'cause=invalid_epoch', got:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "expected=uint64_decimal") {
				t.Fatalf("%s: expected stderr to contain 'expected=uint64_decimal', got:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "PREFLIGHT REFUSED") {
				t.Fatalf("%s: expected stderr to contain 'PREFLIGHT REFUSED', got:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "changed=no") {
				t.Fatalf("%s: expected stderr to contain 'changed=no', got:\n%s", tc.name, stderr)
			}

			// Invariance verification: zero evidence records created in Redis
			keys, err := rdb.Keys(ctx, "evidence:*").Result()
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("%s: expected zero evidence keys in Redis, found %v", tc.name, keys)
			}
		})
	}
}

// TestCard057EvidenceStaleEpoch verifies that recording evidence with an epoch differing
// from the active table epoch refuses with cause=stale_epoch.
func TestCard057EvidenceStaleEpoch(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	t.Run("DefaultEpochZeroWithNonZeroManifest", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		ctx := context.Background()
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { rdb.Close() })

		tbl := "cards_stale_epoch_0"
		setupCardTable(t, binPath, addr, tbl)

		manifestFile := filepath.Join(t.TempDir(), "evidence.json")
		manifestData := map[string]any{
			"schema":       1,
			"table":        tbl,
			"epoch":        "1",
			"operation_id": "op-ev-stale-1",
			"evidence": []map[string]any{
				{
					"card_id":     "card-test",
					"head":        headCommit,
					"digest":      "1111111111111111111111111111111111111111111111111111111111111111",
					"reader":      "reviewer-1",
					"disposition": "accepted",
					"ci_status":   "pass",
				},
			},
		}
		data, err := json.Marshal(manifestData)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestFile, data, 0600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", manifestFile, "--table", tbl)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d; stdout=%s; stderr=%s", code, stdout, stderr)
		}
		if !strings.Contains(stderr, "cause=stale_epoch") {
			t.Fatalf("expected stderr to contain 'cause=stale_epoch', got:\n%s", stderr)
		}
		if !strings.Contains(stderr, "expected=0") || !strings.Contains(stderr, "observed=1") {
			t.Fatalf("expected stderr to contain expected=0 observed=1, got:\n%s", stderr)
		}

		keys, err := rdb.Keys(ctx, "evidence:*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 0 {
			t.Fatalf("expected zero evidence keys in Redis, found %v", keys)
		}
	})

	t.Run("ConfiguredEpochDomainWithStalePriorEpoch", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		ctx := context.Background()
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { rdb.Close() })

		tbl := "cards_stale_epoch_custom"
		setupCardTableWithEpoch(t, binPath, addr, tbl, "domain:epoch:active", "n")

		if err := rdb.HSet(ctx, "domain:epoch:active", "n", "5").Err(); err != nil {
			t.Fatal(err)
		}

		for _, staleEpoch := range []string{"0", "4", "6"} {
			manifestFile := filepath.Join(t.TempDir(), fmt.Sprintf("evidence_%s.json", staleEpoch))
			manifestData := map[string]any{
				"schema":       1,
				"table":        tbl,
				"epoch":        staleEpoch,
				"operation_id": "op-ev-stale-" + staleEpoch,
				"evidence": []map[string]any{
					{
						"card_id":     "card-test",
						"head":        headCommit,
						"digest":      "1111111111111111111111111111111111111111111111111111111111111111",
						"reader":      "reviewer-1",
						"disposition": "accepted",
						"ci_status":   "pass",
					},
				},
			}
			data, err := json.Marshal(manifestData)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestFile, data, 0600); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", manifestFile, "--table", tbl)
			if code != 1 {
				t.Fatalf("staleEpoch %s: expected exit code 1, got %d; stdout=%s; stderr=%s", staleEpoch, code, stdout, stderr)
			}
			if !strings.Contains(stderr, "cause=stale_epoch") {
				t.Fatalf("staleEpoch %s: expected stderr to contain 'cause=stale_epoch', got:\n%s", staleEpoch, stderr)
			}
			wantExpected := "expected=5"
			wantObserved := fmt.Sprintf("observed=%s", staleEpoch)
			if !strings.Contains(stderr, wantExpected) || !strings.Contains(stderr, wantObserved) {
				t.Fatalf("staleEpoch %s: expected %s %s, got:\n%s", staleEpoch, wantExpected, wantObserved, stderr)
			}
		}

		keys, err := rdb.Keys(ctx, "evidence:*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 0 {
			t.Fatalf("expected zero evidence keys in Redis, found %v", keys)
		}
	})
}

// TestCard057MoveStaleDigest verifies that when a card definition/digest changes after
// evidence was recorded, attempting to move the card to merging rejects the stale evidence
// and keeps the card in review without mutation.
func TestCard057MoveStaleDigest(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	addr := throwaway(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })

	tbl := "cards_stale_digest"
	setupCardTable(t, binPath, addr, tbl)

	cid, digest := setupCardInReview(t, repoRoot, binPath, addr, tbl, "card-stale-digest", headCommit)

	// Record valid reader evidence matching the initial digest and exact commit head
	evFile := filepath.Join(t.TempDir(), "evidence_valid.json")
	evManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-ev-valid",
		"evidence": []map[string]any{
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-1",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-2",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
		},
	}
	evData, err := json.Marshal(evManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evFile, evData, 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", evFile, "--table", tbl)
	if code != 0 {
		t.Fatalf("expected evidence recording to succeed, got exit code %d; stdout=%s; stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "outcome=recorded") {
		t.Fatalf("expected stdout to contain outcome=recorded, got:\n%s", stdout)
	}

	prePlace, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
	if err != nil || prePlace != "stream-1:review" {
		t.Fatalf("expected pre-move place to be stream-1:review, got %q (err=%v)", prePlace, err)
	}
	preRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
	if err != nil {
		t.Fatal(err)
	}
	preTableRev, err := rdb.HGet(ctx, fmt.Sprintf("table:%s:revision", tbl), "n").Result()
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the card digest in Redis (simulating card definition updated after review approval)
	mutatedDigest := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := rdb.HSet(ctx, fmt.Sprintf("card:%s", cid), "digest", mutatedDigest).Err(); err != nil {
		t.Fatal(err)
	}

	// Attempt move from review to merging
	moveFile := filepath.Join(t.TempDir(), "move_merging.json")
	moveManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-move-to-merging",
		"events": []map[string]any{
			{
				"id":              cid,
				"to":              "merging",
				"expect_place":    "stream-1:review",
				"expect_revision": preRev,
				"head":            headCommit,
			},
		},
	}
	moveData, err := json.Marshal(moveManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moveFile, moveData, 0600); err != nil {
		t.Fatal(err)
	}

	mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", tbl)
	if mCode != 1 {
		t.Fatalf("expected move with stale digest to exit code 1, got %d; stdout=%s; stderr=%s", mCode, mStdout, mStderr)
	}
	if !strings.Contains(mStderr, "cause=insufficient_evidence") {
		t.Fatalf("expected stderr to contain 'cause=insufficient_evidence', got:\n%s", mStderr)
	}
	if !strings.Contains(mStderr, "PREFLIGHT REFUSED") {
		t.Fatalf("expected stderr to contain 'PREFLIGHT REFUSED', got:\n%s", mStderr)
	}

	// Card remains in review; revisions unchanged
	postPlace, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
	if err != nil || postPlace != "stream-1:review" {
		t.Fatalf("card place was altered! expected stream-1:review, got %q (err=%v)", postPlace, err)
	}
	postRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
	if err != nil || postRev != preRev {
		t.Fatalf("card revision was altered! expected %s, got %q (err=%v)", preRev, postRev, err)
	}
	postTableRev, err := rdb.HGet(ctx, fmt.Sprintf("table:%s:revision", tbl), "n").Result()
	if err != nil || postTableRev != preTableRev {
		t.Fatalf("table revision was altered! expected %s, got %q (err=%v)", preTableRev, postTableRev, err)
	}

	// Verify cell membership: review still has card, merging does not
	if _, err := rdb.ZScore(ctx, fmt.Sprintf("table:%s:cell:stream-1:review", tbl), cid).Result(); err != nil {
		t.Fatalf("card missing from table review cell: %v", err)
	}
	if _, err := rdb.ZScore(ctx, fmt.Sprintf("table:%s:cell:stream-1:merging", tbl), cid).Result(); err == nil {
		t.Fatalf("card unexpectedly present in merging cell!")
	}
}

// TestCard057MoveMismatchedTableInEvidence verifies that evidence recorded under another table
// is rejected during batch apply/move, keeping the card in review.
func TestCard057MoveMismatchedTableInEvidence(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	addr := throwaway(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })

	tbl := "cards_table_scope"
	setupCardTable(t, binPath, addr, tbl)

	cid, digest := setupCardInReview(t, repoRoot, binPath, addr, tbl, "card-table-mismatch", headCommit)

	evFile := filepath.Join(t.TempDir(), "evidence_valid.json")
	evManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-ev-scope",
		"evidence": []map[string]any{
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-1",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-2",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
		},
	}
	evData, err := json.Marshal(evManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evFile, evData, 0600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", evFile, "--table", tbl)
	if code != 0 {
		t.Fatalf("expected evidence recording to succeed, got %d; stdout=%s; stderr=%s", code, stdout, stderr)
	}

	preRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
	if err != nil {
		t.Fatal(err)
	}

	// Mismatched table in evidence: mutate table field of both evidence records in Redis
	evKeys, err := rdb.Keys(ctx, fmt.Sprintf("evidence:%s:*", cid)).Result()
	if err != nil {
		t.Fatal(err)
	}
	mutatedKeys := 0
	for _, k := range evKeys {
		if strings.HasSuffix(k, ":readers") {
			continue
		}
		if err := rdb.HSet(ctx, k, "table", "other_foreign_table").Err(); err != nil {
			t.Fatal(err)
		}
		mutatedKeys++
	}
	if mutatedKeys < 2 {
		t.Fatalf("expected at least 2 evidence keys to mutate, found %d", mutatedKeys)
	}

	moveFile := filepath.Join(t.TempDir(), "move_merging.json")
	moveManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-move-table-mismatch",
		"events": []map[string]any{
			{
				"id":              cid,
				"to":              "merging",
				"expect_place":    "stream-1:review",
				"expect_revision": preRev,
				"head":            headCommit,
			},
		},
	}
	moveData, err := json.Marshal(moveManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moveFile, moveData, 0600); err != nil {
		t.Fatal(err)
	}

	mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", tbl)
	if mCode != 1 {
		t.Fatalf("expected move with mismatched evidence table to exit code 1, got %d; stdout=%s; stderr=%s", mCode, mStdout, mStderr)
	}
	if !strings.Contains(mStderr, "cause=insufficient_evidence") {
		t.Fatalf("expected stderr to contain 'cause=insufficient_evidence', got:\n%s", mStderr)
	}

	place, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
	if err != nil || place != "stream-1:review" {
		t.Fatalf("expected card to remain in stream-1:review, got %q (err=%v)", place, err)
	}

	// Subtest 1: Evidence recording against non-existent table -> cause=table_not_found
	t.Run("EvidenceRecordingNonExistentTable", func(t *testing.T) {
		code, _, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", evFile, "--table", "non_existent_table_123")
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr, "cause=table_not_found") {
			t.Fatalf("expected cause=table_not_found, got:\n%s", stderr)
		}
	})

	// Subtest 2: Evidence recording on table where card is not placed -> cause=card_not_in_table
	t.Run("EvidenceRecordingCardNotInTable", func(t *testing.T) {
		otherTbl := "cards_other_empty"
		setupCardTable(t, binPath, addr, otherTbl)

		otherEvFile := filepath.Join(t.TempDir(), "evidence_other_table.json")
		otherEvManifest := map[string]any{
			"schema":       1,
			"table":        otherTbl,
			"epoch":        "0",
			"operation_id": "op-ev-other-table",
			"evidence": []map[string]any{
				{
					"card_id":     cid,
					"head":        headCommit,
					"digest":      digest,
					"reader":      "reviewer-1",
					"disposition": "accepted",
					"ci_status":   "pass",
				},
			},
		}
		data, _ := json.Marshal(otherEvManifest)
		_ = os.WriteFile(otherEvFile, data, 0600)

		code, _, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", otherEvFile, "--table", otherTbl)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr, "cause=card_not_in_table") {
			t.Fatalf("expected cause=card_not_in_table, got:\n%s", stderr)
		}
	})
}

// TestCard057EvidenceScopeHappyPath verifies that when table, epoch, and digest all match,
// evidence is recorded cleanly and card move to merging succeeds.
func TestCard057EvidenceScopeHappyPath(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	addr := throwaway(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })

	tbl := "cards_happy_path"
	setupCardTable(t, binPath, addr, tbl)

	cid, digest := setupCardInReview(t, repoRoot, binPath, addr, tbl, "card-happy", headCommit)

	evFile := filepath.Join(t.TempDir(), "evidence_happy.json")
	evManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-ev-happy",
		"evidence": []map[string]any{
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-happy-1",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
			{
				"card_id":     cid,
				"head":        headCommit,
				"digest":      digest,
				"reader":      "reviewer-happy-2",
				"disposition": "accepted",
				"ci_status":   "pass",
			},
		},
	}
	evData, _ := json.Marshal(evManifest)
	_ = os.WriteFile(evFile, evData, 0600)
	code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", evFile, "--table", tbl)
	if code != 0 {
		t.Fatalf("evidence recording failed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "outcome=recorded") {
		t.Fatalf("expected outcome=recorded, got:\n%s", stdout)
	}

	preRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
	if err != nil {
		t.Fatal(err)
	}

	moveFile := filepath.Join(t.TempDir(), "move_happy.json")
	moveManifest := map[string]any{
		"schema":       1,
		"table":        tbl,
		"epoch":        "0",
		"operation_id": "op-move-happy",
		"events": []map[string]any{
			{
				"id":              cid,
				"to":              "merging",
				"expect_place":    "stream-1:review",
				"expect_revision": preRev,
				"head":            headCommit,
			},
		},
	}
	moveData, _ := json.Marshal(moveManifest)
	_ = os.WriteFile(moveFile, moveData, 0600)

	mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", tbl)
	if mCode != 0 {
		t.Fatalf("expected move to succeed, got code %d: stdout=%s stderr=%s", mCode, mStdout, mStderr)
	}
	if !strings.Contains(mStdout, "outcome=changed") {
		t.Fatalf("expected outcome=changed, got:\n%s", mStdout)
	}

	postPlace, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
	if err != nil || postPlace != "stream-1:merging" {
		t.Fatalf("expected card to be in stream-1:merging, got %q (err=%v)", postPlace, err)
	}
}

// TestCard057MalformedActiveEpoch verifies that when a table's configured active epoch domain
// contains a non-uint64 active epoch, recording evidence is refused with cause=invalid_active_epoch.
func TestCard057MalformedActiveEpoch(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	cases := []struct {
		name        string
		activeEpoch string
		desc        string
	}{
		{"NonDecimalLetters", "bad-epoch", "alphabetic active epoch string"},
		{"NonDecimalAlphaNumeric", "12a", "alphanumeric active epoch string"},
		{"NegativeMinusOne", "-1", "negative active epoch string -1"},
		{"LeadingZeroes01", "01", "leading zero active epoch '01'"},
		{"OverflowUint64PlusOne", "18446744073709551616", "2^64 exact overflow"},
		{"OverflowTwentyDigits", "99999999999999999999", "20 digits exceeding 2^64-1"},
		{"NonDecimalFloat", "2.5", "floating point active epoch string"},
		{"NonDecimalHex", "0x20", "hexadecimal active epoch string"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr := throwaway(t)
			ctx := context.Background()
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			t.Cleanup(func() { rdb.Close() })

			tbl := "cards_malformed_active_epoch_" + strings.ToLower(tc.name)
			epochKey := "domain:epoch:" + tbl
			setupCardTableWithEpoch(t, binPath, addr, tbl, epochKey, "n")

			// Populate active epoch with malformed value
			if err := rdb.HSet(ctx, epochKey, "n", tc.activeEpoch).Err(); err != nil {
				t.Fatal(err)
			}

			manifestFile := filepath.Join(t.TempDir(), "evidence.json")
			manifestData := map[string]any{
				"schema":       1,
				"table":        tbl,
				"epoch":        "0",
				"operation_id": "op-ev-malformed-active-" + strings.ToLower(tc.name),
				"evidence": []map[string]any{
					{
						"card_id":     "card-test",
						"head":        headCommit,
						"digest":      "1111111111111111111111111111111111111111111111111111111111111111",
						"reader":      "reviewer-1",
						"disposition": "accepted",
						"ci_status":   "pass",
					},
				},
			}
			data, err := json.Marshal(manifestData)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestFile, data, 0600); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", manifestFile, "--table", tbl)
			if code != 1 {
				t.Fatalf("%s (%s): expected exit code 1, got %d; stdout=%s; stderr=%s", tc.name, tc.desc, code, stdout, stderr)
			}
			if !strings.Contains(stderr, "cause=invalid_active_epoch") {
				t.Fatalf("%s: expected stderr to contain 'cause=invalid_active_epoch', got:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "expected=uint64_decimal") {
				t.Fatalf("%s: expected stderr to contain 'expected=uint64_decimal', got:\n%s", tc.name, stderr)
			}
			wantObserved := fmt.Sprintf("observed=%s", tc.activeEpoch)
			if !strings.Contains(stderr, wantObserved) {
				t.Fatalf("%s: expected stderr to contain %q, got:\n%s", tc.name, wantObserved, stderr)
			}
			if !strings.Contains(stderr, "PREFLIGHT REFUSED") {
				t.Fatalf("%s: expected stderr to contain 'PREFLIGHT REFUSED', got:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "changed=no") {
				t.Fatalf("%s: expected stderr to contain 'changed=no', got:\n%s", tc.name, stderr)
			}

			// Invariance verification: zero evidence records created in Redis
			keys, err := rdb.Keys(ctx, "evidence:*").Result()
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("%s: expected zero evidence keys in Redis, found %v", tc.name, keys)
			}
		})
	}
}

// TestCard057StoredWrongEpochMergeRefusal verifies that the merge filter rejects
// persisted wrong-epoch evidence even if evidence recording was bypassed (e.g. direct Redis write)
// or if evidence was subsequently corrupted/mutated with a wrong epoch.
func TestCard057StoredWrongEpochMergeRefusal(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)
	headCommit := getHeadCommit(t, repoRoot)

	t.Run("DirectlyPersistedWrongEpochBypassingRecording", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		ctx := context.Background()
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { rdb.Close() })

		tbl := "cards_bypassed_wrong_epoch"
		setupCardTable(t, binPath, addr, tbl)

		cid, digest := setupCardInReview(t, repoRoot, binPath, addr, tbl, "card-bypassed-epoch", headCommit)

		preRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
		if err != nil {
			t.Fatal(err)
		}

		// Directly inject two reader approvals into Redis with wrong epoch "99" (bypassing card-manager.py evidence recording)
		readers := []string{"reviewer-bypass-1", "reviewer-bypass-2"}
		for _, r := range readers {
			evKey := fmt.Sprintf("evidence:%s:op-bypass:%s", cid, r)
			evData := map[string]any{
				"card_id":      cid,
				"table":        tbl,
				"operation_id": "op-bypass",
				"head":         headCommit,
				"digest":       digest,
				"reader":       r,
				"disposition":  "accepted",
				"ci_status":    "pass",
				"epoch":        "99", // Persisted wrong-epoch evidence!
			}
			if err := rdb.HSet(ctx, evKey, evData).Err(); err != nil {
				t.Fatal(err)
			}
			if err := rdb.SAdd(ctx, fmt.Sprintf("evidence:%s:readers", cid), r).Err(); err != nil {
				t.Fatal(err)
			}
		}

		// Attempt move to merging under table epoch "0"
		moveFile := filepath.Join(t.TempDir(), "move_merging.json")
		moveManifest := map[string]any{
			"schema":       1,
			"table":        tbl,
			"epoch":        "0",
			"operation_id": "op-move-bypassed-wrong-epoch",
			"events": []map[string]any{
				{
					"id":              cid,
					"to":              "merging",
					"expect_place":    "stream-1:review",
					"expect_revision": preRev,
					"head":            headCommit,
				},
			},
		}
		moveData, err := json.Marshal(moveManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(moveFile, moveData, 0600); err != nil {
			t.Fatal(err)
		}

		mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", tbl)
		if mCode != 1 {
			t.Fatalf("expected move with bypassed wrong-epoch evidence to exit code 1, got %d; stdout=%s; stderr=%s", mCode, mStdout, mStderr)
		}
		if !strings.Contains(mStderr, "cause=insufficient_evidence") {
			t.Fatalf("expected stderr to contain 'cause=insufficient_evidence', got:\n%s", mStderr)
		}
		if !strings.Contains(mStderr, "PREFLIGHT REFUSED") {
			t.Fatalf("expected stderr to contain 'PREFLIGHT REFUSED', got:\n%s", mStderr)
		}
		if !strings.Contains(mStderr, "changed=no") {
			t.Fatalf("expected stderr to contain 'changed=no', got:\n%s", mStderr)
		}

		// Ensure card place did not transition and remained in stream-1:review
		place, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
		if err != nil || place != "stream-1:review" {
			t.Fatalf("expected card to remain in stream-1:review, got %q (err=%v)", place, err)
		}
	})

	t.Run("RecordedEvidenceMutatedToWrongEpoch", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		ctx := context.Background()
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { rdb.Close() })

		tbl := "cards_mutated_wrong_epoch"
		setupCardTable(t, binPath, addr, tbl)

		cid, digest := setupCardInReview(t, repoRoot, binPath, addr, tbl, "card-mutated-epoch", headCommit)

		// Record valid evidence via card-manager.py under epoch 0
		evFile := filepath.Join(t.TempDir(), "evidence_valid.json")
		evManifest := map[string]any{
			"schema":       1,
			"table":        tbl,
			"epoch":        "0",
			"operation_id": "op-ev-valid",
			"evidence": []map[string]any{
				{
					"card_id":     cid,
					"head":        headCommit,
					"digest":      digest,
					"reader":      "reviewer-1",
					"disposition": "accepted",
					"ci_status":   "pass",
				},
				{
					"card_id":     cid,
					"head":        headCommit,
					"digest":      digest,
					"reader":      "reviewer-2",
					"disposition": "accepted",
					"ci_status":   "pass",
				},
			},
		}
		evData, err := json.Marshal(evManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(evFile, evData, 0600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runCardCmd(t, repoRoot, binPath, addr, "evidence", "--evidence", evFile, "--table", tbl)
		if code != 0 {
			t.Fatalf("expected evidence recording to succeed, got %d; stdout=%s; stderr=%s", code, stdout, stderr)
		}

		preRev, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), "revision").Result()
		if err != nil {
			t.Fatal(err)
		}

		// Mutate stored evidence in Redis to have wrong epoch "7"
		evKeys, err := rdb.Keys(ctx, fmt.Sprintf("evidence:%s:*", cid)).Result()
		if err != nil {
			t.Fatal(err)
		}
		mutated := 0
		for _, k := range evKeys {
			if strings.HasSuffix(k, ":readers") {
				continue
			}
			if err := rdb.HSet(ctx, k, "epoch", "7").Err(); err != nil {
				t.Fatal(err)
			}
			mutated++
		}
		if mutated < 2 {
			t.Fatalf("expected at least 2 evidence records mutated, got %d", mutated)
		}

		// Attempt move to merging
		moveFile := filepath.Join(t.TempDir(), "move_merging.json")
		moveManifest := map[string]any{
			"schema":       1,
			"table":        tbl,
			"epoch":        "0",
			"operation_id": "op-move-mutated-wrong-epoch",
			"events": []map[string]any{
				{
					"id":              cid,
					"to":              "merging",
					"expect_place":    "stream-1:review",
					"expect_revision": preRev,
					"head":            headCommit,
				},
			},
		}
		moveData, err := json.Marshal(moveManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(moveFile, moveData, 0600); err != nil {
			t.Fatal(err)
		}

		mCode, mStdout, mStderr := runCardCmd(t, repoRoot, binPath, addr, "move", "--events", moveFile, "--table", tbl)
		if mCode != 1 {
			t.Fatalf("expected move with mutated wrong-epoch evidence to exit code 1, got %d; stdout=%s; stderr=%s", mCode, mStdout, mStderr)
		}
		if !strings.Contains(mStderr, "cause=insufficient_evidence") {
			t.Fatalf("expected stderr to contain 'cause=insufficient_evidence', got:\n%s", mStderr)
		}

		place, err := rdb.HGet(ctx, fmt.Sprintf("card:%s", cid), fmt.Sprintf("place:%s", tbl)).Result()
		if err != nil || place != "stream-1:review" {
			t.Fatalf("expected card to remain in stream-1:review, got %q (err=%v)", place, err)
		}
	})
}
