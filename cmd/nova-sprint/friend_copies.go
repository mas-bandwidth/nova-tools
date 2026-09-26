// friend pull, done and beat (nova-tools #4233; Glenn 2026-09-26 9:03 AM
// ET: "friends are running themselves, and pull from their ready queue. Not
// that you launch friends models yourself." "Friends are not like swarms."):
// a friend is a consumer of copies like a bench (friend:<f>:cards:ready |
// working | ok | fail, dealt by the reconciler's deal duty by its advertised
// tiers exactly as a bench is), but it has no wrapper and no bench harness.
// Its own session, on any machine, runs these three verbs, each one the
// copy model's own move and nothing beside it:
//
//	friend pull --as friend:<f> [--n <k>] [--dir <d>] [--model <m>] [--harness <h>] [--child <id>]
//	    card work --as friend:<f> (--fill, or --n k) in one call, then who
//	    works them (model, harness, child: taskcard.Who) onto each copy and
//	    each copy's brief (card.RenderCopy: the person's brief for a friend)
//	    written to <d>/<copy label>.card; one PULLED line per copy naming
//	    the path, the leg and the token, then the friend's beat loop
//	    (ensureFriendBeat: one BEATLOOP line) and the receipt.
//	friend done --as friend:<f> --id <copy> (--ok [--pr <repo>#<n> --head <sha> [--branch <b>]] [--done-already <sha>]
//	    | --score N/10 [--gates <g>] [--finding <text>] | --fail <why>) [--token <t>]
//	    card end --id <copy> with the same evidence, refused (NOTMINE) for a
//	    copy that is not this friend's. --ok --pr first records the PR
//	    (card.RecordPR, what the wrapper's harvest writes) so the end is
//	    not refused NOPR; --branch is the PR's branch when it is not the
//	    brief's (the copy's branch, else the wrapper's name for it).
//	friend beat --as friend:<f> [--host <h>] [--once | --loop [--lease <token>]]
//	    the zero-token tick, one round trip: the friend's beat (host, at,
//	    load1, ncpu, cpu of the machine this session runs on: its status
//	    and load on the consumer table, the deal duty's liveness) and the
//	    lease of every copy it holds (ns_cm_beat over its working set in
//	    the same pipeline). Without --once it ticks each second until
//	    interrupted; a refused tick backs off and says why. --loop is the
//	    friend's one beat loop (life.BeatLoop): it holds
//	    friend:<f>:beatloop (--lease: the token the starting verb claimed
//	    it with) and exits when the friend holds no working copy for two
//	    ticks; friend pull and card work --as friend:<f> start it in its
//	    own session (the refresh start) when no loop holds the lease.
//
// The retired `friend serve` (#4327) dispatched a friend's copies through
// the bench wrapper on the Studio; nothing here launches a model.
package main

