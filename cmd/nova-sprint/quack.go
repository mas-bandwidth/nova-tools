// The quack verbs (nova-tools#4307): a probe run's cards, cut and started as
// two verbs instead of the morning's hand steps (a bash loop of a hundred
// task push calls from a template, the pit stop set and lifted by hand, the
// base sha read by hand).
//
//	nova-sprint quack cut --n <N> --repo <owner/name> --stream <s> --sprint <S>
//	    [--tiers flash,pro] [--base dev] [--base-sha <sha40>] [--ref <owner/name#n>] [--actor <a>] [--redis <addr>]
//	nova-sprint quack run --sprint <S> [--slots hetzner=8,hulk=16,...] [--actor <a>] [--redis <addr>]
//
// quack cut sets the sprint's pit stop (why: cutting N quack cards), pushes
// N primaries quack-001..quack-NNN into the stream's waiting set (the
// template card.QuackIssue rendered per card, tiers round-robin over
// --tiers, one ns_tcard_push through taskcard.Push per card, never a child
// process) and prints one CUT line. An id that already exists is SKIPPED
// with a line and the cut goes on. The stop stays set and the CUT line says
// so: quack run lifts it. The base sha is --base-sha, else the tip of --base
// in this host's mirror (~/nova-bench/mirror/<name>.git); with neither the
// verb refuses and names the remedy, before anything is written.
//
// quack run sets each --slots bench's slots through the capacity path
// (capacity.SetBenchWith, the bench's recorded machine) and lifts the pit
// stop, one receipt line each, then one QUACK RUN line.
//
// Exit 0 done, 1 refused with the remedy named (or a bench refused), 2 usage.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "quack",
		Summary: "cut --n <N> --repo <owner/name> --stream <s> --sprint <S> [--tiers flash,pro] | run --sprint <S> [--slots b=n,...]: cut a probe run's primaries under the pit stop, then lift it with the benches' slots (#4307)",
		Run:     runQuack,
	})
}

// quackNow is the verbs' clock for the ms= field; a test may replace it.
var quackNow = time.Now

func runQuack(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "quack", "want cut or run")
	}
	switch args[0] {
	case "cut":
		return runQuackCut(ctx, args[1:], out, errOut)
	case "run":
		return runQuackRun(ctx, args[1:], out, errOut)
	}
	return refuse(errOut, "quack", fmt.Sprintf("unknown subverb %s; want cut or run", args[0]))
}

// quackActor is --actor, else the seat (NOVA_FRIEND).
func quackActor(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(seatEnv)
}

func runQuackCut(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "quack cut"
	fs := taskFlags(verb)
	n := fs.Int("n", 0, "")
	repo := fs.String("repo", "", "")
	stream := fs.String("stream", "", "")
	name := fs.String("sprint", "", "")
	tiers := fs.String("tiers", cardhdr.DefaultTiers, "")
	base := fs.String("base", "dev", "")
	baseSHA := fs.String("base-sha", "", "")
	ref := fs.String("ref", "", "")
	actor := fs.String("actor", "", "")
	addr := fs.String("redis", redisDefault(), "")
	const want = "wants --n <N> --repo <owner/name> --stream <s> --sprint <S> [--tiers flash,pro] [--base dev] [--base-sha <sha40>] [--ref <owner/name#n>] [--actor <a>] [--redis <addr>]"
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not "+strconv.Quote(fs.Arg(0)))
	}
	if *n <= 0 || !landRepoOK(*repo) || strings.TrimSpace(*stream) == "" || *name == "" || *base == "" {
		return refuse(errOut, verb, want)
	}
	if !sprint.ValidName(*name) {
		return refuse(errOut, verb, "--sprint must match [a-z0-9-]{1,40}")
	}
	tierList := splitList(*tiers)
	if len(tierList) == 0 {
		return refuse(errOut, verb, "--tiers names at least one of "+cardhdr.RouteList)
	}
	for _, t := range tierList {
		if !cardhdr.IsRoute(t) {
			return refuse(errOut, verb, fmt.Sprintf("tier %q is not %s", t, cardhdr.RouteList))
		}
	}
	who := quackActor(*actor)
	if who == "" {
		return refuse(errOut, verb, "--actor is required when "+seatEnv+" is empty")
	}
	raddr := taskAddr(*addr)
	if raddr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	S := oneline.Field(*name)
	if *baseSHA == "" {
		sha, err := card.MirrorBranchSHA(*repo, *base)
		if err != nil {
			fmt.Fprintf(out, "CUT REFUSED sprint=%s repo=%s why=%s remedy=%s\n", S, *repo, oneline.Field(err.Error()),
				oneline.Quote("pass --base-sha <sha40>, or nova-sprint mirror refresh so "+card.MirrorPath(*repo)+" holds "+*base))
			return 1
		}
		*baseSHA = sha
	}
	// Every card is rendered before Redis is touched: a bad input refuses
	// with nothing written.
	cuts, err := quackCuts(*n, *name, *stream, *repo, *base, *baseSHA, tierList)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}

	st, err := store.Open(ctx, raddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	return quackCutOn(ctx, st.Client(), quackPlan{name: *name, stream: *stream, repo: *repo, ref: *ref, who: who,
		baseSHA: *baseSHA, tiers: tierList, cuts: cuts}, out, errOut)
}

