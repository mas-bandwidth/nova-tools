package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE PULL (docs/SPEC-JOBS.md, "Redis ready set (the pull)"). A bench is a
// consumer in the one `benches` group over `cards:ready`; it claims exactly one
// card with XREADGROUP, takes the `lease:<id>` key for the card with a TTL,
// renews that lease while the card runs, writes the card to
// `<slot-root>/<id>-<label>/cards/<label>.md`, runs it the way `nova-swarm
// native` does, XACKs the entry when the run is done, and writes the outcome to
// `cards:done`. A lease that lapses lets another bench XAUTOCLAIM the entry, so
// no launch happens without a lease (SPEC-JOBS rule 2).

// cardRun is everything one claimed card hands the runner. renew heartbeats the
// lease and is the seam a test uses to exercise renewal without a clock.
type cardRun struct {
	ID       string
	EntryID  string
	Label    string
	Bench    string
	SlotRoot string
	SlotDir  string
	CardPath string
	JobDir   string
	Card     []byte
	Deadline time.Duration
	native   pullNative
	renew    func() error
}

// pullNative is the frozen native run a card's runner is handed, one field per
// `nova-swarm native` flag.
type pullNative struct {
	harness        string
	model          string
	worker         string
	auth           string
	config         string
	sandbox        string
	noWall         bool
	noSharedCaches bool
	repos          []string
}

// pullRunner executes one card and returns its exit code, the first line of the
// card's RESULT.md, and an error when the run could not be started.
type pullRunner func(ctx context.Context, run cardRun, stdout, stderr io.Writer) (int, string, error)

var (
	// executePull is the run step. It is a variable so a unit test can hand the
	// pull a fake runner and assert the ack order without starting a harness.
	executePull pullRunner = executeNativeCard
	// pullBlock is the XREADGROUP block on an empty ready set, 30 s on a bench.
	pullBlock = 30 * time.Second
	// pullRenewInterval is the lease heartbeat: a running card renews its lease
	// once a minute.
	pullRenewInterval = time.Minute
)

func cmdPull(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("pull")
	redisAddr := f.fs.String("redis", "", "")
	bench := f.fs.String("bench", "", "")
	slotRoot := f.fs.String("slot-root", "", "")
	once := f.fs.Bool("once", false, "")
	leaseStr := f.fs.String("lease", "45m", "")
	deadlineStr := f.fs.String("deadline", "", "")
	runDeadlineStr := f.fs.String("run-deadline", "30m", "")
	harness := f.fs.String("harness", "", "")
	model := f.fs.String("model", "", "")
	worker := f.fs.String("worker", "", "")
	auth := f.fs.String("auth", "", "")
	config := f.fs.String("config", "", "")
	sandbox := f.fs.String("sandbox", "", "")
	noWall := f.fs.Bool("no-wall", false, "")
	noSharedCaches := f.fs.Bool("no-shared-caches", false, "")
	var repos []string
	f.fs.Var(stringListValue{&repos}, "repo", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*redisAddr, "redis", "the address of the Redis instance that holds the ready set")
	f.want(*bench, "bench", "this bench's name, the consumer it reads the ready set as")
	f.want(*slotRoot, "slot-root", "the directory this bench's slots are made under")
	lease := parseDurationFlag(f, "lease", *leaseStr, "the TTL of a card's lease")
	runDeadline := parseDurationFlag(f, "run-deadline", *runDeadlineStr, "the wall that kills one card's child")
	loopDeadline := time.Duration(0)
	if strings.TrimSpace(*deadlineStr) != "" {
		loopDeadline = parseDurationFlag(f, "deadline", *deadlineStr, "the wall after which this loop claims no more cards")
	}
	if f.refused(stderr) {
		return 2
	}

	ctx := context.Background()
	client, err := redisq.Open(*redisAddr)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: the ready set at %s is not reachable: %s\n", oneline.Field(*redisAddr), oneline.Err(err))
		return 2
	}
	defer client.Close()
	if err := client.EnsureGroup(ctx, redisq.ReadyStream, redisq.Group, "0"); err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: the %s group could not be made on %s: %s\n",
			oneline.Field(redisq.Group), oneline.Field(redisq.ReadyStream), oneline.Err(err))
		return 2
	}

	native := pullNative{
		harness: *harness, model: *model, worker: *worker, auth: *auth, config: *config,
		sandbox: *sandbox, noWall: *noWall, noSharedCaches: *noSharedCaches, repos: repos,
	}
	deadline := time.Time{}
	if loopDeadline > 0 {
		deadline = now.Add(loopDeadline)
	}
	for {
		if !deadline.IsZero() && time.Now().UTC().After(deadline) {
			return 0
		}
		entry, err := client.ReadGroup(ctx, redisq.ReadyStream, redisq.Group, *bench, pullBlock)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm pull: reading %s for %s: %s\n", oneline.Field(redisq.ReadyStream), oneline.Field(*bench), oneline.Err(err))
			return 2
		}
		if entry == nil {
			entry, err = client.AutoClaim(ctx, redisq.ReadyStream, redisq.Group, *bench, lease)
			if err != nil {
				fmt.Fprintf(stderr, "nova-swarm pull: reclaiming a lapsed card for %s: %s\n", oneline.Field(*bench), oneline.Err(err))
				return 2
			}
			if entry != nil {
				// A card whose lease is still held is a live run, not a lapsed
				// one: XAUTOCLAIM would move it, so it is left alone.
				if v, _ := client.LeaseValue(ctx, redisq.LeaseKey(entry.ID)); v != "" {
					entry = nil
				}
			}
		}
		if entry == nil {
			if *once {
				return 0
			}
			continue
		}
		if err := pullOne(ctx, client, entry, *bench, *slotRoot, lease, runDeadline, native, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(err))
			return 2
		}
		if *once {
			return 0
		}
	}
}