import (
	"context"
	"errors"
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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
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
	redisAddr := fs.String("redis", redisDefault(), "")
	as := fs.String("as", "", "")
	n := fs.Int("n", 0, "")
	dir := fs.String("dir", "", "")
	model := fs.String("model", "", "")
	harness := fs.String("harness", "", "")
	child := fs.String("child", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	who := taskcard.Who{Model: *model, Harness: *harness, Child: *child}
	if err := who.Check(); err != nil {
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
	// who works the copies goes on their records in the move itself
	// (ns_cm_work), so each brief read below carries its WORKER line
	w, err := taskcard.WorkAs(ctx, c, k, by, *n, *n == 0, who)
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
	if !printFriendBeat(ctx, c, k, redisArg(*redisAddr), out) {
		code = 1
	}
	fmt.Fprintf(out, "FRIEND PULL as=%s n=%d free=%d dir=%s ms=%d\n", k, pulled, w.Free, *dir, ms())
	return code
}

// redisArg is the --redis a started loop is given: the verb's own when it
// is not the default, so the loop reads the store the verb wrote; else
// none, and the loop resolves the same default from the same environment.
func redisArg(flag string) string {
	if flag == redisDefault() {
		return ""
	}
	return flag
}

// loopStarter is how a verb starts a friend's beat loop and judges the
// holder of its lease; the package's tests replace friendLoop (their binary
// is not nova-sprint).
type loopStarter struct {
	// start runs argv in its own session, its stdout and stderr appended
	// to log, and returns its pid.
	start func(argv []string, log string) (int, error)
	// alive says whether pid is a live process on this host.
	alive func(pid int) bool
	// log is the file friend f's loop writes to.
	log func(friend string) (string, error)
}

var friendLoop = loopStarter{start: startOwnSessionLog, alive: pidAlive, log: friendBeatLog}

// pidAlive is kill(pid, 0): the process exists (EPERM: it does, another
// user's).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// friendBeatLog is friend f's beat loop log, in the seat's state dir beside
// friend pull's briefs (friendCardsDir): ~/.nova-sprint/friend/<f>/beatloop.log.
func friendBeatLog(friend string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory for the beat loop's log: %v", err)
	}
	return filepath.Join(home, ".nova-sprint", "friend", friend, "beatloop.log"), nil
}

// printFriendBeat is ensureFriendBeat's one BEATLOOP line on out, after the
// verb's move; false when it was refused.
func printFriendBeat(ctx context.Context, c redis.Cmdable, k taskcard.Consumer, redisFlag string, out io.Writer) bool {
	line, err := ensureFriendBeat(ctx, c, k, redisFlag, friendLoop, time.Now())
	if err != nil {
		fmt.Fprintf(out, "BEATLOOP REFUSED as=%s why=%s\n", k, quoteField(err.Error()))
		return false
	}
	if line != "" {
		fmt.Fprintln(out, line)
	}
	return true
}

// ensureFriendBeat keeps a friend that holds working copies beating: when
// k holds any copy (<primary>~<n>) in working and no live loop holds
// friend:<f>:beatloop,
// it claims the lease (ns_friend_loop_claim) and starts `nova-sprint friend
// beat --as friend:<f> --loop --lease <token>` through s.start, its output
// to s.log's file, and writes the started pid onto the lease; a start that
// fails releases the claim and is the error. A lease held from this host by
// a pid that is gone (kill -9 leaves it for life.BeatLoopLease) is taken
// over. The line says what it found: started (pid, log), restarted (pid,
// log, dead=<the gone pid>) or running (the holder's pid and host); "" when
// k holds none. The verb calls it after its move, so a loop that saw no
// copy and let go of the lease is replaced (life.BeatLoop.Step).
func ensureFriendBeat(ctx context.Context, c redis.Cmdable, k taskcard.Consumer, redisFlag string, s loopStarter, now time.Time) (string, error) {
	if k.Kind != "friend" {
		return "", nil
	}
	// the loop beats copies (ns_cm_beat); a friend-queue task in the same
	// set is renewed by task beat, and alone it starts no loop
	members, err := c.ZRange(ctx, k.Key("working"), 0, -1).Result()
	if err != nil {
		return "", fmt.Errorf("zrange %s: %w", k.Key("working"), err)
	}
	n := 0
	for _, id := range members {
		if taskcard.IsCopy(id) {
			n++
		}
	}
	if n == 0 {
		return "", nil
	}
	host, _ := os.Hostname()
	me := life.LoopHolder{Token: fmt.Sprintf("%s:%d:%d", host, os.Getpid(), now.UnixNano()), Host: host}
	took, held, err := life.ClaimBeatLoop(ctx, c, k.Name, me, "")
	if err != nil {
		return "", err
	}
	dead := 0
	if !took && held.Host == host && held.PID > 0 && !s.alive(held.PID) {
		dead = held.PID
		if took, held, err = life.ClaimBeatLoop(ctx, c, k.Name, me, held.Token); err != nil {
			return "", err
		}
	}
	if !took {
		return fmt.Sprintf("BEATLOOP as=%s running pid=%d host=%s working=%d", k, held.PID, dash(held.Host), n), nil
	}
	log, err := s.log(k.Name)
	if err == nil {
		var exe string
		if exe, err = os.Executable(); err == nil {
			argv := []string{exe, "friend", "beat", "--as", k.String(), "--loop", "--lease", me.Token}
			if redisFlag != "" {
				argv = append(argv, "--redis", redisFlag)
			}
			if me.PID, err = s.start(argv, log); err == nil {
				// the lease names the loop's pid from its start, so the next
				// verb can judge it before the loop's own first renew
				if _, err := life.RenewBeatLoop(ctx, c, k.Name, me); err != nil {
					return "", fmt.Errorf("loop started pid=%d: %w", me.PID, err)
				}
				if dead > 0 {
					return fmt.Sprintf("BEATLOOP as=%s restarted pid=%d working=%d log=%s dead=%d", k, me.PID, n, log, dead), nil
				}
				return fmt.Sprintf("BEATLOOP as=%s started pid=%d working=%d log=%s", k, me.PID, n, log), nil
			}
		}
	}
	if relErr := life.ReleaseBeatLoop(ctx, c, k.Name, me.Token); relErr != nil {
		return "", fmt.Errorf("start beat loop: %v; %v", err, relErr)
	}
	return "", fmt.Errorf("start beat loop: %w", err)
}

func runFriendDone(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend done"
	fs := verbflag.New(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	as := fs.String("as", "", "")
	id := fs.String("id", "", "")
	ok := fs.Bool("ok", false, "")
	pr := fs.String("pr", "", "")
	head := fs.String("head", "", "")
	doneAlready := fs.String("done-already", "", "")
	branch := fs.String("branch", "", "")
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
	if *pr != "" && !*ok {
		return refuse(errOut, verb, "--pr goes with --ok")
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
	rec, err := c.HGetAll(ctx, taskcard.Key(*id)).Result()
	holder := rec["consumer"]
	switch {
	case err != nil && !errors.Is(err, redis.Nil):
		return refuse(errOut, verb, err.Error())
	case holder == "":
		fmt.Fprintf(out, "FRIEND DONE REFUSED id=%s why=%s ms=%d\n", *id, quoteField("NOCOPY task:"+*id), ms())
		return 1
	case holder != k.String():
		fmt.Fprintf(out, "FRIEND DONE REFUSED id=%s why=%s ms=%d\n", *id, quoteField("NOTMINE task:"+*id+" is "+holder+"'s copy, not "+k.String()+"'s"), ms())
		return 1
	}
	if r.PR != "" {
		// The PR the friend opened is recorded before the end, as the
		// wrapper's harvest records the bench's: card end refuses an ok
		// whose PR record is missing (NOPR) or at another head.
		n, err := strconv.Atoi(r.PR)
		if err != nil || n <= 0 {
			return refuse(errOut, verb, "--pr wants <repo>#<n>, n a PR number")
		}
		cc := card.CopyCardFrom(*id, rec)
		b := *branch
		if b == "" {
			b = strings.TrimSpace(cc.Branch)
		}
		if b == "" {
			cn, _ := card.CopyNumber(*id)
			b = card.WrapperBranch(card.CopySprint, card.CopyCardLabel(*id), cn)
		}
		if err := card.RecordPR(ctx, c, nil, harvestcopy.Result{Repo: r.Repo, PR: n, Head: r.Head, Branch: b}, cc); err != nil {
			return refuse(errOut, verb, err.Error())
		}
		fmt.Fprintf(out, "RECORDED pr=%s#%d head=%s branch=%s\n", prkey.Name(r.Repo), n, r.Head, b)
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

// friendBeatOnce is one tick of friend beat: the beat and every held
// copy's lease, one round trip (life.FriendBeat).
func friendBeatOnce(ctx context.Context, st *store.Store, k taskcard.Consumer, host string, now time.Time) (life.FriendBeatResult, error) {
	return life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: k.Name, Host: host,
		Load1: life.Load1Now(), NCPU: runtime.NumCPU(), CPU: life.CPUBusyNow(), At: now})
}

func runFriendBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend beat"
	fs := verbflag.New(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	as := fs.String("as", "", "")
	host := fs.String("host", "", "")
	once := fs.Bool("once", false, "")
	loop := fs.Bool("loop", false, "")
	lease := fs.String("lease", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *once && *loop {
		return refuse(errOut, verb, "--once and --loop are two ways; take one")
	}
	if *lease != "" && !*loop {
		return refuse(errOut, verb, "--lease goes with --loop")
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
	if *loop {
		return runFriendBeatLoop(ctx, st, k, *host, *lease, out, errOut)
	}
	res, err := friendBeatOnce(ctx, st, k, *host, time.Now())
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "FRIEND BEAT as=%s host=%s working=%d lease_until=%d at=%d\n", k, *host, res.Working, res.LeaseUntil, res.AtMS)
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
			if _, err := friendBeatOnce(signalCtx, st, k, *host, now); err != nil {
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

// runFriendBeatLoop is friend beat --loop: the friend's one beat loop. It
// holds friend:<f>:beatloop (token: the starting verb's claim, else its own
// claim, and a loop already holding it is left alone) and steps each
// second (life.BeatLoop.Step: renew the lease, beat, count idle ticks)
// until the friend holds no working copy for two ticks, another loop holds
// the lease, or a signal (which releases the lease).
func runFriendBeatLoop(ctx context.Context, st *store.Store, k taskcard.Consumer, host, token string, out, errOut io.Writer) int {
	c := st.Client()
	osHost, _ := os.Hostname()
	me := life.LoopHolder{Token: token, Host: osHost, PID: os.Getpid()}
	if me.Token == "" {
		me.Token = fmt.Sprintf("%s:%d:%d", osHost, os.Getpid(), time.Now().UnixNano())
		took, held, err := life.ClaimBeatLoop(ctx, c, k.Name, me, "")
		if err != nil {
			return refuse(errOut, "friend beat", err.Error())
		}
		if !took {
			fmt.Fprintf(out, "FRIEND BEAT LOOP as=%s held key=%s pid=%d host=%s\n", k, life.BeatLoopKey(k.Name), held.PID, dash(held.Host))
			return 0
		}
	}
	l := &life.BeatLoop{Lease: life.StoreLease{Client: c, Friend: k.Name, Me: me}, Friend: k.Name,
		Tick: func(ctx context.Context, now time.Time) (int, error) {
			res, err := friendBeatOnce(ctx, st, k, host, now)
			return res.Working, err
		}}
	fmt.Fprintf(out, "FRIEND BEAT LOOP as=%s host=%s key=%s pid=%d\n", k, host, life.BeatLoopKey(k.Name), os.Getpid())
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	var backoff time.Duration
	var next time.Time
	now := time.Now()
	for {
		if !now.Before(next) {
			done, why, err := l.Step(signalCtx, now)
			switch {
			case err != nil && signalCtx.Err() == nil:
				backoff = benchBeatBackoff(backoff, life.BeatInterval)
				next = now.Add(backoff)
				fmt.Fprintf(errOut, "friend %s beat loop: %v; retry in %s\n", k.Name, err, backoff)
			case done:
				fmt.Fprintf(out, "FRIEND BEAT LOOP END as=%s why=%s\n", k, quoteField(why))
				return 0
			default:
				backoff, next = 0, time.Time{}
			}
		}
		select {
		case <-signalCtx.Done():
			// a stopped loop lets go at once, so the next verb starts one
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = life.ReleaseBeatLoop(rctx, c, k.Name, me.Token)
			cancel()
			fmt.Fprintf(out, "FRIEND BEAT LOOP END as=%s why=signal\n", k)
			return 0
		case now = <-ticker.C:
		}
	}
}
