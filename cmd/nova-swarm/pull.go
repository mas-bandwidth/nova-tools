// pull is the pull worker of docs/SPEC-STATE.md Part 2: it reads one card from the
// Redis Streams queue with XREADGROUP over the one shared `workers` group, reclaims a dead
// worker's card with XAUTOCLAIM after the lease, runs the card, and XACKs it on clip. The
// directory queue is the fallback a bench chooses at start with --dir, and the two stores
// are never both active: the mode is chosen once and every read follows it.
//
// It is also `nova-swarm pull` per-bench queues with work stealing (docs/SPEC-JOBS.md
// section 2): it lists a bench's queue/ directory and takes one card by
// rename(<name>.card, taken/<worker>-<name>.card) -- atomic within the directory, so two
// workers cannot take one card. It drains the worker's own taken/ before it reaches for
// another bench. A worker with no owned card and an empty home queue may steal from the
// fullest bench named by --steal, never below that victim's --capacity line, and only on
// the mirror's five-minute timer.
//
// BATCHING (docs/SPEC-JOBS.md section 6): pull takes up to --batch cards of one kind and
// one repo in one turn on the one kept clone named by --clone, clips after each card
// (commit, harvest its RESULT.md, reset to base), and returns the remainder to queue/ when
// a card goes past its :effort. The runner is one command per card, handed the card and the
// clone through PULL_CARD, PULL_CLONE and PULL_LABEL and reporting its turns as a trailing
// `TURNS <n>` line. The clip is git against --clone, resetting to --base or the clone's own
// HEAD when --base is absent.
//
// AFFINITY (docs/SPEC-JOBS.md section 4). A card carries a kind and a repo; pull prefers
//
// BACKPRESSURE (docs/SPEC-JOBS.md section 7): one worker takes cards from a bench's queue/
// only while the bench's capacity line admits it. It reads the four probe numbers from
// flags (never probes), computes min(cores*1.5-load1, (free_gb-25)/2, memfree_gb/2), takes
// at most line-running cards by rename into taken/, and prints one PULL line. A full bench
// takes nothing and leaves every card in queue/. An idle slot does not poll queue/: when
// the take comes back empty the slot asks the coordinator for work by an event, nova-pulse
// watch (internal/swarm SlotWait). This verb is the take; the ask is the watch.
// the card whose repo the bench already holds in a kept worktree under
// <slot>/worktrees/<owner>/<name>, so the clone is reused and cache warmth is kept. With
// no warm card it falls back to the first card and a fetch from the bench mirror. Every
// path comes from a flag; there is no default slot, queue or mirror.
//
// All five habits live on the one `pull` verb, and one run picks the shape from the flags
// it was handed: the section 6 flags (--batch/--runner/--clone) are the batch, the section 7
// probe numbers (--cores/--load1/--free-gb/--memfree-gb/--running) are the backpressured
// take, the section 4 flags (--slot/--queue/--mirror) are the warm-worktree prefer, the
// SPEC-STATE flags (--stream and --redis/--dir) are the stream worker, and everything else
// is section 2's per-bench queue pull.
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
// present means the caller asked for the stream, not one of the SPEC-JOBS pulls. --bench is
// not among them: it names the consumer there and the bench directory in section 2's pull,
// so the stream is chosen by a flag only the stream has.
var pullStreamFlags = []string{"stream", "redis", "dir", "lane", "wait"}

// cmdPull dispatches the four pull shapes by the flags the caller named.
func cmdPull(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("pull")
	stream := f.fs.String("stream", "", "")
	bench := f.fs.String("bench", "", "")
	redisAddr := f.fs.String("redis", "", "")
	dir := f.fs.String("dir", "", "")
	lane := f.fs.String("lane", "", "")
	wait := f.fs.Duration("wait", 0, "")
	slot := f.fs.String("slot", "", "")
	queue := f.fs.String("queue", "", "")
	mirror := f.fs.String("mirror", "", "")
	worker := f.fs.String("worker", "", "")
	steal := f.fs.String("steal", "", "")
	capacity := f.fs.Int("capacity", 0, "")
	lastSteal := f.fs.String("last-steal", "", "")
	// Section 7 (backpressure and idle): the four probe numbers, read, never probed.
	cores := f.fs.Int("cores", -1, "")
	load1 := f.fs.Int("load1", -1, "")
	freeGB := f.fs.Int("free-gb", -1, "")
	memFreeGB := f.fs.Int("memfree-gb", -1, "")
	running := f.fs.Int("running", 0, "")
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
	// Batching (section 6) is the shape whenever any of its own flags is present; it is
	// asked first because it shares --queue with the affinity pull of section 4.
	if *batch > 0 || strings.TrimSpace(*runner) != "" || strings.TrimSpace(*clone) != "" {
		f.want(*queue, "queue", "the directory cards wait in")
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
			Run: func(c swarm.PullCard) (int, error) {
				return runPullCard(*runner, c, *clone)
			},
			Clip: func(c swarm.PullCard, result string) error {
				return clipPullClone(*clone, baseRev, c.Label, result)
			},
			Stdout: stdout,
			Stderr: stderr,
		})
	}
	if *slot != "" || *queue != "" || *mirror != "" {
		return pullWarm(f, *slot, *queue, *mirror, stdout, stderr)
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
	// Backpressure (section 7) is the shape whenever any probe number is present; without
	// one, --bench and --worker are section 2's per-bench queue pull.
	if *cores >= 0 || *load1 >= 0 || *freeGB >= 0 || *memFreeGB >= 0 || *running != 0 {
		return pullBackpressure(f, *bench, *worker, *cores, *load1, *freeGB, *memFreeGB, *running, stdout, stderr)
	}
	return pullBench(f, *bench, *worker, *steal, *capacity, *lastSteal, stdout, stderr, now)
}