// parseDurationFlag reads one duration flag, recording a want on a flag the
// caller typed but this tool cannot read. A blank value is the caller saying
// nothing, and zero is returned.
func parseDurationFlag(f *flags, name, value, wants string) time.Duration {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		f.add(fmt.Sprintf("--%s wants a positive duration such as 45m, got %q; it wants %s",
			oneline.Field(name), value, oneline.Field(wants)))
		return 0
	}
	return d
}

// pullOne takes the lease, writes the card, runs it, XACKs it and records the
// outcome. A card whose lease another bench still holds is skipped whole.
func pullOne(ctx context.Context, client redisq.Client, entry *redisq.Entry, bench, slotRoot string, lease, runDeadline time.Duration, native pullNative, stdout, stderr io.Writer) error {
	id := entry.Field("id")
	if id == "" {
		id = entry.ID
	}
	label := entry.Field("label")
	if label == "" {
		label = id
	}
	leaseKey := redisq.LeaseKey(entry.ID)
	held, err := client.AcquireLease(ctx, leaseKey, bench, lease)
	if err != nil {
		return fmt.Errorf("taking the lease for %s: %w", entry.ID, err)
	}
	if !held {
		return nil
	}

	slotDir := filepath.Join(slotRoot, id+"-"+label)
	cardDir := filepath.Join(slotDir, "cards")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		return fmt.Errorf("making the card directory %s: %w", cardDir, err)
	}
	cardPath := filepath.Join(cardDir, label+".md")
	if err := os.WriteFile(cardPath, []byte(entry.Field("body")), 0o644); err != nil {
		return fmt.Errorf("writing the card %s: %w", cardPath, err)
	}
	jobDir := filepath.Join(slotDir, "jobs", label)
	renew := func() error {
		_, err := client.RenewLease(ctx, leaseKey, bench, lease)
		return err
	}
	run := cardRun{
		ID: id, EntryID: entry.ID, Label: label, Bench: bench, SlotRoot: slotRoot, SlotDir: slotDir,
		CardPath: cardPath, JobDir: jobDir, Card: []byte(entry.Field("body")),
		Deadline: runDeadline, native: native, renew: renew,
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(pullRenewInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = renew()
			}
		}
	}()

	exit, result, runErr := executePull(ctx, run, stdout, stderr)
	close(stop)
	wg.Wait()

	// XACK ON COMPLETION: a card is acked when its run is over, so a dead
	// worker's card stays pending and the next bench reclaims it.
	if err := client.Ack(ctx, redisq.ReadyStream, redisq.Group, entry.ID); err != nil {
		return fmt.Errorf("acking %s: %w", entry.ID, err)
	}
	_, _ = client.ReleaseLease(ctx, leaseKey, bench)

	commit, branch := gitInfo(jobDir)
	done := map[string]string{
		"id": id, "label": label, "bench": bench,
		"exit": strconv.Itoa(exit), "result": result, "job": jobDir,
		"commit": dash(commit), "branch": dash(branch),
	}
	if _, err := client.Add(ctx, redisq.DoneStream, done); err != nil {
		return fmt.Errorf("writing the result for %s: %w", id, err)
	}
	fmt.Fprintf(stdout, "PULL OK id=%s label=%s bench=%s exit=%d\n",
		oneline.Field(entry.ID), oneline.Field(label), oneline.Field(bench), exit)
	if runErr != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: the run of %s ended: %s\n", oneline.Field(label), oneline.Err(runErr))
	}
	return nil
}

