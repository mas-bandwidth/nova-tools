// pull is the pull worker of docs/SPEC-STATE.md Part 2: it reads one card from the
// Redis Streams queue with XREADGROUP over the one shared `workers` group, reclaims a dead
// worker's card with XAUTOCLAIM after the lease, runs the card, and XACKs it on clip. The
// directory queue is the fallback a bench chooses at start with --dir, and the two stores
// are never both active: the mode is chosen once and every read follows it.
//
// PULL (slice 4 of SPEC-JOBS, section 4). A card carries a kind and a repo; pull prefers
//
// It is also `nova-swarm pull` per-bench queues with work stealing (docs/SPEC-JOBS.md
// section 2): it lists a bench's queue/ directory and takes one card by
// rename(<name>.card, taken/<worker>-<name>.card) -- atomic within the directory, so two
// workers cannot take one card. It drains the worker's own taken/ before it reaches for
// another bench. A worker with no owned card and an empty home queue may steal from the
// fullest bench named by --steal, never below that victim's --capacity line, and only on
// the mirror's five-minute timer.
//
// the card whose repo the bench already holds in a kept worktree under
// <slot>/worktrees/<owner>/<name>, so the clone is reused and cache warmth is kept. With
// no warm card it falls back to the first card and a fetch from the bench mirror. Every
// path comes from a flag; there is no default slot, queue or mirror.
//
// Both habits live on the one `pull` verb: the SPEC-STATE flags (--stream/--bench and
// --redis/--dir) select the stream worker, and the SPEC-JOBS flags (--slot/--queue/--mirror)
// select the warm-worktree prefer.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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

// cmdPull dispatches the three pull shapes by the flags the caller named: a slot, a queue
// or a mirror is section 4's affinity pull; a stream flag is SPEC-STATE's stream worker;
// and everything else is section 2's per-bench queue pull.
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
	if !f.parse(args, stderr) {
		return 2
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
