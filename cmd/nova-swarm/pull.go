// pull is the pull worker of docs/SPEC-STATE.md Part 2: it reads one card from the
// Redis Streams queue with XREADGROUP over the one shared `workers` group, reclaims a dead
// worker's card with XAUTOCLAIM after the lease, runs the card, and XACKs it on clip. The
// directory queue is the fallback a bench chooses at start with --dir, and the two stores
// are never both active: the mode is chosen once and every read follows it.
//
// PULL (slice 4 of SPEC-JOBS, section 4). A card carries a kind and a repo; pull prefers
// the card whose repo the bench already holds in a kept worktree under
// <slot>/worktrees/<owner>/<name>, so the clone is reused and cache warmth is kept. With
// no warm card it falls back to the first card and a fetch from the bench mirror. Every
// path comes from a flag; there is no default slot, queue or mirror.
//
// Both habits live on the one `pull` verb: the SPEC-STATE flags (--stream/--bench and
// --redis/--dir) select the stream worker, and the SPEC-JOBS flags (--slot/--queue/--mirror)
// select the warm-worktree prefer.
//
// PULL (docs/SPEC-JOBS.md sections 2, 3, 4, 6 and pull worker daemon).
//
// The verb carries multiple shapes, picked from the flags:
// 1. Worker daemon: --bench <name> --slots <n> --seat <seat> runs cards in container
// 2. Batching (section 6): --batch, --clone, --harvest, --runner
// 3. Affinity (section 4): --slot, --queue, --mirror
// 4. Stealing (section 2): --bench, --worker, [--steal, --capacity, --last-steal]
// 5. Leases (section 3): --store, --owner, --for
// 6. Redis/Dir Streams: --stream, --bench, --redis/--dir, --lane
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// pullLanes is the read order: lanes are read red, then green, then small, then next, so
// priority is the stream suffix and the read order, never a scorer.
var pullLanes = []string{"red", "green", "small", "next"}

// pullStreamFlags are the flags that select the SPEC-STATE stream worker; any one of them
// present means the caller asked for the stream, not the warm-worktree prefer.
var pullStreamFlags = []string{"stream", "redis", "dir", "lane", "wait"}

func cmdPull(args []string, stdout, stderr io.Writer) int {
	now := time.Now().UTC()
	f := newFlags("pull")

	// Stream flags (SPEC-STATE)
	stream := f.fs.String("stream", "", "")
	redisAddr := f.fs.String("redis", "", "")
	dir := f.fs.String("dir", "", "")
	lane := f.fs.String("lane", "", "")
	wait := f.fs.Duration("wait", 0, "")

	// Affinity flags (section 4)
	slot := f.fs.String("slot", "", "")
	queue := f.fs.String("queue", "", "")
	mirror := f.fs.String("mirror", "", "")

	// Batching flags (section 6)
	clone := f.fs.String("clone", "", "")
	harvest := f.fs.String("harvest", "", "")
	batch := f.fs.Int("batch", 0, "")
	kind := f.fs.String("kind", "", "")
	repo := f.fs.String("repo", "", "")
	runner := f.fs.String("runner", "", "")
	base := f.fs.String("base", "", "")

	// Stealing / Per-bench queue flags (section 2)
	bench := f.fs.String("bench", "", "")
	worker := f.fs.String("worker", "", "")
	steal := f.fs.String("steal", "", "")
	capacity := f.fs.Int("capacity", 0, "")
	lastSteal := f.fs.String("last-steal", "", "")

	// Lease take flags (section 3)
	store := f.fs.String("store", "", "")
	owner := f.fs.String("owner", "", "")
	forDur := f.fs.String("for", "", "")

	// Pull Worker daemon flags
	slots := f.fs.Int("slots", 0, "")
	seat := f.fs.String("seat", "", "")
	image := f.fs.String("image", "", "")
	model := f.fs.String("model", "", "")
	container := f.fs.String("container", "", "")
	once := f.fs.Bool("once", false, "")

	if !f.parse(args, stderr) {
		return 2
	}

	if pullWantsStream(f) {
		f.want(*stream, "stream", "the card kind this bench pulls, from nova:queue:<kind>:<lane>")
		f.want(*bench, "bench", "this bench's consumer name inside the shared workers group")
		mode := redisq.ChooseMode(*redisAddr)
		if mode == redisq.ModeDirectory && strings.TrimSpace(*dir) == "" {
			f.add("--redis or --dir is required; it wants where the queue lives: the Redis address this bench reads, or the directory it falls back to")
		}
		if mode == redisq.ModeRedis && strings.TrimSpace(*dir) != "" {
			f.add("--redis and --dir together name two stores; one mode per bench, chosen at start, and a slot granted by both is a fence no token sees")
		}
		lanes := pullLanes
		if strings.TrimSpace(*lane) != "" {
			lanes = []string{strings.TrimSpace(*lane)}
		}
		if f.refused(stderr) {
			return 2
		}
		if mode == redisq.ModeRedis {
			return pullRedis(*redisAddr, *stream, *bench, lanes, *wait, stdout, stderr)
		}
		return pullDirectory(*dir, *stream, *bench, lanes, stdout, stderr)
	}

	// 1. Batching (section 6)
	if *batch > 0 || strings.TrimSpace(*clone) != "" || (strings.TrimSpace(*runner) != "" && strings.TrimSpace(*bench) == "") {
		return pullBatch(f, *queue, *clone, *harvest, *base, *kind, *repo, *runner, *batch, stdout, stderr)
	}

	// 2. Affinity (section 4)
	if strings.TrimSpace(*slot) != "" || strings.TrimSpace(*mirror) != "" {
		return pullWarm(f, *slot, *queue, *mirror, stdout, stderr)
	}

	// 3. Worker daemon: --seat or --slots given, or --bench given without --worker
	if strings.TrimSpace(*seat) != "" || *slots > 0 || (strings.TrimSpace(*bench) != "" && strings.TrimSpace(*worker) == "") {
		return pullWorker(f, *bench, *slots, *seat, *store, *harvest, *image, *model, *runner, *container, *forDur, *once, stdout, stderr)
	}

	// 4. Lease take (section 3): --store and --owner given without --bench
	if strings.TrimSpace(*store) != "" && strings.TrimSpace(*owner) != "" {
		return pullLease(f, *store, *owner, *forDur, stdout, stderr, now)
	}

	// 5. Stealing / Per-bench queue (section 2)
	return pullBench(f, *bench, *worker, *steal, *capacity, *lastSteal, stdout, stderr, now)
}

