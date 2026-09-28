package swarm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// BOUNDED EXECUTION TIMEOUTS AND MULTI-TIER HIERARCHY (Sprint Row 11, #2062).
//
// Every execution in the swarm must be strictly bounded in wall-clock duration.
// An unbounded job or a job with a fractional-millisecond deadline is a defect in
// configuration: without lower bounds, spurious deadline expirations starve legitimate
// work before the runtime can initialize; without upper bounds, runaway loops hold
// runner slots and leak compute indefinitely.
//
// The standing bounds for card execution are:
//   - Minimum execution timeout: 5 seconds (MinExecutionTimeout).
//   - Maximum execution timeout: 2 hours (MaxExecutionTimeout).
//   - Default grace period for termination: 3 seconds (TerminateGrace / DefaultGracePeriod).
//
// The execution hierarchy encompasses three distinct tiers:
//   1. Card Timeout: the top-level bounded budget for the entire card (5s..2h).
//   2. Step Timeout: the execution budget for a single step/turn within a card (5s..CardTimeout).
//   3. Git Operation Timeout: the sub-operation budget for VCS/git operations (5s..min(StepTimeout, 1m)).

const (
	// MinExecutionTimeout is the absolute lower bound on execution timeout (5s).
	MinExecutionTimeout = 5 * time.Second

	// MaxExecutionTimeout is the absolute upper bound on execution timeout (2h).
	MaxExecutionTimeout = 2 * time.Hour

	// DefaultCardTimeout is the canonical default timeout for an unconfigured card (30m).
	DefaultCardTimeout = 30 * time.Minute

	// DefaultStepTimeout is the canonical default timeout for an individual step (5m).
	DefaultStepTimeout = 5 * time.Minute

	// DefaultGitTimeout is the default budget for a git/VCS operation (1m).
	DefaultGitTimeout = 1 * time.Minute

	// MaxGitTimeout is the maximum budget allowed for a single git operation (1m).
	MaxGitTimeout = 1 * time.Minute

	// MinGitTimeout is the minimum budget floor for a git operation when time allows (5s).
	MinGitTimeout = 5 * time.Second

	// DefaultGracePeriod is the grace period granted to a process group between SIGTERM
	// and SIGKILL, matching TerminateGrace (3s).
	DefaultGracePeriod = TerminateGrace
)

// ClampTimeout clamps any raw duration to the bounded interval [MinExecutionTimeout, MaxExecutionTimeout]
// (5s to 2h). Durations below 5s are clamped to 5s; durations above 2h are clamped to 2h.
func ClampTimeout(d time.Duration) time.Duration {
	if d < MinExecutionTimeout {
		return MinExecutionTimeout
	}
	if d > MaxExecutionTimeout {
		return MaxExecutionTimeout
	}
	return d
}

// ClampTimeoutWithDefault clamps d if positive, or returns defaultTimeout clamped to [5s, 2h].
func ClampTimeoutWithDefault(d, defaultTimeout time.Duration) time.Duration {
	if d <= 0 {
		return ClampTimeout(defaultTimeout)
	}
	return ClampTimeout(d)
}

// TimeoutHierarchy defines the multi-tier execution timeouts for card execution,
// enforcing invariants across all three layers:
//
//	CardTimeout >= StepTimeout >= GitTimeout
type TimeoutHierarchy struct {
	// CardTimeout is the overall execution budget for the entire card/task (5s..2h).
	CardTimeout time.Duration

	// StepTimeout is the per-step or per-turn execution budget (5s..CardTimeout).
	StepTimeout time.Duration

	// GitTimeout is the bounded budget for VCS/git sub-operations (5s..min(StepTimeout, 1m)).
	GitTimeout time.Duration
}

// NewTimeoutHierarchy creates a validated, bounded timeout hierarchy.
//   - card is clamped to [MinExecutionTimeout, MaxExecutionTimeout] (5s..2h).
//   - step is bounded to [MinExecutionTimeout, CardTimeout]. If step <= 0, it defaults to min(DefaultStepTimeout, CardTimeout)
//     or CardTimeout if CardTimeout < DefaultStepTimeout.
//   - git is bounded to [MinGitTimeout, min(StepTimeout, MaxGitTimeout)]. If git <= 0, it defaults to
//     min(StepTimeout/3, MaxGitTimeout), floored at MinGitTimeout (or StepTimeout if StepTimeout < MinGitTimeout).
func NewTimeoutHierarchy(card, step, git time.Duration) TimeoutHierarchy {
	c := ClampTimeout(card)

	// Step budget derivation and clamping
	s := step
	if s <= 0 {
		if c < DefaultStepTimeout {
			s = c
		} else {
			s = DefaultStepTimeout
		}
	}
	if s > c {
		s = c
	}
	if s < MinExecutionTimeout {
		s = MinExecutionTimeout
		if s > c {
			s = c
		}
	}

	// Git budget derivation and clamping
	g := git
	if g <= 0 {
		g = s / 3
		if g > MaxGitTimeout {
			g = MaxGitTimeout
		}
		if g < MinGitTimeout {
			g = MinGitTimeout
		}
	}
	if g > MaxGitTimeout {
		g = MaxGitTimeout
	}
	if g > s {
		g = s
	}

	return TimeoutHierarchy{
		CardTimeout: c,
		StepTimeout: s,
		GitTimeout:  g,
	}
}

// DefaultTimeoutHierarchy returns the canonical hierarchy using default budgets.
func DefaultTimeoutHierarchy() TimeoutHierarchy {
	return NewTimeoutHierarchy(DefaultCardTimeout, DefaultStepTimeout, DefaultGitTimeout)
}

