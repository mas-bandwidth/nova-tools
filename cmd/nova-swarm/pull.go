package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// PULL (docs/SPEC-JOBS.md sections 4 and 6). The verb carries two shapes, and one run picks
// the shape from the flags it was handed.
//
// Batching (section 6): pull takes up to --batch cards of one kind and one repo in one turn
// on the one kept clone named by --clone, clips after each card (commit, harvest its
// RESULT.md, reset to base), and returns the remainder to queue/ when a card goes past its
// :effort. The runner is one command per card, handed the card and the clone through
// PULL_CARD, PULL_CLONE and PULL_LABEL and reporting its turns as a trailing `TURNS <n>`
// line. The clip is git against --clone, resetting to --base or the clone's own HEAD when
// --base is absent.
//
// Affinity (section 4): a card carries a kind and a repo; pull prefers the card whose repo
// the bench already holds in a kept worktree under <slot>/worktrees/<owner>/<name>, so the
// clone is reused and cache warmth is kept. With no warm card it falls back to the first
// card and a fetch from the bench mirror.
//
// Every path comes from a flag; there is no default slot, queue or mirror.
func cmdPull(args []string, stdout, stderr io.Writer) int {
	f := newFlags("pull")
	slot := f.fs.String("slot", "", "")
	queue := f.fs.String("queue", "", "")
	mirror := f.fs.String("mirror", "", "")
	clone := f.fs.String("clone", "", "")
	harvest := f.fs.String("harvest", "", "")
	batch := f.fs.Int("batch", 0, "")
	kind := f.fs.String("kind", "", "")
	repo := f.fs.String("repo", "", "")
	runner := f.fs.String("runner", "", "")
	base := f.fs.String("base", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the directory cards wait in")

	// Batching (section 6) is the shape whenever any of its own flags is present; otherwise
	// the run is the affinity pull of section 4.
	if *batch > 0 || strings.TrimSpace(*runner) != "" || strings.TrimSpace(*clone) != "" {
		f.want(*clone, "clone", "the one kept clone every card in the batch runs on")
		f.want(*harvest, "harvest", "where every card's RESULT.md lands, one directory per label")
		f.wantCount(*batch, "batch", "the most cards one turn takes, all of one kind and one repo")
		f.want(*runner, "runner", "the command one process per card runs")
		if f.refused(stderr) {
			return 2
		}
		baseRev := strings.TrimSpace(*base)
		if baseRev == "" {
			out, err := exec.Command("git", "-C", *clone, "rev-parse", "HEAD").Output()
			if err != nil {
				return refuse(stderr, " pull", "the clone has no HEAD to reset to; pass --base <rev>")
			}
			baseRev = strings.TrimSpace(string(out))
		}
		return swarm.PullBatch(swarm.PullInput{
			Queue:   *queue,
			Clone:   *clone,
			Harvest: *harvest,
			Batch:   *batch,
			Kind:    strings.TrimSpace(*kind),
			Repo:    strings.TrimSpace(*repo),
			Run: func(c swarm.PullBatchCard) (int, error) {
				return runPullCard(*runner, c, *clone)
			},
			Clip: func(c swarm.PullBatchCard, result string) error {
				return clipPullClone(*clone, baseRev, c.Label, result)
			},
			Stdout: stdout,
			Stderr: stderr,
		})
	}

	// Affinity (section 4).
	f.want(*slot, "slot", "the bench slot whose kept worktrees live under <slot>/worktrees/<owner>/<name>")
	f.want(*mirror, "mirror", "the bench mirror a cold clone fetches from")
	if f.refused(stderr) {
		return 2
	}
	cards, err := swarm.ReadCardDir(*queue)
	if err != nil {
		return refusePull(stderr, oneline.Err(err))
	}
	got, err := swarm.Prefer([]swarm.PullBench{{Name: "local", Slot: *slot, Mirror: *mirror, Queue: cards}})
	if err != nil {
		return refusePull(stderr, oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PULL OK card=%s kind=%s repo=%s warm=%t worktree=%s",
		oneline.Field(got.Card.Name), oneline.Field(got.Card.Kind), oneline.Field(got.Card.Repo),
		got.Warm, oneline.Field(got.Path))
	if !got.Warm {
		fmt.Fprintf(stdout, " fetch=%s", oneline.Field(got.Fetch))
	}
	fmt.Fprintln(stdout)
	return 0
}

// runPullCard runs one card's turn on the kept clone and reads the trailing TURNS <n> line.
func runPullCard(runner string, c swarm.PullBatchCard, clone string) (int, error) {
	cmd := exec.Command("sh", "-c", runner)
	cmd.Env = append(os.Environ(),
		"PULL_CARD="+c.Path,
		"PULL_CLONE="+clone,
		"PULL_LABEL="+c.Label,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = oneline.Err(err)
		}
		return 0, fmt.Errorf("card %s: %s", c.Label, reason)
	}
	return pullTurns(string(out)), nil
}

// pullTurns reads the turns a card used from the last TURNS <n> line. A card that names
// none used zero: :effort is then never exceeded, which is the honest reading of silence.
func pullTurns(out string) int {
	turns := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "TURNS" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				turns = n
			}
		}
	}
	return turns
}

// clipPullClone commits the card's work, harvests its RESULT.md to result, and resets the
// clone to base -- the clip that keeps card n's uncommitted diff out of card n+1.
func clipPullClone(clone, base, label, result string) error {
	git := func(args ...string) (string, error) {
		full := append([]string{"-C", clone}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("add", "-A"); err != nil {
		return fmt.Errorf("clip %s: %s", label, oneline.Err(err))
	}
	// A card that changed nothing has nothing to commit, and that is not a clip failure.
	if _, err := git("-c", "user.name=nova-swarm", "-c", "user.email=nova-swarm@localhost",
		"commit", "-q", "-m", "pull "+label); err != nil {
		if _, statusErr := git("diff", "--cached", "--quiet"); statusErr == nil {
			return fmt.Errorf("clip %s: commit: %s", label, oneline.Err(err))
		}
	}
	if err := harvestPullResult(clone, result); err != nil {
		return err
	}
	if _, err := git("reset", "--hard", base); err != nil {
		return fmt.Errorf("clip %s: reset: %s", label, oneline.Err(err))
	}
	if _, err := git("clean", "-fd"); err != nil {
		return fmt.Errorf("clip %s: clean: %s", label, oneline.Err(err))
	}
	return nil
}

// harvestPullResult copies the card's RESULT.md out of the clone. It looks where a card
// actually leaves it: the clone root, or one level under repo/.
func harvestPullResult(clone, result string) error {
	candidates := []string{
		filepath.Join(clone, "RESULT.md"),
		filepath.Join(clone, "repo", "RESULT.md"),
	}
	under, _ := filepath.Glob(filepath.Join(clone, "repo", "*", "RESULT.md"))
	candidates = append(candidates, under...)
	for _, from := range candidates {
		raw, err := os.ReadFile(from)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(result), 0o755); err != nil {
			return fmt.Errorf("harvest %s: %v", result, err)
		}
		if err := os.WriteFile(result, raw, 0o644); err != nil {
			return fmt.Errorf("harvest %s: %v", result, err)
		}
		// One RESULT.md per card: the clone's copy has been harvested and the reset
		// removes it, so the next card cannot read it as its own.
		return nil
	}
	return nil
}

// refusePull writes the one line a pull that could not run owes its caller.
func refusePull(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PULL REFUSED: %s\n", oneline.Escape(reason))
	return 2
}
