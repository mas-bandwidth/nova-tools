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
// (docs/SPEC-FRIEND.md, one-shot lanes; the dsh row). Each card gets its own fresh
// headless run with dsh --profile headless, HOME confined to the friend's working dir,
// and the result reported per lane.
type DSHCardRunner struct {
	Stub
	Friend, Dir string
	// ConfigDir is the friend row's config_dir as the daemon last read it;
	// empty for DSH since we use --profile headless instead.
	ConfigDir func() string
	Run       Exec
	Program   string    // DSHProgram when empty
	Out       io.Writer // where the run's output goes, when set: the daemon's record
}

// RunsDSHCardRunner says DSH runs each card as a process of its own (a
// DSHCardRunner) when its row is one-shot: it has no session to push a turn
// into, so install and run owe it no deliver-command refusal; its session
// check goes in by the folder (FolderCheck) for a live session to answer.
func RunsDSHCardRunner(harness string) bool { return harness == "dsh" }

// NewDSHCardRunner is the dsh harness's card runner over run, in the friend's
// directory dir, its runs' output going to out when set.
func NewDSHCardRunner(friend, dir string, run Exec, out io.Writer) *DSHCardRunner {
	return &DSHCardRunner{Stub: Stub{Harness: "dsh"}, Friend: friend, Dir: dir, Run: run, Out: out}
}

// Refusal names the missing config_dir and its remedy: a dsh friend in
// one-shot mode runs only with its own working directory as HOME.
func (d *DSHCardRunner) Refusal() string {
	if d.ConfigDir == nil {
		return ""
	}
	return "" // DSH one-shot doesn't require config_dir like Claude
}

// RunCard runs card c to its end: its exit, and an error naming what the
// outbox lacks when the run wrote no REPORT.md or RESULT.md there.
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
	// Run dsh with profile headless, HOME confined to card's job directory
	jobDir := LaneDirOf(ctx, d.Dir)
	if jobDir == "" {
		jobDir = d.Dir
	}
	// dsh --profile headless reads from stdin; we pass the brief on stdin
	out, exit, err := d.Run(ctx, jobDir, program, []string{"--profile", "headless", "-"}, string(brief))
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	// Check for credential refusal in output
	if exit == 1 && strings.Contains(out, "MISSING_CREDENTIAL") {
		// Report as provider refusal, not silent failure
		return LaneTurn{Exit: exit}, ProviderRefused{Reason: "dsh: missing credential for provider route deepseek-official"}
	}
	if exit, err = refused("dsh --profile headless", out, exit, err); err != nil {
		return LaneTurn{Exit: exit}, err
	}
	// Check for missing reports
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
