package merge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reportTestGitTimeout = 5 * time.Second

func reportTipLab(t *testing.T) (lane, tip, fetchHeadPath, trackingRef string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	lane = filepath.Join(root, "lane")
	tipGit(t, root, "init", "-q", "--bare", remote)
	quietRepo(t, remote)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	tipGit(t, seed, "init", "-q")
	tipGit(t, seed, "config", "user.name", "Nova Test")
	tipGit(t, seed, "config", "user.email", "nova-test@example.invalid")
	head := strings.Repeat("a", 40)
	old := filepath.Join(seed, ReadsDir, "951", "old.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, tipRead("old", head), 0o644); err != nil {
		t.Fatal(err)
	}
	tipGit(t, seed, "add", ReadsDir)
	tipGit(t, seed, "commit", "-qm", "old records")
	tipGit(t, seed, "remote", "add", "origin", remote)
	tipGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/nova-merge/lane")
	tipGit(t, root, "clone", "-q", "--branch", "nova-merge/lane", remote, lane)
	tipGit(t, lane, "config", "user.name", "Nova Test")
	tipGit(t, lane, "config", "user.email", "nova-test@example.invalid")

	newRecord := filepath.Join(seed, ReadsDir, "951", "new.json")
	if err := os.WriteFile(newRecord, tipRead("new", head), 0o644); err != nil {
		t.Fatal(err)
	}
	tipGit(t, seed, "add", ReadsDir)
	tipGit(t, seed, "commit", "-qm", "new records")
	tipGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/nova-merge/lane")
	tip = tipGit(t, seed, "rev-parse", "HEAD")
	trackingRef = "refs/remotes/origin/nova-merge/lane"
	fetchHeadPath = filepath.Join(lane, tipGit(t, lane, "rev-parse", "--git-path", "FETCH_HEAD"))
	if err := os.WriteFile(fetchHeadPath, []byte("shared fetch head must stay put\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return lane, tip, fetchHeadPath, trackingRef
}

func reportNonce(t *testing.T, nonce string) {
	t.Helper()
	old := newReportFetchNonce
	newReportFetchNonce = func() (string, error) { return nonce, nil }
	t.Cleanup(func() { newReportFetchNonce = old })
}

func reportRefNames(t *testing.T, lane string) []string {
	t.Helper()
	out := tipGit(t, lane, "for-each-ref", "--format=%(refname)", reportFetchedRefPrefix)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestWithFetchedReportTipPinsTheCallbackWithoutCheckoutOrFetchHead(t *testing.T) {
	lane, wantTip, fetchHeadPath, trackingRef := reportTipLab(t)
	statePath := filepath.Join(lane, StateName)
	if err := os.WriteFile(statePath, []byte("state remains report-external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeFetchHead, err := os.ReadFile(fetchHeadPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeTracking := tipGit(t, lane, "rev-parse", trackingRef)

	release, err := Lock(filepath.Join(lane, CheckoutLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, nil), time.Millisecond)
	var gotTip string
	err = records.WithFetchedReportTip(func(tip string) error {
		gotTip = tip
		if refs := reportRefNames(t, lane); len(refs) != 1 {
			t.Fatalf("callback must hold exactly one private reachability ref, got %v", refs)
		}
		// The fetched commit is newer than every ordinary lane ref. If the private pin did
		// not hold it, prune=now could remove it before the immutable fold below.
		tipGit(t, lane, "gc", "--prune=now")
		folded, err := records.FoldFetchedTip(tip)
		if err != nil {
			return err
		}
		if reads := folded.Reads["951"]; len(reads) != 2 || reads[0].Who != "new" && reads[1].Who != "new" {
			return fmt.Errorf("pinned fold lost the newly fetched record: %+v", reads)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("lock-free report acquisition: %v", err)
	}
	if gotTip != wantTip {
		t.Fatalf("callback saw %s, want fetched %s", gotTip, wantTip)
	}
	if refs := reportRefNames(t, lane); len(refs) != 0 {
		t.Fatalf("private ref leaked after callback: %v", refs)
	}
	afterFetchHead, err := os.ReadFile(fetchHeadPath)
	if err != nil {
		t.Fatal(err)
	}
	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFetchHead, beforeFetchHead) || !bytes.Equal(afterState, beforeState) {
		t.Fatal("report acquisition changed shared FETCH_HEAD or lane state")
	}
	if got := tipGit(t, lane, "rev-parse", trackingRef); got != beforeTracking {
		t.Fatalf("explicit private fetch changed remote-tracking ref: got %s want %s", got, beforeTracking)
	}
}

func TestWithFetchedReportTipKeepsConcurrentPinsThroughGC(t *testing.T) {
	lane, wantTip, _, _ := reportTipLab(t)
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, nil), time.Second)
	start := make(chan struct{})
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- records.WithFetchedReportTip(func(tip string) error {
				if tip != wantTip {
					return fmt.Errorf("callback tip %s, want %s", tip, wantTip)
				}
				arrived <- struct{}{}
				<-release
				_, err := records.FoldFetchedTip(tip)
				return err
			})
		}()
	}
	close(start)
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(30 * time.Second):
			t.Fatal("concurrent report acquisition did not reach both pinned callbacks")
		}
	}
	if refs := reportRefNames(t, lane); len(refs) != 2 || refs[0] == refs[1] {
		t.Fatalf("concurrent callbacks need two distinct pins, got %v", refs)
	}
	tipGit(t, lane, "gc", "--prune=now")
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent report callback lost its pin: %v", err)
		}
	}
	if refs := reportRefNames(t, lane); len(refs) != 0 {
		t.Fatalf("concurrent pins leaked after callbacks: %v", refs)
	}
}

