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