// pullWorker runs the pull worker process in container/runner.
func pullWorker(f *flags, bench string, slots int, seat, store, harvest, image, model, runner, container, forDur string, once bool, stdout, stderr io.Writer) int {
	f.want(bench, "bench", "the bench name or directory")
	if slots <= 0 {
		slots = 1
	}
	dur := swarm.DefaultWorkerLeaseDur
	if strings.TrimSpace(forDur) != "" {
		d, err := time.ParseDuration(forDur)
		if err != nil || d <= 0 {
			f.add(fmt.Sprintf("--for wants a positive duration, got %q", forDur))
		} else {
			dur = d
		}
	}
	if f.refused(stderr) {
		return 2
	}
	ctx := context.Background()

	opts := swarm.PullWorkerOptions{
		Bench:     bench,
		Slots:     slots,
		Seat:      seat,
		Store:     store,
		Harvest:   harvest,
		Image:     image,
		Model:     model,
		Runner:    runner,
		Container: container,
		For:       dur,
		Once:      once,
		Stdout:    stdout,
		Stderr:    stderr,
	}
	return swarm.RunPullWorker(ctx, opts)
}

// pullLease takes a card from store/ under a slot lease (section 3).
func pullLease(f *flags, store, owner, forDur string, stdout, stderr io.Writer, now time.Time) int {
	f.want(store, "store", "the bench store holding shares.tsv, queue/ and slots/")
	f.want(owner, "owner", "whose share the lease counts against")
	dur, err := time.ParseDuration(forDur)
	if err != nil || dur <= 0 {
		f.add(fmt.Sprintf("--for wants a positive duration such as 30m, got %q", forDur))
	}
	if f.refused(stderr) {
		return 2
	}

	res, err := swarm.PullCard(store, owner, dur, now, os.Getpid())
	if swarm.IsNoCard(err) {
		fmt.Fprintf(stdout, "PULL IDLE owner=%s cards=0\n", oneline.Field(owner))
		return 0
	}
	if ref, ok := swarm.AsLeaseRefusal(err); ok {
		return refuse(stderr, " pull", fmt.Sprintf(
			"no lease for owner=%s card=%s held=%d share=%d free=%d holders=%s; a launch without a lease is refused",
			oneline.Field(owner), oneline.Field(ref.Card), ref.Held, ref.Share, ref.Free, oneline.Escape(ref.Holders)))
	}
	if err != nil {
		return refuse(stderr, " pull", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PULL OK owner=%s card=%s lease=%s until=%s\n",
		oneline.Field(res.Owner), oneline.Field(res.Card), oneline.Field(res.Lease),
		oneline.Field(res.Until.UTC().Format(time.RFC3339)))
	return 0
}

// pullWarm is affinity pull (section 4).
func pullWarm(f *flags, slot, queue, mirror string, stdout, stderr io.Writer) int {
	f.want(slot, "slot", "the bench slot whose kept worktrees live under <slot>/worktrees/<owner>/<name>")
	f.want(queue, "queue", "the bench's queue directory of cards")
	f.want(mirror, "mirror", "the bench mirror a cold clone fetches from")
	if f.refused(stderr) {
		return 2
	}

	cards, err := swarm.ReadCardDir(queue)
	if err != nil {
		return refusePull(stderr, oneline.Err(err))
	}
	got, err := swarm.Prefer([]swarm.PullBench{{Name: "local", Slot: slot, Mirror: mirror, Queue: cards}})
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

// pullBatch is batch pull (section 6).
func pullBatch(f *flags, queue, clone, harvest, base, kind, repo, runner string, batch int, stdout, stderr io.Writer) int {
	f.want(queue, "queue", "the directory cards wait in")
	f.want(clone, "clone", "the one kept clone every card in the batch runs on")
	f.want(harvest, "harvest", "where every card's RESULT.md lands, one directory per label")
	f.wantCount(batch, "batch", "the most cards one turn takes, all of one kind and one repo")
	f.want(runner, "runner", "the command one process per card runs")
	if f.refused(stderr) {
		return 2
	}
	baseRev := strings.TrimSpace(base)
	if baseRev == "" {
		out, err := exec.Command("git", "-C", clone, "rev-parse", "HEAD").Output()
		if err != nil {
			return refuse(stderr, " pull", "the clone has no HEAD to reset to; pass --base <rev>")
		}
		baseRev = strings.TrimSpace(string(out))
	}
	return swarm.PullBatch(swarm.PullInput{
		Queue:   queue,
		Clone:   clone,
		Harvest: harvest,
		Batch:   batch,
		Kind:    strings.TrimSpace(kind),
		Repo:    strings.TrimSpace(repo),
		Run: func(c swarm.PullBatchCard) (int, error) {
			return runPullCard(runner, c, clone)
		},
		Clip: func(c swarm.PullBatchCard, result string) error {
			return clipPullClone(clone, baseRev, c.Label, result)
		},
		Stdout: stdout,
		Stderr: stderr,
	})
}

// pullBench is per-bench queue pull with work stealing (section 2).
func pullBench(f *flags, bench, worker, steal string, capacity int, lastSteal string, stdout, stderr io.Writer, now time.Time) int {
	f.want(bench, "bench", "the bench directory holding queue/ and taken/")
	f.want(worker, "worker", "the worker name written into taken/<worker>-<name>.card")
	if capacity < 0 {
		f.add(fmt.Sprintf("--capacity is the victim's capacity line and is 0 or more, got %d; refusing to guess", capacity))
	}
	last := time.Time{}
	if s := strings.TrimSpace(lastSteal); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			f.add(fmt.Sprintf("--last-steal wants an RFC3339 stamp such as 2026-09-18T00:00:00Z, got %q", lastSteal))
		} else {
			last = t
		}
	}
	if f.refused(stderr) {
		fmt.Fprintln(stderr, "nova-swarm pull: run: nova-swarm help")
		return 2
	}

	queue, err := swarm.QueueCards(bench)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(err))
		return 2
	}
	owned, err := swarm.OwnedCards(bench, worker)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(err))
		return 2
	}

	if len(owned) > 0 {
		writePull(stdout, "taken", bench, worker, owned[0], len(queue), len(owned))
		return 0
	}

	name, ok, err := swarm.TakeCard(bench, worker)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(err))
		return 2
	}
	if ok {
		writePull(stdout, "queue", bench, worker, name, len(queue)-1, 1)
		return 0
	}

	if victims := splitList(steal); len(victims) > 0 && swarm.MirrorDue(last, now) {
		victim, queued, found, ferr := swarm.FullestBench(victims)
		if ferr != nil {
			fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(ferr))
			return 2
		}
		if found {
			stolen, serr := swarm.Steal(victim, worker, capacity)
			if serr != nil {
				fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(serr))
				return 2
			}
			if len(stolen) > 0 {
				fmt.Fprintf(stdout, "PULL bench=%s worker=%s card=%s source=steal victim=%s queue=%d taken=%d\n",
					oneline.Field(bench), oneline.Field(worker), oneline.Field(stolen[0]),
					oneline.Field(victim), queued, len(stolen))
				return 0
			}
		}
	}

	fmt.Fprintf(stdout, "PULL bench=%s worker=%s card=- source=none queue=%d taken=%d\n",
		oneline.Field(bench), oneline.Field(worker), len(queue), len(owned))
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
	return parseTurns(string(out)), nil
}