type reportRunner struct {
	mode        string
	replacement string
	ref         string
	calls       [][]string
}

func (r *reportRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if isReportFetch(args) && (r.mode == "fetch-fail" || r.mode == "fetch-ambiguous") {
		_, r.ref, _ = reportFetchDestination(args)
		if r.mode == "fetch-ambiguous" {
			cmd := exec.CommandContext(ctx, name, "update-ref", r.ref, r.replacement)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("test replacement: %w: %s", err, out)
			}
		}
		return "forced fetch failure", errors.New("forced fetch failure")
	}
	if isReportDelete(args) && r.mode == "replace-before-delete" {
		r.ref, _, _ = reportDeleteTarget(args)
		cmd := exec.CommandContext(ctx, name, "update-ref", r.ref, r.replacement)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("test replacement: %w: %s", err, out)
		}
	}
	return (Exec{}).Run(ctx, dir, name, args...)
}

func isReportFetch(args []string) bool {
	for _, arg := range args {
		if arg == "fetch" {
			return true
		}
	}
	return false
}

func isReportDelete(args []string) bool {
	for i := range args {
		if args[i] == "update-ref" {
			for _, arg := range args[i+1:] {
				if arg == "-d" {
					return true
				}
			}
		}
	}
	return false
}

func reportFetchDestination(args []string) (source, destination string, ok bool) {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "+refs/heads/") {
			continue
		}
		source, destination, ok = strings.Cut(arg, ":")
		return source, destination, ok
	}
	return "", "", false
}

func reportDeleteTarget(args []string) (ref, expected string, ok bool) {
	for i, arg := range args {
		if arg != "-d" || i+2 >= len(args) {
			continue
		}
		return args[i+1], args[i+2], true
	}
	return "", "", false
}

