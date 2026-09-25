package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "quack",
		Summary: "--benches a,b [--tiers pro,flash]: push one probe card per bench x tier into a fresh sprint and print the per-stage timing table",
		Run:     runQuack,
	})
}

// quackNow is the probe's clock; a test may replace it.
var quackNow = time.Now

// runQuack is nova-tools#3648, the per-bench end-to-end probe that replaces
// the hand quack test (a bash generator, one card push per card, timings
// copied by hand): it opens a fresh sprint, pushes one card per bench x tier
// in one batch (card.QuackCard, pinned by BENCH:, tier by ROUTE:), reads the
// card records every tick (card.ReadQuack, three pipelines at most) until
// every card has its first read or has failed, or --timeout, and prints one
// row per card with the seconds from T0 to push, deal, launch, end, harvest
// and first read, judged against the bars in cfg:quack (card.DefaultQuackBars
// where the hash sets none). Exit 0 every row PASS, 1 any FAIL or a refusal
// with the remedy named, 2 usage.
func runQuack(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("quack", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	benches := fs.String("benches", "", "")
	tiers := fs.String("tiers", "pro,flash", "")
	name := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	repo := fs.String("repo", "mas-bandwidth/nova-tools", "")
	base := fs.String("base", "dev", "")
	baseSHA := fs.String("base-sha", "", "")
	stream := fs.String("stream", "swarm: cards", "")
	timeout := fs.Duration("timeout", 20*time.Minute, "")
	tick := fs.Duration("tick", 2*time.Second, "")
	const want = "wants --benches <a,b> [--tiers pro,flash] [--sprint <S>] [--base dev] [--base-sha <sha40>] [--timeout 20m] [--tick 2s] and --redis <addr> (or NOVA_SPRINT_REDIS)"
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *addr == "" || *timeout <= 0 || *tick <= 0 {
		return refuse(stderr, "quack", want)
	}
	benchList, tierList := splitList(*benches), splitList(*tiers)
	if len(benchList) == 0 || len(tierList) == 0 {
		return refuse(stderr, "quack", want)
	}
	if *name == "" {
		*name = "quack-" + quackNow().UTC().Format("0102-150405")
	}
	if !sprint.ValidName(*name) {
		return refuse(stderr, "quack", "--sprint must match [a-z0-9-]{1,40}")
	}
	if *baseSHA == "" {
		sha, err := card.MirrorBranchSHA(*repo, *base)
		if err != nil {
			fmt.Fprintf(stdout, "REFUSED quack sprint=%s why=%s remedy=%s\n", *name, oneline.Field(err.Error()), oneline.Field("pass --base-sha <sha40> or refresh the mirror"))
			return 1
		}
		*baseSHA = sha
	}
	var files []card.CardFile
	var labels []string
	for _, b := range benchList {
		for _, tier := range tierList {
			f, err := card.QuackCard(card.QuackInput{Sprint: *name, Bench: b, Tier: tier, Repo: *repo, Base: *base, BaseSHA: *baseSHA, Stream: *stream})
			if err != nil {
				return refuse(stderr, "quack", err.Error())
			}
			files = append(files, f)
			labels = append(labels, card.QuackLabel(b, tier))
		}
	}

	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(openCtx, *addr)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED quack sprint=%s redis=down remedy=%s\n", *name, oneline.Field("check --redis or NOVA_SPRINT_REDIS"))
		return 1
	}
	defer st.Close()
	client := st.Client()
	bars, barSource, err := card.ReadQuackBars(openCtx, client)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED quack sprint=%s why=%s remedy=%s\n", *name, oneline.Field(err.Error()), oneline.Field("fix or delete "+card.QuackBarsKey))
		return 1
	}
	// Fresh means no record of the sprint at all: a probe in a reused sprint
	// would time the old cards (run #4 reused run #3's labels).
	if n, err := client.Exists(openCtx, "s:"+*name, "s:"+*name+":pool").Result(); err != nil || n > 0 {
		fmt.Fprintf(stdout, "REFUSED quack sprint=%s why=%s remedy=%s\n", *name, "sprint-not-fresh", oneline.Field("name a new --sprint"))
		return 1
	}
	now := quackNow()
	if line, err := sprint.Begin(openCtx, st, *name, "", "", now); err != nil || line != "" {
		return quackOpenRefused(stdout, *name, line, err)
	}
	if line, err := sprint.Finish(openCtx, st, *name, "", "", now, nil); err != nil || line != "" {
		return quackOpenRefused(stdout, *name, line, err)
	}
	for _, res := range card.PushBatch(openCtx, client, *name, files, card.PushOptions{}) {
		if res.Code != 0 {
			fmt.Fprintf(stdout, "REFUSED quack sprint=%s push=%d why=%s\n", *name, res.Code, oneline.Field(strings.TrimSpace(res.Stderr)))
			return 1
		}
	}
	fmt.Fprintf(stdout, "QUACK PUSHED sprint=%s cards=%d base-sha=%s bars=%s timeout=%s\n", *name, len(files), (*baseSHA)[:12], barSource, *timeout)

	deadline := quackNow().Add(*timeout)
	var rows []card.QuackProgress
	timedOut := false
	for {
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
		rows, err = card.ReadQuack(readCtx, client, *name, labels)
		cancelRead()
		if err != nil {
			fmt.Fprintf(stdout, "REFUSED quack sprint=%s why=%s\n", *name, oneline.Field(err.Error()))
			return 1
		}
		all := true
		for _, r := range rows {
			all = all && r.Done()
		}
		if all {
			break
		}
		if !quackNow().Before(deadline) {
			timedOut = true
			break
		}
		select {
		case <-ctx.Done():
			timedOut = true
		case <-time.After(*tick):
		}
		if timedOut {
			break
		}
	}
	t0 := int64(0)
	for _, r := range rows {
		if at := r.At["push"]; at > 0 && (t0 == 0 || at < t0) {
			t0 = at
		}
	}
	pass := 0
	for i, r := range rows {
		b, tier := benchList[i/len(tierList)], tierList[i%len(tierList)]
		cols, ok, verdict := card.QuackRow(r, t0, bars, timedOut)
		if ok {
			pass++
		}
		fmt.Fprintf(stdout, "%s %s %s pr=%s %s\n", b, tier, cols, orDash(r.PR), verdict)
	}
	fmt.Fprintf(stdout, "QUACK sprint=%s rows=%d pass=%d fail=%d timed_out=%t\n", *name, len(rows), pass, len(rows)-pass, timedOut)
	if pass != len(rows) {
		return 1
	}
	return 0
}

func quackOpenRefused(stdout io.Writer, name, line string, err error) int {
	why := line
	if err != nil {
		why = err.Error()
	}
	fmt.Fprintf(stdout, "REFUSED quack sprint=%s open=%s\n", name, oneline.Field(why))
	return 1
}

func splitList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
