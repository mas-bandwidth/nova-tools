package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mutate-seed-timeout-is-not-a-pass, at the level where the decision is made and
// without the wall clock.
//
// The cold read of 2026-09-19 (#1708 finding 1) reproduced this at the verb: a
// `--timeout` that killed `go test` mid-flight came back as `red=1 ... PASS`, so a
// deadline was a control that held. The verb-level proof of it costs a real second of
// sleeping, and the two-minute rule binds harder than the extra coverage is worth
// (`nova-review mutate --seed` runs inside the accept gate's own loop), so the run that
// waits is behind `-short` in cmd/nova-review and this is the one that always runs.
//
// A context whose deadline is already in the past is the same fact as a context whose
// deadline passes at second two: `exec` refuses to start, `ctx.Err()` is non-nil, and
// the answer is a could-not-run. Neither `runPackage` nor `listPackage` is allowed to
// call that a kill.
func TestSeedTimeoutIsACouldNotRunAndNeverAKill(t *testing.T) {
	t.Parallel()
	// The deadline is already in the PAST, so nothing waits for it: the second is how
	// far back it is, not how long this test takes.
	expired := func() (context.Context, context.CancelFunc) {
		return context.WithDeadline(context.Background(), time.Now().Add(-time.Second)) // wall-ok: a deadline in the past waits for nothing
	}

	ctx, cancel := expired()
	defer cancel()
	red, green, err := runPackage(ctx, t.TempDir(), "sign")
	if err == nil {
		t.Fatalf("a killed run was judged: red=%d green=%d", red, green)
	}
	if red != 0 || green != 0 {
		t.Fatalf("red=%d green=%d, want no verdict at all from a run that was killed", red, green)
	}
	// The words matter, not just the error: `go test` under a dead context fails with
	// "context deadline exceeded" of its own, and a refusal that merely forwarded
	// that would read as the run's failure rather than as the caller's clock.
	if !strings.Contains(err.Error(), wantDeadline) {
		t.Fatalf("err = %v, want %q", err, wantDeadline)
	}

	ctx2, cancel2 := expired()
	defer cancel2()
	if err := listPackage(ctx2, t.TempDir(), "sign"); err == nil {
		t.Fatal("a killed `go list` reported a package that exists")
	} else if !strings.Contains(err.Error(), wantDeadline) {
		// It must not read as the caller's typo either: the package was never looked
		// at, so "not a package at this head" is a thing nobody measured.
		t.Fatalf("err = %v, want %q", err, wantDeadline)
	}
}

const wantDeadline = "--timeout deadline passed"

// The refusal for a package that does not exist names the package and nothing else.
// `go list`'s own first line is `stat <the throwaway worktree>/nosuch: directory not
// found`, and that line is quoted into PR bodies and harvest logs (#1708, cold read 2,
// finding 1). The module is real, because without one `go list` fails for a different
// reason and says nothing about any path.
func TestSeedRefusalForAMissingPackageCarriesNoTempPath(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := listPackage(context.Background(), wt, "nosuch")
	if err == nil {
		t.Fatal("a package that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "nosuch") || !strings.Contains(err.Error(), "not a package at this head") {
		t.Fatalf("err = %v, want the package named", err)
	}
	if strings.Contains(err.Error(), wt) {
		t.Fatalf("the refusal carries the throwaway worktree's path: %v", err)
	}
}

// The seed form runs a program the worker chose in two places -- the git clean
// filter its `.git` selects, and the Test the card wrote -- and neither may read
// the gate's environment or run where the gate can. The job clone is the
// worker's, so a linked worktree of it shares the worker's local config and its
// `info/attributes`: `git apply`, `git add -A` and the tests are all programs of
// the card's choosing, run as the gate user (#1897).
//
// This fixture arms both channels. It names a `filter.leak.clean` in the job
// clone's local config and selects it with `.git/info/attributes`, so any `git
// add` of the seeded tree runs a script the card wrote. The head's own Test
// copies DEEPSEEK_API_KEY out, so the gate's key is one `os.Getenv` from a file
// the harvest already copies. The seed runs in a private clone of the objects
// (no config, info/attributes, hooks or replace refs) and each child gets an
// environment with every secret-named variable dropped, so neither program runs
// and neither sees the key.
func TestMutateSeedRunsNoWorkerFilterAndHandsNoSecretToTheCardsTest(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "git", "checkout", "-q", "-b", "fixed")
	write(t, dir, "sign/sign.go", `package sign

func Sign(n int) int {
	if n > 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	return -1
}
`)
	escaped := filepath.Join(t.TempDir(), "escaped")
	write(t, dir, "sign/sign_test.go", `package sign

import (
	"os"
	"testing"
)

func TestSignZero(t *testing.T) {
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		_ = os.WriteFile(os.Getenv("NOVA_SEED_MARKER"), []byte("escaped"), 0o644)
	}
	if Sign(0) != 0 {
		t.Fatal("zero")
	}
}
`)
	commit(t, dir, "the zero case, with a test that reads the gate environment")

	// The seed is generated from the tree, one changed line, so no hunk context
	// line's leading space and tab lands in this file where `git diff --check`
	// would read it as a whitespace error in the test itself. It is written before
	// the worker's filter is armed: `git diff` runs the clean filter too.
	write(t, dir, "sign/sign.go", mutantSign)
	patch := run(t, dir, "git", "diff", "--no-ext-diff", "--no-renames")
	run(t, dir, "git", "checkout", "--", "sign/sign.go")
	if !strings.Contains(patch, "return 1") {
		t.Fatalf("the generated seed is not the one-line mutant:\n%s", patch)
	}
	seed := filepath.Join(t.TempDir(), "seed.patch")
	if err := os.WriteFile(seed, []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}

	// The worker's side: attributes select a clean filter and the job clone's own
	// config names the script. Neither is a committed file.
	filterRan := filepath.Join(t.TempDir(), "filter-ran")
	write(t, dir, ".git/info/attributes", "* filter=leak\n")
	run(t, dir, "git", "config", "filter.leak.clean", "sh -c 'cat; echo ran > "+filterRan+"'")

	t.Setenv("DEEPSEEK_API_KEY", "sk-not-a-real-key-123456")
	t.Setenv("NOVA_SEED_MARKER", escaped)

	res, err := MutateSeed(context.Background(), SeedOptions{
		Repo: dir, Head: "HEAD", Seed: seed, Tests: []string{"sign"}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass || res.Edits != 1 || res.Red != 1 {
		t.Fatalf("the seeded run did not kill the mutant: edits=%d red=%d green=%d pass=%v", res.Edits, res.Red, res.Green, res.Pass)
	}
	if _, err := os.Stat(filterRan); err == nil {
		t.Fatalf("the worker's git clean filter ran: a `git add` executed a script the card named")
	}
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("the card's test read DEEPSEEK_API_KEY from the gate process: the seed form handed it the gate's environment")
	}
}

// mutantSign is the head's sign.go with its zero case broken: the one edit the
// generated seed carries. It is written, diffed and checked out, so the patch
// this test feeds the verb is the same shape a card's seed is.
const mutantSign = `package sign

func Sign(n int) int {
	if n > 0 {
		return 1
	}
	if n == 0 {
		return 1
	}
	return -1
}
`
