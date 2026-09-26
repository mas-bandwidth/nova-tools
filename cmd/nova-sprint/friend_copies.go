// friend pull, done and beat (nova-tools #4233; Glenn 2026-09-26 9:03 AM
// ET: "friends are running themselves, and pull from their ready queue. Not
// that you launch friends models yourself." "Friends are not like swarms."):
// a friend is a consumer of copies like a bench (friend:<f>:cards:ready |
// working | ok | fail, dealt by the reconciler's deal duty by its advertised
// tiers exactly as a bench is), but it has no wrapper and no bench harness.
// Its own session, on any machine, runs these three verbs, each one the
// copy model's own move and nothing beside it:
//
//	friend pull --as friend:<f> [--n <k>] [--dir <d>]
//	    card work --as friend:<f> (--fill, or --n k) in one call, then each
//	    copy's brief (card.RenderCopy: the person's brief for a friend)
//	    written to <d>/<copy label>.card; one PULLED line per copy naming
//	    the path, the leg and the token, then the receipt.
//	friend done --as friend:<f> --id <copy> (--ok [--pr <repo>#<n> --head <sha>] [--done-already <sha>]
//	    | --score N/10 [--gates <g>] [--finding <text>] | --fail <why>) [--token <t>]
//	    card end --id <copy> with the same evidence, refused (NOTMINE) for a
//	    copy that is not this friend's.
//	friend beat --as friend:<f> [--host <h>] [--once]
//	    the zero-token tick: the friend's beat (host, at, load1, ncpu, cpu
//	    of the machine this session runs on: its presence and load on the
//	    consumer table) and row (the deal duty's liveness), then the leases
//	    of every copy it holds (card beat). Without --once it ticks each
//	    second until interrupted.
//
// The retired `friend serve` (#4327) dispatched a friend's copies through
// the bench wrapper on the Studio; nothing here launches a model.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/redis/go-redis/v9"
)

// friendConsumer reads --as friend:<f>; a bench or a bare name is refused
// with the one spelling named.
func friendConsumer(as string) (taskcard.Consumer, error) {
	k, err := taskcard.ParseConsumer(as)
	if err != nil || k.Kind != "friend" {
		return taskcard.Consumer{}, fmt.Errorf("--as wants friend:<f>, got %q", as)
	}
	return k, nil
}

// friendActor is the by of a friend's move: the seat (NOVA_FRIEND) when
// set, which must be the friend; else the friend's name.
func friendActor(friend string) (string, error) {
	if seat := os.Getenv(seatEnv); seat != "" && seat != friend {
		return "", fmt.Errorf("--as friend:%s is not the seat (%s=%s)", friend, seatEnv, seat)
	}
	return friend, nil
}

// friendCardsDir is where friend pull writes briefs when --dir is not given.
func friendCardsDir(friend string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("--dir is required (no home directory: %v)", err)
	}
	return filepath.Join(home, ".nova-sprint", "friend", friend, "cards"), nil
}

