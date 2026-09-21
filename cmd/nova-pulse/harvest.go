package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdHarvest(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("harvest")
	id := f.fs.String("id", "", "")
	root := f.fs.String("root", "", "")
	sources := f.fs.String("sources", "", "")
	templates := f.fs.String("templates", "", "")
	maxBodyBytes := f.fs.Int("max-body-bytes", 4096, "")
	max := f.fs.Int("max", 20, "")
	decideOn := f.fs.Bool("decide", false, "")
	floor := f.fs.Float64("floor", 0.9, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	bench := f.fs.String("bench", "", "")
	machines := f.fs.String("machines", "", "")
	ssh := f.fs.String("ssh", "", "")
	session := f.fs.String("session", "", "")
	branchPrefix := f.fs.String("branch-prefix", pulse.DefaultBranchPrefix, "")
	base := f.fs.String("base", "", "")
	since := f.fs.String("since", "", "")
	launched := f.fs.String("launched", "", "")
	doneDir := f.fs.String("done", "", "")
	failedDir := f.fs.String("failed", "", "")
	batch := f.fs.Bool("batch", false, "")
	store := f.fs.String("store", "", "")
	storeUser := f.fs.String("store-user", "", "")
	passwordEnv := f.fs.String("password-env", pulse.DefaultStorePasswordEnv, "")
	var clones benchFlag
	f.fs.Var(&clones, "clone", "")
	working := f.fs.String("working", "", "")
	roots := f.fs.String("roots", "", "")
	timer := f.fs.String("timer", "", "")
	resultsDir := f.fs.String("results", "", "")
	commitFlag := f.fs.Bool("commit", false, "")

	if !f.parse(args, stderr) {
		return 2
	}

	commitSet := false
	f.fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "commit" {
			commitSet = true
		}
	})

	onBench := strings.TrimSpace(*bench) != ""
	commitVal := *commitFlag
	if !commitSet && onBench {
		commitVal = true
	}

	// The working layout names no --id and no --root: it folds the bench's jobs
	// under --working and the swarm roots under --roots. The old layout is
	// unchanged and still wants both.
	if strings.TrimSpace(*working) != "" || strings.TrimSpace(*roots) != "" {
		if *max < 0 {
			f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
		}
		if f.refused(stderr) {
			return 2
		}
		// --clone is where a --working harvest's DESTINATION comes from, and the only
		// place it can come from: every job under --working was written by a worker,
		// and a worker owns its own clone's `origin` (Johnny's HOLD of #1809 at
		// 7f692ef6). Without it a job that would publish is refused repo-unknown by
		// name; the fold itself still runs and still classifies.
		return pulse.HarvestWorking(pulse.HarvestInput{
			Working:      *working,
			Roots:        *roots,
			Base:         *base,
			SinceStamp:   *since,
			Timer:        *timer,
			Max:          *max,
			Clones:       []string(clones),
			BranchPrefix: *branchPrefix,
			Commit:       commitVal,
			ResultsDir:   *resultsDir,
			Stdout:       stdout,
			Stderr:       stderr,
			Now:          func() time.Time { return now },
		})
	}
	// A bench harvest folds what is on the bench. There is no pulse packet to name and no
	// relaunch to feed, so --id, --sources and --templates are not its to supply: a
	// `cut --rows` produces none of the three (dogfood, 2026-09-18).
	if !onBench {
		f.want(*id, "id", "the pulse id whose cards this harvest folds")
	}
	f.want(*root, "root", "the pulse root this pulse's state hangs under, or with --bench the swarm root ON the bench")
	var age time.Duration
	if s := strings.TrimSpace(*since); s != "" {
		d, err := time.ParseDuration(s)
		switch {
		case err != nil:
			f.add(fmt.Sprintf("--since wants a duration like 6h, got %q", s))
		case d < 0:
			f.add(fmt.Sprintf("--since is 0 or more, got %s", d))
		default:
			age = d
		}
	}
	if onBench && len(clones) == 0 {
		f.add("--clone is required with --bench; the branch is pushed from a clone HERE, never from the bench (pass --clone <dir>, or --clone <owner>/<name>=<dir> per repo)")
	}
	if *maxBodyBytes <= 0 {
		f.add(fmt.Sprintf("--max-body-bytes wants a positive byte count, got %d", *maxBodyBytes))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *decideOn && (*floor < 0 || *floor > 1) {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g", *floor))
	}
	if f.refused(stderr) {
		return 2
	}
	in := pulse.HarvestInput{
		ID:           *id,
		Root:         *root,
		Sources:      *sources,
		Templates:    *templates,
		MaxBodyBytes: *maxBodyBytes,
		Max:          *max,
		Stdout:       stdout,
		Stderr:       stderr,
		Bench:        *bench,
		Machines:     *machines,
		SSH:          *ssh,
		Clones:       []string(clones),
		Session:      *session,
		BranchPrefix: *branchPrefix,
		Base:         *base,
		Since:        age,
		Launched:     *launched,
		Done:         *doneDir,
		Failed:       *failedDir,
		Commit:       commitVal,
		Batch:        *batch,
		Store:        pulse.StoreOptions{Addr: *store, User: *storeUser, PasswordEnv: *passwordEnv},
		ResultsDir:   *resultsDir,
	}
	if *decideOn {
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, " harvest", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		in.Decide, in.Floor, in.Decider = true, *floor, client
	}
	return pulse.Harvest(in)
}