// parseTurns extracts `TURNS <n>` from runner output.
func parseTurns(out string) int {
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

// harvestPullResult copies the card's RESULT.md out of the clone.
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
		return nil
	}
	return nil
}

// pullWantsStream reports whether any flag of the SPEC-STATE stream worker was set, which
// is how the one `pull` verb tells the two habits apart.
func pullWantsStream(f *flags) bool {
	want := false
	f.fs.Visit(func(fl *flag.Flag) {
		for _, name := range pullStreamFlags {
			if fl.Name == name {
				want = true
			}
		}
	})
	return want
}

// pullRedis is the Redis mode: reclaim a lapsed lease first, then read a new card.
func pullRedis(addr, kind, bench string, lanes []string, wait time.Duration, stdout, stderr io.Writer) int {
	q, err := redisq.Open(addr)
	if err != nil {
		return refuse(stderr, " pull", oneline.Err(err))
	}
	defer q.Close()
	ctx := context.Background()
	for _, ln := range lanes {
		name := "nova:queue:" + kind + ":" + ln
		if err := q.EnsureGroup(ctx, name); err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		card, err := q.Claim(ctx, name, bench, redisq.ConsumerLease)
		if err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		if card == nil {
			card, err = q.Pull(ctx, name, bench, wait)
			if err != nil {
				return refuse(stderr, " pull", oneline.Err(err))
			}
		}
		if card == nil {
			continue
		}
		fmt.Fprintf(stdout, "PULL stream=%s bench=%s card=%s\n",
			oneline.Field(name), oneline.Field(bench), oneline.Field(card.ID))
		// The clip: the card has landed, so the one thing safe to forget is forgotten.
		if err := q.Ack(ctx, name, card.ID); err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		return 0
	}
	fmt.Fprintf(stdout, "PULL stream=%s bench=%s none\n",
		oneline.Field("nova:queue:"+kind), oneline.Field(bench))
	return 0
}