func TestWithFetchedReportTipCleansKnownFailureAndPreservesAmbiguousRef(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		wantLeaked bool
	}{
		{name: "marker is still ours", mode: "fetch-fail"},
		{name: "failed fetch changed target", mode: "fetch-ambiguous", wantLeaked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lane, _, _, _ := reportTipLab(t)
			replacement := tipGit(t, lane, "commit-tree", "HEAD^{tree}", "-m", "replacement")
			runner := &reportRunner{mode: tc.mode, replacement: replacement}
			records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
			err := records.WithFetchedReportTip(func(string) error {
				t.Fatal("callback ran after failed fetch")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "forced fetch failure") {
				t.Fatalf("fetch failure was not returned: %v", err)
			}
			refs := reportRefNames(t, lane)
			if tc.wantLeaked {
				if len(refs) != 1 || tipGit(t, lane, "rev-parse", refs[0]) != replacement {
					t.Fatalf("ambiguous target must be preserved for reconciliation, got %v", refs)
				}
				if !strings.Contains(err.Error(), "could not remove private report-fetch ref") {
					t.Fatalf("ambiguous ownership must be surfaced, got %v", err)
				}
				return
			}
			if len(refs) != 0 {
				t.Fatalf("known marker should be cleaned after failure, got %v", refs)
			}
		})
	}
}

func TestWithFetchedReportTipCleansAfterCallbackFailure(t *testing.T) {
	lane, _, _, _ := reportTipLab(t)
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, nil), time.Second)
	want := errors.New("callback refused")
	err := records.WithFetchedReportTip(func(string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("callback refusal was lost: %v", err)
	}
	if refs := reportRefNames(t, lane); len(refs) != 0 {
		t.Fatalf("callback failure leaked private ref: %v", refs)
	}
}

func TestWithFetchedReportTipCleanupCASPreservesReplacement(t *testing.T) {
	lane, wantTip, _, _ := reportTipLab(t)
	replacement := tipGit(t, lane, "commit-tree", "HEAD^{tree}", "-m", "replacement")
	runner := &reportRunner{mode: "replace-before-delete", replacement: replacement}
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
	called := false
	err := records.WithFetchedReportTip(func(tip string) error {
		called = true
		if tip != wantTip {
			t.Fatalf("callback tip %s, want %s", tip, wantTip)
		}
		return nil
	})
	if !called || err == nil || !strings.Contains(err.Error(), "could not remove private report-fetch ref") {
		t.Fatalf("replacement before cleanup must fail closed, called=%t err=%v calls=%v ref=%q", called, err, runner.calls, runner.ref)
	}
	refs := reportRefNames(t, lane)
	if len(refs) != 1 || tipGit(t, lane, "rev-parse", refs[0]) != replacement {
		t.Fatalf("expected-old cleanup deleted a replacement: %v", refs)
	}
}