// pullWarm is PULL (slice 4 of SPEC-JOBS, section 4). A card carries a kind and a repo; pull prefers
// the card whose repo the bench already holds in a kept worktree under
// <slot>/worktrees/<owner>/<name>, so the clone is reused and cache warmth is kept. With
// no warm card it falls back to the first card and a fetch from the bench mirror. Every
// path comes from a flag; there is no default slot, queue or mirror.
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

// pullBench is section 2's per-bench queue pull.
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

	// Drain the worker's own taken/ first: a card already owned needs no queue scan
	// and no other bench.
	if len(owned) > 0 {
		writePull(stdout, "taken", bench, worker, owned[0], len(queue), len(owned))
		return 0
	}

	// Otherwise take one card from this bench's queue by the atomic rename.
	name, ok, err := swarm.TakeCard(bench, worker)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm pull: %s\n", oneline.Err(err))
		return 2
	}
	if ok {
		writePull(stdout, "queue", bench, worker, name, len(queue)-1, 1)
		return 0
	}

	// Idle: steal from the fullest bench on the mirror's five-minute timer, never
	// below that victim's capacity line.
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
func runPullCard(runner string, c swarm.PullCard, clone string) (int, error) {
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

// pullBackpressure is section 7's take: `nova-swarm pull --bench <dir> --worker <name>
// --cores <n> --load1 <n> --free-gb <n> --memfree-gb <n> [--running <n>]`.
func pullBackpressure(f *flags, bench, worker string, cores, load1, freeGB, memFreeGB, running int, stdout, stderr io.Writer) int {
	f.want(bench, "bench", "the bench root holding queue/ and taken/")
	f.want(worker, "worker", "this worker's name, written on every card it takes")
	for _, c := range []struct {
		val  int
		name string
	}{
		{cores, "cores"}, {load1, "load1"}, {freeGB, "free-gb"}, {memFreeGB, "memfree-gb"},
	} {
		if c.val < 0 {
			f.add(fmt.Sprintf("--%s is required and is 0 or more, got %d; the capacity line reads it, refusing to guess", oneline.Escape(c.name), c.val))
		}
	}
	if running < 0 {
		f.add(fmt.Sprintf("--running is 0 or more, got %d; it is the workers already on the bench", running))
	}
	if f.refused(stderr) {
		return 2
	}

	queue := filepath.Join(bench, "queue")
	taken := filepath.Join(bench, "taken")
	queued := countQueue(queue)
	line := swarm.AdmissionLine(cores, load1, freeGB, memFreeGB)
	admit := swarm.Admission(line, running, queued)
	names, err := swarm.PullQueue(queue, taken, worker, admit)
	if err != nil {
		return refuse(stderr, " pull", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintf(stdout, "PULL bench=%s line=%d running=%d queued=%d taken=%d left=%d\n",
		oneline.Field(filepath.Base(bench)), line, running, queued, len(names), queued-len(names))
	return 0
}

// countQueue is how many cards wait in a bench's queue/: the pull's own read of the depth
// that is the backpressure signal. It is nil-safe: a bench with no queue/ has none.
func countQueue(queue string) int {
	matches, _ := filepath.Glob(filepath.Join(queue, "*.card"))
	return len(matches)
}

// refusePull writes the one line a pull that could not run owes its caller.
func refusePull(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PULL REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

// writePull prints the one line a pull that took a card writes: the bench, the
// worker, the card, and where the card came from.
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
