//go:build functional

package card_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestCopyLedgerPrepushRedEndsFailWithFindingAndNoPush verifies that when prepush
// tests fail, the copy ends fail with reason tests-red, finding carries the failing test
// names, and Harvest is never called (no push and no PR opened).
func TestCopyLedgerPrepushRedEndsFailWithFindingAndNoPush(t *testing.T) {
	t.Parallel()
	c, primary, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("ba", 20)

	harvestCalled := false
	prepushCalled := false

	led := &card.CopyLedger{
		Client:    c,
		Copy:      l.Copy,
		Bench:     "b",
		Token:     l.Token,
		PushToken: "ghp-bench",
		PrepushTest: func(_ context.Context, repoDir string, pkgs []string) (card.PrepushTestResult, error) {
			prepushCalled = true
			return card.PrepushTestResult{
				Passed:       false,
				FailingTests: []string{"TestAlpha", "TestBeta"},
			}, nil
		},
		Harvest: func(_ context.Context, _ harvestcopy.Request) (harvestcopy.Result, error) {
			harvestCalled = true
			t.Error("harvest must not be called when prepush tests fail")
			return harvestcopy.Result{}, nil
		},
	}

	end := doneEnd(t, sha)
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}

	if !prepushCalled {
		t.Fatal("prepush test runner was not called")
	}
	if harvestCalled {
		t.Fatal("harvest was called on red prepush test")
	}

	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "fail" {
		t.Fatalf("copy record where=%q, want fail", rec["where"])
	}
	wantWhy := "tests-red: TestAlpha TestBeta"
	if rec["why"] != wantWhy {
		t.Fatalf("copy record why=%q, want %q", rec["why"], wantWhy)
	}
	wantFinding := "TestAlpha TestBeta"
	if rec["finding"] != wantFinding {
		t.Fatalf("copy record finding=%q, want %q", rec["finding"], wantFinding)
	}
	if rec["pr"] != "" {
		t.Fatalf("copy record has pr=%q on fail", rec["pr"])
	}

	p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
	if p["where"] != "review" || p["pr"] != "" {
		t.Fatalf("primary %v, want review with no PR", p)
	}
}

// TestCopyLedgerPrepushGreenPushesAndCarriesReceiptInPR verifies that when prepush
// tests pass, Harvest is called with the test receipt, the PR is opened, and the copy ends ok.
func TestCopyLedgerPrepushGreenPushesAndCarriesReceiptInPR(t *testing.T) {
	t.Parallel()
	c, primary, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("dc", 20)

	var got harvestcopy.Request
	harvestCalled := false
	prepushCalled := false

	const receipt = "make test PKGS=./internal/ci ./internal/docs ./internal/nsprint/card GOTEST_COUNT_FLAG= pass"

	led := &card.CopyLedger{
		Client:    c,
		Copy:      l.Copy,
		Bench:     "b",
		Token:     l.Token,
		PushToken: "ghp-bench",
		Now:       func() time.Time { return time.UnixMilli(1700000000000) },
		PrepushTest: func(_ context.Context, repoDir string, pkgs []string) (card.PrepushTestResult, error) {
			prepushCalled = true
			return card.PrepushTestResult{
				Passed:  true,
				Receipt: receipt,
			}, nil
		},
		Harvest: func(_ context.Context, r harvestcopy.Request) (harvestcopy.Result, error) {
			harvestCalled = true
			got = r
			return harvestcopy.Result{
				Repo:   "mas-bandwidth/nova-tools",
				Branch: r.Branch,
				Head:   r.SHA,
				PR:     9876,
				URL:    "http://127.0.0.1/pr/9876",
				Push:   "pushed",
				Open:   "opened",
				Title:  "prepush quality title",
				Body:   "TESTS: " + receipt + "\n",
			}, nil
		},
	}

	end := doneEnd(t, sha)
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}

	if !prepushCalled {
		t.Fatal("prepush test runner was not called")
	}
	if !harvestCalled {
		t.Fatal("harvest was not called on green prepush test")
	}
	if got.Tests != receipt {
		t.Fatalf("harvest got Tests=%q, want %q", got.Tests, receipt)
	}

	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "ok" || rec["pr"] != "9876" || rec["head"] != sha {
		t.Fatalf("copy record %v, want ok with pr=9876 and head=%s", rec, sha)
	}

	p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
	if p["where"] != "review" || p["pr"] != "9876" || p["head"] != sha {
		t.Fatalf("primary %v, want review with pr=9876", p)
	}
}

