package friend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DSHCardRunner is a Deliverer that runs each card as a one-shot headless dsh process
// (docs/SPEC-FRIEND.md, one-shot lanes; the dsh row): each card gets its own fresh
// headless run with dsh --profile headless, HOME confined to the friend's working dir,
// and the result reported per lane; a failing lane does not stop the others.
type DSHCardRunner struct {
	Stub
	Friend, Dir string
	Run         Exec
	Program     string    // DSHProgram when empty
	Out         io.Writer // where the run's output goes, when set: the daemon's record
}

// NewDSHCardRunner is the dsh harness's card runner over run, in the friend's
// directory dir, its runs' output going to out when set.
func NewDSHCardRunner(friend, dir string, run Exec, out io.Writer) *DSHCardRunner {
	return &DSHCardRunner{Stub: Stub{Harness: "dsh"}, Friend: friend, Dir: dir, Run: run, Out: out}
}

// Refusal is why no card can run now, with its remedy; dsh one-shot needs no
// config_dir as claude's lane does (the run is dsh --profile headless with
// HOME the friend's own working directory), so there is none.
func (d *DSHCardRunner) Refusal() string { return "" }

// RunCard runs card c to its end: its exit, and an error naming what the
// outbox lacks when the run wrote no REPORT.md or RESULT.md there. A
// missing credential (MISSING_CREDENTIAL) is reported as a provider
// refusal, a judgment told at once, never a silent failure.
func (d *DSHCardRunner) RunCard(ctx context.Context, card Card) (LaneTurn, error) {
	if why := d.Refusal(); why != "" {
		return LaneTurn{}, errors.New(why)
	}
	brief, err := os.ReadFile(card.Brief)
	if err != nil {
		return LaneTurn{}, fmt.Errorf("the card's brief: %w", err)
	}
	program := d.Program
	if program == "" {
		program = DSHProgram
	}
	// dsh --profile headless reads the brief on stdin ("-"), driven in the
	// card's job directory when its lane names one, HOME the friend's own dir.
	out, exit, err := d.Run(ctx, LaneDirOf(ctx, d.Dir), program, []string{"--profile", "headless", "-"}, string(brief))
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	// A missing credential is the provider's refusal, told at once, never a
	// silent failure: the lane ends its card refused and the seat is told.
	if exit == 1 && strings.Contains(out, "MISSING_CREDENTIAL") {
		return LaneTurn{Exit: exit}, ProviderRefused{Reason: "dsh: missing credential for provider route deepseek-official"}
	}
	if exit, err = refused("dsh --profile headless", out, exit, err); err != nil {
		return LaneTurn{Exit: exit}, err
	}
	var missing []string
	for _, f := range []string{"REPORT.md", "RESULT.md"} {
		if !exists(filepath.Join(card.Outbox, f)) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return LaneTurn{Exit: exit, FirstError: HarnessFirstError(out)}, NoReport{Run: "dsh --profile headless", Exit: exit, Outbox: card.Outbox, Lacks: missing}
	}
	return LaneTurn{Exit: exit}, nil
}
