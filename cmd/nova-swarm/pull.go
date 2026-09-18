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
// present means the caller asked for the stream, not the warm-worktree prefer.
var pullStreamFlags = []string{"stream", "bench", "redis", "dir", "lane", "wait"}

// cmdPull reads at most one card for one bench and prints one line. The mode is chosen at
// start from the flags: --redis is the instance and --dir is the directory fallback. With
// the SPEC-JOBS flags it instead prefers the card whose repo the bench already holds.
func cmdPull(args []string, stdout, stderr io.Writer) int {
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
