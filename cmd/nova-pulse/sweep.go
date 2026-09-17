package main

// The sweep verb's flags, and the two ways it reads the world: `gh` on a bench, and a
// source file in a test. An approval is a ledger row and the sweep walks the open rows
// every tick (internal/pulse/ledger.go, issue #828 class D).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdSweep(args []string, stdout, stderr io.Writer) int {
	f := newFlags("sweep")
	repo := f.fs.String("repo", "", "")
	queue := f.fs.String("queue", "", "")
	source := f.fs.String("source", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	decideOrder := f.fs.Bool("decide", false, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	floor := f.fs.Float64("floor", pulse.DefaultOrderFloor, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the repository the open approvals belong to, owner/name")
	f.want(*queue, "queue", "the queue directory holding ledger.tsv and the reads' verdicts")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *floor < 0 || *floor > 1 {
		f.add(fmt.Sprintf("--floor is the ordering score's floor in [0,1], got %v", *floor))
	}
	if f.refused(stderr) {
		return 2
	}

	in := pulse.SweepInput{
		Repo: *repo, Queue: *queue,
		Now: func() time.Time { return time.Now().UTC() }, Stdout: stdout, Stderr: stderr,
	}
	bound := time.Duration(*timeout) * time.Second
	in.Source = pulse.GHSource{Timeout: bound}
	in.Enqueuer = pulse.GHEnqueuer{Timeout: bound}
	if *decideOrder {
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			fmt.Fprintf(stderr, "SWEEP REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		in.Scorer = pulse.DecideScorer{Ask: client}
		in.Floor = *floor
	}
	if *source != "" {
		rows, err := readPRSourceFile(*source)
		if err != nil {
			fmt.Fprintf(stderr, "SWEEP REFUSED: --source %s: %s (one PR per line: pr, state, draft, head, labels, title, checks, automerge)\n", oneline.Field(*source), oneline.Err(err))
			return 2
		}
		in.Source = rows
		in.Enqueuer = &fileEnqueuer{path: filepath.Join(*queue, "enqueued.tsv")}
	}
	return pulse.Sweep(in)
}

// fileSource is a PRSource read off a file: the shape a bench writes when it wants the
// sweep replayed without a network, and the shape the tests drive.
type fileSource map[int]pulse.PRView

func (f fileSource) View(repo string, pr int) (pulse.PRView, error) {
	view, ok := f[pr]
	if !ok {
		return pulse.PRView{}, fmt.Errorf("--source names no pull request %d", pr)
	}
	return view, nil
}

// readPRSourceFile reads eight tab-separated fields per line: pr, state, draft, head,
// labels (comma separated or -), title, checks (name:STATE,… or -), automerge.
func readPRSourceFile(path string) (fileSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := fileSource{}
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			return nil, fmt.Errorf("line %d wants 8 fields, got %d", n+1, len(fields))
		}
		pr, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %q is not a PR number", n+1, fields[0])
		}
		view := pulse.PRView{
			Number:    pr,
			State:     fields[1],
			IsDraft:   fields[2] == "true",
			Head:      fields[3],
			Title:     fields[5],
			AutoMerge: fields[7] == "true",
		}
		if fields[4] != "-" {
			view.Labels = strings.Split(fields[4], ",")
		}
		if fields[6] != "-" {
			for _, c := range strings.Split(fields[6], ",") {
				name, state, _ := strings.Cut(c, ":")
				view.Checks = append(view.Checks, pulse.PRCheck{Name: name, State: state})
			}
		}
		rows[pr] = view
	}
	return rows, nil
}

// fileEnqueuer is what a --source run enqueues into: a line per PR in <queue>/enqueued.tsv,
// so a replayed sweep never touches the merge queue of a real repository.
type fileEnqueuer struct{ path string }

func (f *fileEnqueuer) Enqueue(repo string, pr int) error {
	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "%s\t%d\n", oneline.Field(repo), pr)
	return err
}