func runFriendPull(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend pull"
	fs := verbflag.New(verb)
	redisAddr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	n := fs.Int("n", 0, "")
	dir := fs.String("dir", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *n < 0 {
		return refuse(errOut, verb, "--n wants a positive count; omit it to fill every free slot")
	}
	k, err := friendConsumer(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	by, err := friendActor(k.Name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *dir == "" {
		if *dir, err = friendCardsDir(k.Name); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return refuse(errOut, verb, "--dir: "+err.Error())
	}
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	w, err := taskcard.Work(ctx, c, k, by, *n, *n == 0)
	if err != nil {
		if why, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(out, "FRIEND PULL REFUSED as=%s why=%s ms=%d\n", k, quoteField(why), ms())
			return 1
		}
		return refuse(errOut, verb, err.Error())
	}
	pipe := c.Pipeline()
	recs := make([]*redis.MapStringStringCmd, len(w.IDs))
	for i, id := range w.IDs {
		recs[i] = pipe.HGetAll(ctx, taskcard.Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return refuse(errOut, verb, "read copies: "+err.Error())
	}
	code := 0
	pulled := 0
	for i, id := range w.IDs {
		rec := recs[i].Val()
		body, err := card.RenderCopy(card.CopyCardFrom(id, rec))
		path := filepath.Join(*dir, card.CopyLabel(id)+".card")
		if err == nil {
			err = os.WriteFile(path, body, 0o644)
		}
		if err != nil {
			// The copy is working with this friend's token: a brief it
			// cannot get is its fail now, not a lease left to lapse.
			why := err.Error()
			fmt.Fprintf(out, "PULLED %s REFUSED why=%s\n", id, quoteField(why))
			if _, endErr := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{id}, Why: "brief: " + why, Token: w.Tokens[i], By: by}); endErr != nil {
				fmt.Fprintf(out, "PULLED %s FAIL-REFUSED why=%s\n", id, quoteField(endErr.Error()))
			}
			code = 1
			continue
		}
		pulled++
		fmt.Fprintf(out, "PULLED %s leg=%s token=%s card=%s\n", id, dash(rec["leg"]), w.Tokens[i], path)
	}
	fmt.Fprintf(out, "FRIEND PULL as=%s n=%d free=%d dir=%s ms=%d\n", k, pulled, w.Free, *dir, ms())
	return code
}

func runFriendDone(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend done"
	fs := verbflag.New(verb)
	redisAddr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	id := fs.String("id", "", "")
	ok := fs.Bool("ok", false, "")
	pr := fs.String("pr", "", "")
	head := fs.String("head", "", "")
	doneAlready := fs.String("done-already", "", "")
	score := fs.String("score", "", "")
	gates := fs.String("gates", "", "")
	finding := fs.String("finding", "", "")
	fail := fs.String("fail", "", "")
	token := fs.String("token", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	k, err := friendConsumer(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	by, err := friendActor(k.Name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *id == "" || !taskcard.IsCopy(*id) {
		return refuse(errOut, verb, "--id wants a copy id <primary>~<n>")
	}
	ways := 0
	for _, on := range []bool{*ok, *fail != "", *score != ""} {
		if on {
			ways++
		}
	}
	if ways != 1 && !(*ok && *score != "") {
		return refuse(errOut, verb, "wants exactly one of --ok, --score <N>/10 and --fail <why>")
	}
	if *pr != "" && *head == "" {
		return refuse(errOut, verb, "--pr wants --head <sha>")
	}
	r := taskcard.EndRequest{IDs: []string{*id}, OK: *fail == "", Why: *fail, Head: *head, DoneAlready: *doneAlready,
		Gates: *gates, Finding: *finding, Reader: k.Name, Token: *token, By: by}
	if *pr != "" {
		repo, n, ok := strings.Cut(*pr, "#")
		if !ok || repo == "" || n == "" {
			return refuse(errOut, verb, "--pr wants <repo>#<n>")
		}
		r.Repo, r.PR = repo, n
	}
	if *score != "" {
		s, err := strconv.Atoi(strings.TrimSuffix(*score, "/10"))
		if err != nil || s < 1 || s > 10 {
			return refuse(errOut, verb, "--score wants N/10, N 1-10")
		}
		r.Score = s
	}
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	// The copy must be this friend's: card end fences on the token when
	// one is given, and a friend ending another consumer's copy by id
	// alone would be a silent theft.
	holder, err := c.HGet(ctx, taskcard.Key(*id), "consumer").Result()
	switch {
	case err == redis.Nil || (err == nil && holder == ""):
		fmt.Fprintf(out, "FRIEND DONE REFUSED id=%s why=%s ms=%d\n", *id, quoteField("NOCOPY task:"+*id), ms())
		return 1
	case err != nil:
		return refuse(errOut, verb, err.Error())
	case holder != k.String():
		fmt.Fprintf(out, "FRIEND DONE REFUSED id=%s why=%s ms=%d\n", *id, quoteField("NOTMINE task:"+*id+" is "+holder+"'s copy, not "+k.String()+"'s"), ms())
		return 1
	}
	e, err := taskcard.End(ctx, c, r)
	if err != nil {
		if why, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(out, "FRIEND DONE REFUSED id=%s why=%s ms=%d\n", *id, quoteField(why), ms())
			return moveRefusedCode(why)
		}
		return refuse(errOut, verb, err.Error())
	}
	printEnded(out, e)
	fmt.Fprintf(out, "FRIEND DONE as=%s n=%d ms=%d\n", k, len(e), ms())
	return 0
}

// friendBeatOnce is one tick of friend beat: the beat and row, then the
// leases of the copies it found working. lease_until is 0 with none.
func friendBeatOnce(ctx context.Context, st *store.Store, k taskcard.Consumer, host string, now time.Time) (life.FriendBeatResult, int64, error) {
	res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: k.Name, Host: host,
		Load1: life.Load1Now(), NCPU: runtime.NumCPU(), CPU: life.CPUBusyNow(), At: now})
	if err != nil {
		return res, 0, err
	}
	if len(res.Working) == 0 {
		return res, 0, nil
	}
	until, err := taskcard.BeatCopies(ctx, st.Client(), k, res.Working...)
	return res, until, err
}

func runFriendBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend beat"
	fs := verbflag.New(verb)
	redisAddr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	host := fs.String("host", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	k, err := friendConsumer(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if _, err := friendActor(k.Name); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *host == "" {
		if *host, err = os.Hostname(); err != nil {
			return refuse(errOut, verb, "--host is required (no hostname: "+err.Error()+")")
		}
	}
	st, err := store.OpenSingle(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	res, until, err := friendBeatOnce(ctx, st, k, *host, time.Now())
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "FRIEND BEAT as=%s host=%s working=%d lease_until=%d at=%d\n", k, *host, len(res.Working), until, res.AtMS)
	if *once {
		return 0
	}
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var backoff time.Duration
	var next time.Time
	for {
		select {
		case <-signalCtx.Done():
			return 0
		case now := <-ticker.C:
			if now.Before(next) {
				continue
			}
			if _, _, err := friendBeatOnce(signalCtx, st, k, *host, now); err != nil {
				if signalCtx.Err() != nil {
					return 0
				}
				backoff = benchBeatBackoff(backoff, life.BeatInterval)
				next = now.Add(backoff)
				fmt.Fprintf(errOut, "friend %s beat: %v; retry in %s\n", k.Name, err, backoff)
				continue
			}
			backoff, next = 0, time.Time{}
		}
	}
}
