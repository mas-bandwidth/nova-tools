package card_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// noCardLedger is a ledger with no card on it: the wrapper refuses at its
// first read, before any claim, so a test can watch what happens before the
// ledger without Redis, a harness or a clock.
type noCardLedger struct{ reads int }

func (l *noCardLedger) Card(context.Context) (card.WrapperCard, error) {
	l.reads++
	return card.WrapperCard{}, nil
}
func (l *noCardLedger) Claim(context.Context, string) (int, error) { return 0, errors.New("unreached") }
func (l *noCardLedger) Launched(context.Context, string, string, time.Duration) (int, error) {
	return 0, errors.New("unreached")
}
func (l *noCardLedger) Beat(context.Context) (int, error) { return 0, errors.New("unreached") }
func (l *noCardLedger) End(context.Context, card.WrapperEnd) (int, error) {
	return 0, errors.New("unreached")
}

func niceWrapperConfig(t *testing.T, yield func() error) card.WrapperConfig {
	t.Helper()
	root := t.TempDir()
	return card.WrapperConfig{
		Sprint: "control-nice", Label: "card-nice", Attempt: 1, Bench: "nice-bench",
		Yield:       yield,
		Harness:     filepath.Join(root, "harness"),
		JobsRoot:    filepath.Join(root, "jobs"),
		ResultsRoot: filepath.Join(root, "results"),
		Clock:       time.Minute,
	}
}

// TestWrapperYieldsToCIBeforeItsFirstWrite (nova-tools#4293): the wrapper
// steps down to nice 15 before it reads the ledger, so before the claim,
// launched, the job dir and the harness exec; a config refusal (nothing to
// run) yields nothing.
func TestWrapperYieldsToCIBeforeItsFirstWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ledger := &noCardLedger{}
	yields, readsAtYield := 0, -1
	cfg := niceWrapperConfig(t, func() error { yields++; readsAtYield = ledger.reads; return nil })
	rep := card.RunWrapper(ctx, cfg, ledger)
	if rep.Code != card.WrapperExitNotDealt || rep.Why != "no such card" {
		t.Fatalf("report = %+v, want NotDealt no such card", rep)
	}
	if yields != 1 || readsAtYield != 0 || ledger.reads != 1 {
		t.Fatalf("yields = %d (ledger reads at the yield %d, after %d), want one yield before the one read", yields, readsAtYield, ledger.reads)
	}

	yields = 0
	bad := cfg
	bad.Harness = "relative/harness"
	if rep := card.RunWrapper(ctx, bad, ledger); rep.Code != card.WrapperExitUsage {
		t.Fatalf("usage refusal = %+v", rep)
	}
	if yields != 0 {
		t.Fatalf("a usage refusal yielded %d times, want 0: nothing runs", yields)
	}
}

// TestWrapperRefusesWhenItCannotYield: a process that cannot step down
// refuses, printed, with nothing claimed; it never runs a copy at CI's
// priority.
func TestWrapperRefusesWhenItCannotYield(t *testing.T) {
	t.Parallel()
	ledger := &noCardLedger{}
	cfg := niceWrapperConfig(t, func() error { return errors.New("setpriority: operation not permitted") })
	rep := card.RunWrapper(context.Background(), cfg, ledger)
	if rep.Code != card.WrapperExitCouldNot || !strings.HasPrefix(rep.Why, "yield to CI: ") || !strings.Contains(rep.Why, "not permitted") {
		t.Fatalf("report = %+v, want CouldNot yield to CI: ...", rep)
	}
	if ledger.reads != 0 {
		t.Fatalf("ledger read %d times after a yield refusal, want 0", ledger.reads)
	}
	if !strings.Contains(rep.Line(), "yield to CI") {
		t.Fatalf("the printed line %q does not say why", rep.Line())
	}
}

// TestCardRunYieldsToCIBeforeTheRunner: `card run` by hand has no wrapper
// above it, so Run yields for itself, before the out dir, the log and the
// runner; a process that cannot yield is refused with the reason.
func TestCardRunYieldsToCIBeforeTheRunner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	yields := 0
	// A valid config with no store: Run refuses "no store" right after the
	// yield, before the out dir, the log or the runner.
	cfg := card.RunConfig{Sprint: "control-nice", Label: "card-nice", Attempt: 1, Bench: "nice-bench",
		Yield:      func() error { yields++; return nil },
		HarnessBin: "/opt/harness", Deadline: "1", Tokens: "1", Home: t.TempDir(),
		OutDir: filepath.Join(t.TempDir(), "out"), JobDir: t.TempDir()}
	rep := card.Run(ctx, nil, cfg)
	if rep.Code != card.RunExitRefused || rep.Why != "no store" {
		t.Fatalf("report = %+v, want refused no store", rep)
	}
	if yields != 1 {
		t.Fatalf("yields = %d, want 1 before the runner", yields)
	}
	cfg.Yield = func() error { return errors.New("no setpriority on plan9") }
	if rep := card.Run(ctx, nil, cfg); rep.Code != card.RunExitRefused || rep.Why != "yield to CI: no setpriority on plan9" {
		t.Fatalf("report = %+v, want the yield refusal", rep)
	}
}