// executeNativeCard runs one card exactly as `nova-swarm native` does: the same
// frozen configuration, the same wall and the same harness. The runner returns
// the child's exit code and line 1 of its RESULT.md.
func executeNativeCard(ctx context.Context, run cardRun, stdout, stderr io.Writer) (int, string, error) {
	n := run.native
	var w swarm.Worker
	workerGiven := n.worker != ""
	if workerGiven {
		loaded, problems := swarm.LoadWorker(n.worker)
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(p))
			}
			return 0, "", fmt.Errorf("the worker description %s could not be loaded", n.worker)
		}
		w = loaded
	}
	model := n.model
	if workerGiven && model == "" {
		model = w.Provider + "/" + w.Model
	}
	cfg := nativeRunConfig{
		binary:         n.harness,
		model:          model,
		label:          run.Label,
		card:           run.Card,
		slotDir:        run.SlotDir,
		root:           run.SlotRoot,
		authFile:       n.auth,
		configFile:     n.config,
		deadline:       run.Deadline,
		repos:          n.repos,
		sandbox:        n.sandbox,
		noWall:         n.noWall,
		noSharedCaches: n.noSharedCaches,
	}
	if workerGiven {
		cfg.worker = &w
	}
	res, code := nativeRun(cfg, stderr)
	if code != 0 {
		return code, "", fmt.Errorf("native refused the card %s", run.Label)
	}
	return res.rc, firstResultLine(res.job), nil
}

// firstResultLine is line 1 of the RESULT.md the run published, or "-" when the
// run published none. It looks where the gather looks (swarm.FindCardResult).
func firstResultLine(jobDir string) string {
	path, ok := swarm.FindCardResult(jobDir)
	if !ok {
		return "-"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	return strings.TrimRight(line, "\r")
}

// gitInfo is the commit and branch a card's job directory records, when it is a
// git checkout at all. An absent repository is two dashes, never an error: a
// card need not touch git.
func gitInfo(dir string) (commit, branch string) {
	if _, err := os.Stat(dir); err != nil {
		return "", ""
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	return commit, branch
}

// cmdPullQueue is the directory-queue pull (SPEC-JOBS section 4): it prefers the
// card whose repo the bench already holds in a kept worktree under
// <slot>/worktrees/<owner>/<name>, so the clone is reused and cache warmth is
// kept. With no warm card it falls back to the first card and a fetch from the
// bench mirror. It stays reachable for a bench that has no Redis address, so
// both the Redis ready set and the directory queue keep working.
func cmdPullQueue(args []string, stdout, stderr io.Writer) int {
	f := newFlags("pull")
	slot := f.fs.String("slot", "", "")
	queue := f.fs.String("queue", "", "")
	mirror := f.fs.String("mirror", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*slot, "slot", "the bench slot whose kept worktrees live under <slot>/worktrees/<owner>/<name>")
	f.want(*queue, "queue", "the bench's queue directory of cards")
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

// refusePull writes the one line a pull that could not run owes its caller.
func refusePull(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PULL REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

// hasFlag reports whether args carries the named long flag, either as `--name`
// or as `--name=value`. The pull dispatch reads it to send a caller with a
// Redis address to the ready set and every other caller to the directory queue.
func hasFlag(args []string, name string) bool {
	prefix := "--" + name
	for _, a := range args {
		if a == prefix || strings.HasPrefix(a, prefix+"=") {
			return true
		}
	}
	return false
}
