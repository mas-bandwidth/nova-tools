package merge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tipGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func tipRead(who, head string) []byte {
	return []byte(fmt.Sprintf(`{"who":%q,"verdict":"hold","note":"","at":"2026-09-14T01:02:03Z","head":%q}`+"\n", who, head))
}

func fetchedTipLab(t *testing.T) (lane, tip, recordPath string) {
	t.Helper()
	lane = t.TempDir()
	tipGit(t, lane, "init", "-q")
	tipGit(t, lane, "config", "user.name", "Nova Test")
	tipGit(t, lane, "config", "user.email", "nova-test@example.invalid")
	head := strings.Repeat("a", 40)
	// Leading and trailing whitespace in the final name is legal Git path data.
	recordPath = filepath.Join(lane, ReadsDir, "951", " reader record .json")
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, tipRead("tip", head), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(lane, ReadsDir, "951", " malformed .json")
	if err := os.WriteFile(bad, []byte(`{"who":`), 0o644); err != nil {
		t.Fatal(err)
	}
	tipGit(t, lane, "add", ReadsDir)
	tipGit(t, lane, "commit", "-qm", "records at immutable tip")
	return lane, tipGit(t, lane, "rev-parse", "HEAD"), recordPath
}

// TestFoldFetchedTipUsesPinnedTreeWithoutCheckoutLock covers work item 0c's narrow
// promise. The checkout and state are deliberately stale and dirty; only the named commit
// may decide the fold, even while a coordinator owns the checkout lock.
func TestFoldFetchedTipUsesPinnedTreeWithoutCheckoutLock(t *testing.T) {
	lane, tip, recordPath := fetchedTipLab(t)
	statePath := filepath.Join(lane, StateName)
	if err := os.WriteFile(recordPath, tipRead("worktree", strings.Repeat("b", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"not":"the fetched fold"}\n`), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	release, err := Lock(filepath.Join(lane, CheckoutLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, nil), time.Millisecond)
	if _, err := records.FoldTip(tip); err == nil {
		t.Fatal("legacy FoldTip must retain its checkout lock")
	}
	folded, err := records.FoldFetchedTip(tip)
	if err != nil {
		t.Fatalf("lock-free immutable fold: %v", err)
	}
	reads := folded.Reads["951"]
	if len(reads) != 1 || reads[0].Who != "tip" {
		t.Fatalf("fold must use the pinned tree rather than dirty checkout/state: %+v", reads)
	}
	if want := "reads/951/ reader record .json"; reads[0].File != want {
		t.Fatalf("NUL tree paths must retain whitespace exactly: got %q want %q", reads[0].File, want)
	}
	if len(folded.Problems) != 1 || folded.Problems[0].File != "reads/951/ malformed .json" {
		t.Fatalf("malformed record must remain a named fold problem: %+v", folded.Problems)
	}
	afterRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterRecord, beforeRecord) || !bytes.Equal(afterState, beforeState) {
		t.Fatal("fetched-tip fold wrote state or the work tree")
	}
}

func TestFoldTipRetainsLegacyTreeishInput(t *testing.T) {
	lane, _, _ := fetchedTipLab(t)
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, nil), time.Second)
	folded, err := records.FoldTip("HEAD")
	if err != nil {
		t.Fatalf("legacy tree-ish HEAD must remain accepted: %v", err)
	}
	if reads := folded.Reads["951"]; len(reads) != 1 || reads[0].Who != "tip" {
		t.Fatalf("legacy FoldTip lost its tree-ish fold: %+v", reads)
	}
}

type countingExec struct {
	calls [][]string
}

func (r *countingExec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return (Exec{}).Run(ctx, dir, name, args...)
}

func (r *countingExec) count(args ...string) int {
	n := 0
	for _, call := range r.calls {
		if len(call) != len(args) {
			continue
		}
		matched := true
		for i := range args {
			if call[i] != args[i] {
				matched = false
				break
			}
		}
		if matched {
			n++
		}
	}
	return n
}

func TestFoldFetchedTipRereadsOnlyProblemPaths(t *testing.T) {
	lane, tip, _ := fetchedTipLab(t)
	runner := &countingExec{}
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
	folded, err := records.FoldFetchedTip(tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(folded.Problems) != 1 {
		t.Fatalf("malformed path must survive its retry: %+v", folded.Problems)
	}
	if got := runner.count("ls-tree", "-r", "-z", "--name-only", tip); got != 1 {
		t.Fatalf("pinned tree must be listed once, got %d", got)
	}
	if got := runner.count("show", tip+":reads/951/ reader record .json"); got != 1 {
		t.Fatalf("valid record must be read once, got %d", got)
	}
	if got := runner.count("show", tip+":reads/951/ malformed .json"); got != 2 {
		t.Fatalf("only malformed record must receive one retry, got %d", got)
	}
}

type tipRunner struct {
	calls [][]string
}

func (r *tipRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) >= 2 && args[0] == "cat-file" && args[1] == "-t" {
		return "blob\n", nil
	}
	return "", fmt.Errorf("unexpected git call: %q", args)
}

func TestFoldFetchedTipRefusesMutableAndNonCommitTipsBeforeTreeWalk(t *testing.T) {
	runner := &tipRunner{}
	lane := t.TempDir()
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
	if _, err := records.FoldFetchedTip("FETCH_HEAD"); err == nil || !strings.Contains(err.Error(), "full 40-character sha") {
		t.Fatalf("mutable name must refuse before Git, got %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("mutable tip reached Git: %v", runner.calls)
	}
	blob := strings.Repeat("c", 40)
	if _, err := records.FoldFetchedTip(blob); err == nil || !strings.Contains(err.Error(), "not a commit") {
		t.Fatalf("non-commit object must refuse, got %v", err)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != "cat-file -t "+blob {
		t.Fatalf("object scope check must stop before tree walk, got %v", runner.calls)
	}
}