// Validate checks that the hierarchy satisfies the ordering invariants.
func (h TimeoutHierarchy) Validate() error {
	if h.CardTimeout < MinExecutionTimeout || h.CardTimeout > MaxExecutionTimeout {
		return fmt.Errorf("card timeout %v outside allowed bounds [%v, %v]", h.CardTimeout, MinExecutionTimeout, MaxExecutionTimeout)
	}
	if h.StepTimeout < MinExecutionTimeout && h.StepTimeout != h.CardTimeout {
		return fmt.Errorf("step timeout %v below minimum %v", h.StepTimeout, MinExecutionTimeout)
	}
	if h.StepTimeout > h.CardTimeout {
		return fmt.Errorf("step timeout %v exceeds card timeout %v", h.StepTimeout, h.CardTimeout)
	}
	if h.GitTimeout > h.StepTimeout {
		return fmt.Errorf("git timeout %v exceeds step timeout %v", h.GitTimeout, h.StepTimeout)
	}
	if h.GitTimeout > MaxGitTimeout {
		return fmt.Errorf("git timeout %v exceeds maximum git timeout %v", h.GitTimeout, MaxGitTimeout)
	}
	return nil
}

// StepBudget computes the remaining budget for a step given the remaining card duration.
// If remainingCard is zero or negative, no budget remains.
// Otherwise, the budget is bounded by min(h.StepTimeout, remainingCard).
func (h TimeoutHierarchy) StepBudget(remainingCard time.Duration) time.Duration {
	if remainingCard <= 0 {
		return 0
	}
	if h.StepTimeout <= remainingCard {
		return h.StepTimeout
	}
	return remainingCard
}

// GitBudget computes the budget for a git operation given the remaining step duration.
// It follows the canonical 1/3 allocation rule (matching busBounds):
//
//	git := remainingStep / 3, capped at MaxGitTimeout (1m), floored at MinGitTimeout (5s).
//
// It never exceeds remainingStep.
func (h TimeoutHierarchy) GitBudget(remainingStep time.Duration) time.Duration {
	if remainingStep <= 0 {
		return 0
	}
	git := remainingStep / 3
	if git > MaxGitTimeout {
		git = MaxGitTimeout
	}
	if h.GitTimeout > 0 && h.GitTimeout < git {
		git = h.GitTimeout
	}
	if git < MinGitTimeout {
		if remainingStep >= MinGitTimeout {
			git = MinGitTimeout
		} else {
			git = remainingStep
		}
	}
	if git > remainingStep {
		git = remainingStep
	}
	return git
}

// CardContext returns a context bounded by CardTimeout.
func (h TimeoutHierarchy) CardContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, h.CardTimeout)
}

// StepContext returns a context bounded by StepBudget(remainingCard).
func (h TimeoutHierarchy) StepContext(parent context.Context, remainingCard time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	budget := h.StepBudget(remainingCard)
	return context.WithTimeout(parent, budget)
}

// GitContext returns a context bounded by GitBudget(remainingStep).
func (h TimeoutHierarchy) GitContext(parent context.Context, remainingStep time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	budget := h.GitBudget(remainingStep)
	return context.WithTimeout(parent, budget)
}

// TwoPhaseReap performs a canonical two-phase reap of a process group:
// 1. Sends SIGTERM (or graceful tree termination) to allow processes to flush/cleanup.
// 2. Waits up to TerminateGrace (3s) polling for all processes in the group to exit.
// 3. If any process in the group remains alive, sends SIGKILL (or forceful tree kill).
// 4. Waits up to TerminateGrace (3s) polling to confirm all processes are reaped.
// It reports whether any process in the group survived.
func TwoPhaseReap(pgid int, started string) (survived bool) {
	return Reap(pgid, started, TerminateGrace)
}

// TwoPhaseReapWithGrace performs the two-phase reap using a custom grace duration.
// If grace <= 0, it defaults to TerminateGrace (3s).
func TwoPhaseReapWithGrace(pgid int, started string, grace time.Duration) (survived bool) {
	if grace <= 0 {
		grace = TerminateGrace
	}
	return Reap(pgid, started, grace)
}

// TaskkillArgs returns the argument list for Windows taskkill.exe to reap a process tree.
// - When force is false, it returns ["/PID", "<pid>", "/T"] for graceful tree termination.
// - When force is true, it returns ["/PID", "<pid>", "/T", "/F"] for forceful tree termination.
// This function is platform-independent so unit tests on any OS can verify argument construction.
func TaskkillArgs(pid int, force bool) []string {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return args
}

// WindowsKillStrategy specifies the termination mechanism on Windows: taskkill vs direct syscall.
type WindowsKillStrategy int

const (
	// StrategyTaskkill uses taskkill.exe /T (/F) to terminate the full process tree.
	StrategyTaskkill WindowsKillStrategy = iota

	// StrategySyscall uses direct Win32 syscall (TerminateProcess / os.Process.Kill) on the PID.
	StrategySyscall
)

// String returns the human-readable name of the strategy.
func (s WindowsKillStrategy) String() string {
	switch s {
	case StrategyTaskkill:
		return "taskkill"
	case StrategySyscall:
		return "syscall"
	default:
		return fmt.Sprintf("strategy(%d)", s)
	}
}

// KnownStamp reports whether a start timestamp is valid and populated (not empty and not Dash).
func KnownStamp(started string) bool {
	return started != "" && started != Dash
}

// ErrInvalidTimeout indicates a duration violates the clamping bounds.
var ErrInvalidTimeout = errors.New("timeout outside valid bounds [5s, 2h]")
