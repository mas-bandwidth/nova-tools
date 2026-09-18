package swarm

// Batching by shared clone: docs/SPEC-JOBS.md section 6.
//
// Small jobs that touch one data set are packed into one worker turn: the clone is loaded
// once, each card runs, and the clone is reset between cards, so setup is amortised and the
// worker's working set carries from one card to the next.
//
// A pull takes up to batch cards of one kind and one repo in one turn on one kept clone.
// After each card the clone is clipped -- commit, harvest its RESULT.md, reset to base -- so
// card n+1 never sees card n's uncommitted diff. Every card keeps its own RESULT.md, and a
// card over its :effort or turn budget stops the batch and returns the remainder to queue/.
//
// The loop is held apart from git and from the model turn so a test drives it with fakes:
// Run is the turn and Clip is the clip. A policy refusal is one line and exit 2.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PullBatchCard is one card as the puller reads it from queue/: its label, the kind and repo
// that decide which cards may share a batch, and its :effort, the turn bound above which
// the card stops the batch.
type PullBatchCard struct {
	Label  string
	Kind   string
	Repo   string
	Effort int
	Path   string // the card's path in queue/, so the returned remainder can go back
}

// PullInput is one pull turn, held apart from the command line so a test can drive it with
// a fake turn and a fake clip.
type PullInput struct {
	Queue   string // required: the directory cards wait in
	Clone   string // required: the one kept clone every card in the batch runs on
	Harvest string // required: where a card's RESULT.md lands, under <harvest>/<label>/
	Batch   int    // required: the most cards one turn takes; at least 1
	Kind    string // optional: take only cards of this kind
	Repo    string // optional: take only cards of this repo

	// Run runs one card on the kept clone and reports the turns it used. A turn past the
	// card's :effort stops the batch.
	Run func(c PullBatchCard) (turns int, err error)
	// Clip commits the card, harvests its RESULT.md to result, and resets the clone to
	// base, so the next Run starts from a clean tree.
	Clip func(c PullBatchCard, result string) error

	Stdout io.Writer
	Stderr io.Writer
}

// PullBatch takes up to Batch cards of one kind and one repo from Queue, runs each on the
// shared clone, clips between cards, and returns the process exit code: 0 when the turn
// ran, 2 when it could not. A card over its :effort stops the batch and returns that card
// and every card behind it to queue/.
func PullBatch(in PullInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		return pullRefuse(in.Stderr, "--queue is required and names the directory cards wait in")
	}
	if strings.TrimSpace(in.Clone) == "" {
		return pullRefuse(in.Stderr, "--clone is required and names the one kept clone the batch runs on")
	}
	if strings.TrimSpace(in.Harvest) == "" {
		return pullRefuse(in.Stderr, "--harvest is required and names where every card's RESULT.md lands")
	}
	if in.Batch < 1 {
		return pullRefuse(in.Stderr, fmt.Sprintf("--batch is required and is at least 1, got %d", in.Batch))
	}
	if in.Run == nil || in.Clip == nil {
		return pullRefuse(in.Stderr, "the turn and the clip are the pull's two halves and neither may be missing")
	}

	cards, err := readPullQueue(in.Queue)
	if err != nil {
		return pullRefuse(in.Stderr, oneline.Err(err))
	}
	selected := selectPull(cards, in.Kind, in.Repo, in.Batch)
	if len(selected) == 0 {
		fmt.Fprintf(in.Stdout, "PULL OK batch=0 done=0 returned=0 repo=%s kind=%s\n",
			oneline.Field(in.Repo), oneline.Field(in.Kind))
		return 0
	}

	takenDir := filepath.Join(in.Queue, "taken")
	if err := os.MkdirAll(takenDir, 0o755); err != nil {
		return pullRefuse(in.Stderr, oneline.Err(err))
	}
	taken := make([]PullBatchCard, 0, len(selected))
	for _, c := range selected {
		dst := filepath.Join(takenDir, filepath.Base(c.Path))
		if err := os.Rename(c.Path, dst); err != nil {
			requeuePull(in.Queue, taken)
			return pullRefuse(in.Stderr, oneline.Err(err))
		}
		taken = append(taken, c)
	}

	done := 0
	stop := -1
	for i, c := range taken {
		turns, err := in.Run(c)
		if err != nil {
			stop = i
			break
		}
		result := filepath.Join(in.Harvest, c.Label, "RESULT.md")
		if err := os.MkdirAll(filepath.Dir(result), 0o755); err != nil {
			stop = i
			break
		}
		// The clip lands BEFORE the next card runs: commit, harvest, reset to base.
		if err := in.Clip(c, result); err != nil {
			stop = i
			break
		}
		done++
		if c.Effort > 0 && turns > c.Effort {
			// Past its :effort: this card and every card behind it go back to queue/.
			stop = i
			break
		}
	}
	returned := 0
	if stop >= 0 {
		returned = requeuePull(in.Queue, taken[stop:])
	}

	fmt.Fprintf(in.Stdout, "PULL OK batch=%d done=%d returned=%d repo=%s kind=%s\n",
		len(taken), done, returned, oneline.Field(in.Repo), oneline.Field(in.Kind))
	return 0
}

// pullRefuse is the one line an unusable pull costs, naming what was wrong and the door to
// the usage.
func pullRefuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-swarm pull: %s; run: nova-swarm help\n", oneline.Escape(what))
	return 2
}

// readPullQueue lists queue/*.card in name order -- the source order the lanes keep -- and
// reads each card's kind, repo and :effort. A card that cannot be read is named, never
// guessed at.
func readPullQueue(queue string) ([]PullBatchCard, error) {
	entries, err := os.ReadDir(queue)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	cards := make([]PullBatchCard, 0, len(names))
	for _, name := range names {
		path := filepath.Join(queue, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		cards = append(cards, parsePullCard(path, name, string(raw)))
	}
	return cards, nil
}

// parsePullCard reads a card's pull fields: :kind, :repo and :effort. The label is the
// file's name without the .card suffix.
func parsePullCard(path, name, body string) PullBatchCard {
	c := PullBatchCard{Label: strings.TrimSuffix(name, ".card"), Path: path}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			switch fields[i] {
			case ":kind":
				c.Kind = fields[i+1]
			case ":repo":
				c.Repo = fields[i+1]
			case ":effort":
				if n, err := strconv.Atoi(fields[i+1]); err == nil {
					c.Effort = n
				}
			}
		}
	}
	return c
}

// selectPull takes up to batch cards of one kind and one repo: the first card in queue
// order sets both, and the cards behind it that share them follow.
func selectPull(cards []PullBatchCard, kind, repo string, batch int) []PullBatchCard {
	eligible := make([]PullBatchCard, 0, len(cards))
	for _, c := range cards {
		if kind != "" && c.Kind != kind {
			continue
		}
		if repo != "" && c.Repo != repo {
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return nil
	}
	first := eligible[0]
	selected := []PullBatchCard{first}
	for _, c := range eligible[1:] {
		if len(selected) >= batch {
			break
		}
		if c.Kind == first.Kind && c.Repo == first.Repo {
			selected = append(selected, c)
		}
	}
	return selected
}

// requeuePull returns taken cards to queue/ by the same rename that took them. It reports
// how many moved back.
func requeuePull(queue string, cards []PullBatchCard) int {
	n := 0
	for _, c := range cards {
		from := filepath.Join(queue, "taken", filepath.Base(c.Path))
		if err := os.Rename(from, c.Path); err == nil {
			n++
		}
	}
	return n
}