// pullDirectory is the fallback mode: the same contract over the directory queue, taken by
// atomic rename, with a taken card older than the lease reclaimed by a rename back.
func pullDirectory(root, kind, bench string, lanes []string, stdout, stderr io.Writer) int {
	dq := &redisq.DirQueue{Root: root}
	now := time.Now().UTC()
	for _, ln := range lanes {
		name := "nova:queue:" + kind + ":" + ln
		if _, err := dq.Reclaim(name, redisq.ConsumerLease, now); err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		card, err := dq.Pull(name)
		if err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		if card == nil {
			continue
		}
		fmt.Fprintf(stdout, "PULL stream=%s bench=%s card=%s\n",
			oneline.Field(name), oneline.Field(bench), oneline.Field(card.ID))
		if err := dq.Ack(name, card.ID); err != nil {
			return refuse(stderr, " pull", oneline.Err(err))
		}
		return 0
	}
	fmt.Fprintf(stdout, "PULL stream=%s bench=%s none\n",
		oneline.Field("nova:queue:"+kind), oneline.Field(bench))
	return 0
}

// refusePull writes the one line a pull that could not run owes its caller.
func refusePull(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PULL REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

// writePull prints the one line a pull that took a card writes.
func writePull(w io.Writer, source, bench, worker, card string, queue, taken int) {
	fmt.Fprintf(w, "PULL bench=%s worker=%s card=%s source=%s queue=%d taken=%d\n",
		oneline.Field(bench), oneline.Field(worker), oneline.Field(card),
		oneline.Field(source), queue, taken)
}

// splitList reads a comma-separated flag value into its non-empty items.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