// TestCopyLedgerPrepushTimeoutEndsFailTestsRed verifies that when prepush
// test execution exceeds the deadline, the copy ends fail with tests-red.
func TestCopyLedgerPrepushTimeoutEndsFailTestsRed(t *testing.T) {
	t.Parallel()
	c, primary, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("fe", 20)

	led := &card.CopyLedger{
		Client:    c,
		Copy:      l.Copy,
		Bench:     "b",
		Token:     l.Token,
		PushToken: "ghp-bench",
		PrepushTest: func(_ context.Context, repoDir string, pkgs []string) (card.PrepushTestResult, error) {
			return card.PrepushTestResult{
				Passed:       false,
				FailingTests: []string{"timeout after 2m0s"},
			}, context.DeadlineExceeded
		},
		Harvest: func(_ context.Context, _ harvestcopy.Request) (harvestcopy.Result, error) {
			t.Error("harvest must not be called on timeout")
			return harvestcopy.Result{}, nil
		},
	}

	end := doneEnd(t, sha)
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}

	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "fail" {
		t.Fatalf("copy record where=%q, want fail", rec["where"])
	}
	if !strings.HasPrefix(rec["why"], "tests-red: ") {
		t.Fatalf("copy record why=%q, want prefix tests-red: ", rec["why"])
	}
	if rec["finding"] != "timeout after 2m0s" {
		t.Fatalf("copy record finding=%q, want 'timeout after 2m0s'", rec["finding"])
	}

	p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
	if p["where"] != "review" || p["pr"] != "" {
		t.Fatalf("primary %v, want review with no PR", p)
	}
}

// TestCopyLedgerPrepushRealMakefileTrigger verifies that hasMakefile triggers
// the prepush test runner when Makefile is present on disk even if PrepushTest is nil.
func TestCopyLedgerPrepushRealMakefileTrigger(t *testing.T) {
	t.Parallel()
	c, _, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("11", 20)

	end := doneEnd(t, sha)
	// Write a Makefile in end.RepoDir that fails
	mkContent := "test:\n" +
		"\t@echo '{\"Action\":\"run\",\"Package\":\"pkg/dummy\",\"Test\":\"TestFailInMake\"}' > $${RUNNER_TEMP}/test.json\n" +
		"\t@echo '{\"Action\":\"fail\",\"Package\":\"pkg/dummy\",\"Test\":\"TestFailInMake\"}' >> $${RUNNER_TEMP}/test.json\n" +
		"\t@exit 1\n"
	if err := os.WriteFile(filepath.Join(end.RepoDir, "Makefile"), []byte(mkContent), 0o644); err != nil {
		t.Fatal(err)
	}

	led := &card.CopyLedger{
		Client:    c,
		Copy:      l.Copy,
		Bench:     "b",
		Token:     l.Token,
		PushToken: "ghp-bench",
		Harvest: func(_ context.Context, _ harvestcopy.Request) (harvestcopy.Result, error) {
			t.Error("harvest must not be called when make test fails")
			return harvestcopy.Result{}, nil
		},
	}

	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}

	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "fail" {
		t.Fatalf("copy record where=%q, want fail", rec["where"])
	}
	if !strings.HasPrefix(rec["why"], "tests-red: ") {
		t.Fatalf("copy record why=%q, want prefix tests-red:", rec["why"])
	}
	if rec["finding"] != "TestFailInMake" {
		t.Fatalf("copy record finding=%q, want 'TestFailInMake'", rec["finding"])
	}
}