// quackCut is one rendered card of a quack cut.
type quackCut struct {
	id, tier string
	spec     taskcard.Spec
}

// quackCuts renders the n cards quack-001.. from the template, tiers
// round-robin, before Redis is touched.
func quackCuts(n int, name, stream, repo, base, baseSHA string, tiers []string) ([]quackCut, error) {
	cuts := make([]quackCut, 0, n)
	for i := 1; i <= n; i++ {
		id, tier := card.QuackID(i), tiers[(i-1)%len(tiers)]
		text, err := card.QuackIssue(card.QuackInput{Sprint: name, Stream: stream, ID: id, Tier: tier,
			Repo: repo, Base: base, BaseSHA: baseSHA})
		if err != nil {
			return nil, err
		}
		cuts = append(cuts, quackCut{id: id, tier: tier, spec: taskcard.ParseIssue(text)})
	}
	return cuts, nil
}

// quackPlan is a quack cut's parsed flags and rendered cards, before Redis.
type quackPlan struct {
	name, stream, repo, ref, who, baseSHA string
	tiers                                 []string
	cuts                                  []quackCut
}

// quackCutOn is quack cut's store half on c. The stream's work order
// (nova-tools #4322): a seat that cannot write it is refused before the
// stop or any card is written (orderGrant, as task push, task card push,
// card push and card cut --from), and after the pushes the stream's order
// is written once (pushReorder: its order= fields on the CUT line).
func quackCutOn(ctx context.Context, cl redis.Cmdable, p quackPlan, out, errOut io.Writer) int {
	const verb = "quack cut"
	n, who, S := len(p.cuts), p.who, oneline.Field(p.name)
	start := quackNow()
	if why := orderGrant(ctx, cl, p.stream); why != "" {
		fmt.Fprintf(out, "CUT REFUSED sprint=%s stream=%s why=%s\n", S, oneline.Field(p.stream), quoteField(why))
		return 1
	}

	// The stop first, so nothing deals a card before the whole run is in.
	stop, err := pitstop.Set(ctx, cl, p.name, who, fmt.Sprintf("quack cut: cutting %d quack cards into %s", n, p.stream), false, "quack-cut-"+p.name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pit := "set"
	switch stop.Outcome {
	case pitstop.Unknown:
		fmt.Fprintf(out, "CUT REFUSED sprint=%s why=sprint-unknown remedy=%s\n", S, oneline.Quote("nova-sprint sprint open --sprint "+p.name+" first"))
		return 1
	case pitstop.Exists:
		pit = "held" // a stop already there is the same stop: nothing deals until quack run
	}

	pushed, skipped, refused := 0, 0, 0
	for i := range p.cuts {
		c := &p.cuts[i]
		_, err := taskcard.Push(ctx, cl, taskcard.PushRequest{ID: c.id, Where: "waiting", Stream: p.stream, Sprint: p.name,
			Ref: p.ref, Title: card.QuackTitle(c.id, c.tier), Repo: p.repo, By: who, Why: "quack cut", Spec: &c.spec})
		if err == nil {
			pushed++
			continue
		}
		why, ok := taskcard.IsRefused(err)
		if !ok {
			return refuse(errOut, verb, err.Error())
		}
		if strings.HasPrefix(why, "EXISTS ") {
			skipped++
			fmt.Fprintf(out, "SKIPPED id=%s why=exists\n", c.id)
			continue
		}
		refused++
		fmt.Fprintf(out, "REFUSED id=%s why=%s\n", c.id, quoteField(why))
	}
	order := pushReorder(ctx, cl, p.stream, "", who, out)
	fmt.Fprintf(out, "CUT n=%d stream=%s sprint=%s repo=%s tiers=%s pushed=%d skipped=%d refused=%d base-sha=%s pitstop=%s %s lift=%s ms=%d\n",
		n, oneline.Field(p.stream), S, p.repo, strings.Join(p.tiers, ","), pushed, skipped, refused, p.baseSHA[:12], pit, order,
		oneline.Quote("nova-sprint quack run --sprint "+p.name), quackNow().Sub(start).Milliseconds())
	if refused > 0 {
		return 1
	}
	return 0
}

func runQuackRun(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "quack run"
	fs := taskFlags(verb)
	name := fs.String("sprint", "", "")
	slots := fs.String("slots", "", "")
	actor := fs.String("actor", "", "")
	addr := fs.String("redis", redisDefault(), "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not "+strconv.Quote(fs.Arg(0)))
	}
	if *name == "" {
		return refuse(errOut, verb, "wants --sprint <S> [--slots <bench>=<n>,...] [--actor <a>] [--redis <addr>]")
	}
	benches, err := parseSlots(*slots)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	who := quackActor(*actor)
	if who == "" {
		return refuse(errOut, verb, "--actor is required when "+seatEnv+" is empty")
	}
	raddr := taskAddr(*addr)
	if raddr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	start := quackNow()
	st, err := store.Open(ctx, raddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	cl := st.Client()
	S := oneline.Field(*name)

	refused := 0
	for _, b := range benches {
		machine, err := existingMachine(ctx, st, capacity.KindBench, b.name)
		if err != nil {
			refused++
			fmt.Fprintf(out, "SLOTS REFUSED bench=%s slots=%d why=%s\n", oneline.Field(b.name), b.slots,
				oneline.Field("read "+capacity.DesiredKey(capacity.KindBench, b.name)+" machine: "+err.Error()))
			continue
		}
		if machine == "" {
			refused++
			fmt.Fprintf(out, "SLOTS REFUSED bench=%s slots=%d why=no-machine remedy=%s\n", oneline.Field(b.name), b.slots,
				oneline.Quote("nova-sprint capacity bench --as "+who+" --machine <m> "+b.name+" "+strconv.Itoa(b.slots)))
			continue
		}
		r, err := capacity.SetBenchWith(ctx, st, b.name, machine, b.slots, who, "quack-run-"+*name+"-"+b.name, capacity.DesiredOpts{})
		if err != nil {
			refused++
			fmt.Fprintf(out, "SLOTS REFUSED bench=%s slots=%d why=%s\n", oneline.Field(b.name), b.slots, oneline.Field(err.Error()))
			continue
		}
		status := r.Status
		if status == "" {
			status = "SET"
		}
		fmt.Fprintf(out, "SLOTS %s bench=%s machine=%s slots=%d desired=%d/%d\n", status, oneline.Field(b.name), oneline.Field(machine), r.Slots, r.Sum, r.Ceiling)
	}

	lifted := "none"
	r, err := pitstop.Clear(ctx, cl, *name, who, "quack-run-"+*name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	switch r.Outcome {
	case pitstop.None:
		fmt.Fprintf(out, "PITSTOP NONE sprint=%s\n", S)
	default:
		lifted = "lifted"
		fmt.Fprintf(out, "PITSTOP CLEAR sprint=%s by=%s at=%d was_by=%s was_why=%s\n", S, oneline.Field(who), r.At,
			oneline.Field(r.Prior.By), oneline.Quote(r.Prior.Why))
	}
	fmt.Fprintf(out, "QUACK RUN sprint=%s benches=%d refused=%d pitstop=%s ms=%d\n", S, len(benches)-refused, refused, lifted, quackNow().Sub(start).Milliseconds())
	if refused > 0 {
		return 1
	}
	return 0
}

// benchSlots is one --slots entry.
type benchSlots struct {
	name  string
	slots int
}

// parseSlots reads --slots <bench>=<n>,...: names once each, in name order.
func parseSlots(s string) ([]benchSlots, error) {
	seen := map[string]bool{}
	var out []benchSlots
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		name, v, ok := strings.Cut(e, "=")
		name = strings.TrimSpace(name)
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if !ok || name == "" || strings.ContainsAny(name, " \t") || err != nil || n < 0 {
			return nil, fmt.Errorf("--slots wants <bench>=<n>,... with n a nonnegative integer, not %q", e)
		}
		if seen[name] {
			return nil, fmt.Errorf("--slots names %s twice", name)
		}
		seen[name] = true
		out = append(out, benchSlots{name: name, slots: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
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