func TestWithFetchedReportTipRefusesInvalidBranchAndReservationCollision(t *testing.T) {
	t.Run("invalid branch", func(t *testing.T) {
		runner := &reportRunner{}
		records := NewRecords(t.TempDir(), "refs/heads/nova-merge/lane", "origin", NewGit(t.TempDir(), time.Second, runner), time.Second)
		err := records.WithFetchedReportTip(func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "short branch name") {
			t.Fatalf("qualified branch must refuse before Git, got %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("invalid branch reached Git: %v", runner.calls)
		}
	})
	t.Run("reservation collision", func(t *testing.T) {
		lane, _, _, _ := reportTipLab(t)
		oldTip := tipGit(t, lane, "rev-parse", "HEAD")
		nonce := strings.Repeat("a", 32)
		reportNonce(t, nonce)
		ref := reportFetchedRefPrefix + nonce
		tipGit(t, lane, "update-ref", ref, oldTip)
		runner := &reportRunner{}
		records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
		err := records.WithFetchedReportTip(func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "could not reserve private") {
			t.Fatalf("existing private destination must refuse, got %v", err)
		}
		if got := tipGit(t, lane, "rev-parse", ref); got != oldTip {
			t.Fatalf("collision ref changed: got %s want %s", got, oldTip)
		}
		for _, call := range runner.calls {
			if isReportFetch(call) {
				t.Fatalf("collision reached fetch: %v", runner.calls)
			}
		}
	})
}

func TestWithFetchedReportTipUsesOnlyThePrivateFetchRefspec(t *testing.T) {
	lane, _, _, _ := reportTipLab(t)
	runner := &reportRunner{}
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
	if err := records.WithFetchedReportTip(func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var fetch []string
	for _, call := range runner.calls {
		if isReportFetch(call) {
			fetch = call
			break
		}
	}
	if len(fetch) == 0 {
		t.Fatal("report acquisition did not fetch")
	}
	for _, want := range []string{"--no-write-fetch-head", "--no-tags", "--no-recurse-submodules", "--refmap=", "+refs/heads/nova-merge/lane:"} {
		found := false
		for _, arg := range fetch {
			if arg == want || strings.HasPrefix(arg, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("private fetch command misses %q: %v", want, fetch)
		}
	}
	separator := -1
	for i, arg := range fetch {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+2 >= len(fetch) || fetch[separator+1] != "origin" || !strings.HasPrefix(fetch[separator+2], "+refs/heads/nova-merge/lane:") {
		t.Fatalf("remote and refspec must follow -- exactly, got %v", fetch)
	}
	for _, arg := range fetch {
		if strings.Contains(arg, "FETCH_HEAD") {
			t.Fatalf("private fetch reached shared FETCH_HEAD: %v", fetch)
		}
	}
}

// timedOutFetchRunner is a fetch that never returns on its own. It blocks until the test
// injects the timeout by closing release, then answers context.DeadlineExceeded, exactly
// as a hung fetch would once its deadline passed. The timeout under test is injected
// through the stub, never a wall-clock sleep (issue 694): the old test waited 150 ms of
// real time for a machine under swarm load to fire a real deadline, and the assertion was
// against elapsed seconds it could not control.
type timedOutFetchRunner struct {
	fetchBlocked chan struct{}
	release      chan struct{}
	cleanupCalls int
	cleanupFresh bool
}

func (r *timedOutFetchRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if isReportFetch(args) {
		close(r.fetchBlocked)
		<-r.release
		return "fetch never returned; the timeout was injected", context.DeadlineExceeded
	}
	if isReportDelete(args) {
		r.cleanupCalls++
		_, hasDeadline := ctx.Deadline()
		r.cleanupFresh = ctx.Err() == nil && hasDeadline
	}
	return (Exec{}).Run(ctx, dir, name, args...)
}

func TestWithFetchedReportTipTimesOutFetchAndCleansOwnedMarker(t *testing.T) {
	lane, _, _, _ := reportTipLab(t)
	runner := &timedOutFetchRunner{fetchBlocked: make(chan struct{}), release: make(chan struct{})}
	records := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, reportTestGitTimeout, runner), time.Second)
	type outcome struct {
		err      error
		cbCalled bool
	}
	done := make(chan outcome, 1)
	go func() {
		cbCalled := false
		err := records.WithFetchedReportTip(func(string) error {
			cbCalled = true
			return nil
		})
		done <- outcome{err: err, cbCalled: cbCalled}
	}()
	<-runner.fetchBlocked
	close(runner.release)
	got := <-done
	if got.cbCalled || got.err == nil || !strings.Contains(got.err.Error(), "could not fetch report tip") {
		t.Fatalf("timed-out fetch must never reach the callback and must print its refusal line, called=%t err=%v", got.cbCalled, got.err)
	}
	if runner.cleanupCalls != 1 || !runner.cleanupFresh {
		t.Fatalf("owned marker cleanup needs one fresh bounded call, calls=%d fresh=%t", runner.cleanupCalls, runner.cleanupFresh)
	}
	if refs := reportRefNames(t, lane); len(refs) != 0 {
		t.Fatalf("timed-out fetch leaked owned marker: %v", refs)
	}
}
