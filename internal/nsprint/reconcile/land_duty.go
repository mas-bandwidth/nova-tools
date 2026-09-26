package reconcile

// The land duty (nova-tools #3898): landing is a reconciler duty.
//
// THE HURT (2026-09-25 10:45 AM ET). The merging column sat at 19 cards while
// Rowan built the stream branch by hand: land stream picked 0 members because
// the reads were in pr:<name>:<n>:lines (read post's list), which the
// lander's reads-field parser never saw, and the Studio test run died twice.
// Glenn: "What do we need to do to ensure this mistake doesn't happen again?"
//
// THE RULE. Every cfg:land tick (seconds, default 300) for every repo the
// duty lands (cfg:land repos, space- or comma-joined owner/name; default
// DefaultLandRepo), one worker takes lease:land:<repo> (never two landings of
// one repo at once; lua/land_duty.lua keeps the lease and the timer) and, for
// every stream in ws:order with at least one landable member (a read at head
// >= cfg:land min_score, counted from the record's reads field and its lines
// list) or an open landing, runs the whole land sequence of #3886
// (stream.Run: build oldest first, push, ONE stream PR, CI request, one read
// of the CI word, merge on green) with every landable member in one batch.
// The batch test is that CI request, claimed by a bench (nova-tools#3899):
// the duty runs no go test on its own seat (NoTest). The CI wait is never slept on: a pass
// that finds CI pending leaves the landing open, and the next tick resumes it
// at its wait and merges it on green.
//
// A member whose merge conflicts is parked (merging -> working, its record
// state=parked) and a rebase task goes to its author's queue (the builder
// task's owner when a friend, else the coordinator), titled with the stream
// branch, its head and the files; the batch lands without it. Members with no
// read at head are never skipped silently: each stream's receipt line names
// them.
//
//	LAND-DUTY repo=<r> stream=<s> state=<st> pr=#<n> members=#a,#b unread=#c parked=#d:<why> rebase=<id> ci=<word> err=<e>
//
// The work runs on the worker's goroutine, off the 1 s reconciler tick, like
// the harvest workers (#3737): Stop cancels it and gives back the lease.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	// DefaultLandRepo is the repo the duty lands when cfg:land names none.
	DefaultLandRepo = "mas-bandwidth/nova-tools"
	// DefaultLandLeaseTTL is lease:land:<repo>'s TTL; the worker renews it
	// every third of it for as long as the pass runs.
	DefaultLandLeaseTTL = time.Minute
	// LandActor is the `by` of the duty's moves and receipts.
	LandActor = "lander"

	fnLandTake    = "ns_land_duty_take"
	fnLandRenew   = "ns_land_duty_renew"
	fnLandPass    = "ns_land_duty_pass"
	fnLandRelease = "ns_land_duty_release"
)

// RebaseTask is the task a parked conflicting member's author gets.
type RebaseTask struct {
	ID, Title, Stream, Repo, Head, Base, To string
	N                                       int
}

// LandDuty is the reconciler's land duty. Its Run is a Duty.
type LandDuty struct {
	Client *redis.Client
	// Repos are the owner/name repos to land; empty reads cfg:land repos
	// each pass, else DefaultLandRepo.
	Repos []string
	// Base is the branch the streams land into; "dev" when empty.
	Base string
	// Workroot is the parent of each build's scratch clone (deleted at the
	// end of the build); ~/rowan-working/tmp when empty.
	Workroot string
	// Mirror is the bench mirror a build clone references, per repo; nil or
	// "" clones without one.
	Mirror func(repo string) string
	// GitHub makes the REST client of one repo pass (the stream PR, the
	// merge, the members' closes). Required once a stream has a member.
	GitHub func() (*stream.GitHub, error)
	// Request puts a stream head in the CI pool (ci.Request) and returns its
	// status word. Required once a stream has a member.
	Request func(ctx context.Context, repo, sha string, pr int, url string) (string, error)
	// CIURL is the clone URL the CI request carries ("": the runner's mirror).
	CIURL string
	// BaseTip reads the base tip from the remote; nil is git ls-remote.
	BaseTip func(ctx context.Context, remote, base string) (string, error)
	// Push pushes a rebase task; nil pushes it with task.Push into the first
	// sprint of sprint:order.
	Push func(ctx context.Context, t RebaseTask) (string, error)
	// TestTimeout bounds one batch test (the stream package's default when
	// zero).
	TestTimeout time.Duration
	// Host names this machine on the lease.
	Host string
	// TTL is the lease TTL; DefaultLandLeaseTTL when zero.
	TTL time.Duration
	// Out receives the LAND-DUTY receipt lines; Log the build's own lines.
	Out, Log io.Writer

	mu     sync.Mutex
	busy   map[string]bool
	held   map[string]string // repo -> the token its worker holds
	inst   string
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// LandLine is one stream's receipt for one pass.
type LandLine struct {
	Repo, Stream, Slug string
	State              string // the land run's state, or "none" (unread only)
	PR                 int
	Members            []int
	Unread             []int
	Skips              []stream.Skip // not landable for another reason
	Parked             []stream.Parked
	Rebase             []string // rebase task ids pushed (or found)
	CI                 string   // the CI request's status word
	Err                string
}

// String is the receipt line.
func (r LandLine) String() string {
	parked := make([]string, 0, len(r.Parked))
	for _, p := range r.Parked {
		parked = append(parked, fmt.Sprintf("#%d:%s", p.N, p.Why))
	}
	skips := make([]string, 0, len(r.Skips))
	for _, s := range r.Skips {
		skips = append(skips, fmt.Sprintf("#%d:%s", s.N, s.Why))
	}
	pr := "-"
	if r.PR > 0 {
		pr = "#" + strconv.Itoa(r.PR)
	}
	return fmt.Sprintf("LAND-DUTY repo=%s stream=%s state=%s pr=%s members=%s unread=%s skip=%s parked=%s rebase=%s ci=%s err=%s",
		r.Repo, wrField(r.Stream), r.State, pr, numList(r.Members), numList(r.Unread), wrList(skips),
		oneline.Field(wrList(parked)), wrList(r.Rebase), orDashStr(r.CI), oneline.Field(orDashStr(r.Err)))
}

func numList(ns []int) string {
	if len(ns) == 0 {
		return "-"
	}
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(out, ",")
}

func orDashStr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// LandPass is one repo's pass: Status TAKEN (it ran), HELD (another worker
// holds lease:land:<repo>) or WAIT (under a tick since the last pass).
type LandPass struct {
	Repo, Status, Holder string
	Lines                []LandLine
	Took                 time.Duration
}

// Run is one reconciler pass: each repo with no worker in flight starts one
// on its own goroutine; the worker's Pass takes the lease or returns. l may
// be nil (a pass outside the reconciler).
func (d *LandDuty) Run(ctx context.Context, l *Lease) (Counts, error) {
	repos, err := d.repos(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("land: %w", err)
	}
	instance := "reconciler"
	if l != nil {
		instance += "-" + l.Instance()
	}
	for _, r := range repos {
		d.start(ctx, l, instance, r)
	}
	return Counts{}, nil
}

func (d *LandDuty) repos(ctx context.Context) ([]string, error) {
	if len(d.Repos) > 0 {
		return d.Repos, nil
	}
	v, err := d.Client.HGet(ctx, "cfg:land", "repos").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	if len(out) == 0 {
		out = []string{DefaultLandRepo}
	}
	return out, nil
}

func (d *LandDuty) start(ctx context.Context, l *Lease, instance, repo string) {
	d.mu.Lock()
	if d.busy == nil {
		d.busy, d.held = map[string]bool{}, map[string]string{}
	}
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(context.WithoutCancel(ctx))
	}
	if d.busy[repo] || d.ctx.Err() != nil || (l != nil && l.Fenced()) {
		d.mu.Unlock()
		return
	}
	wctx := d.ctx
	d.inst = instance
	d.busy[repo] = true
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.busy, repo)
			d.mu.Unlock()
		}()
		p, err := d.Pass(wctx, instance, repo)
		d.print(p, err)
	}()
}

// Wait blocks until every worker in flight has returned.
func (d *LandDuty) Wait() { d.wg.Wait() }

// Stop cancels every worker in flight and waits for them until ctx ends;
// each gives back its lease on the way. A lease still held after the wait is
// released by its token. It returns the repos released that way.
func (d *LandDuty) Stop(ctx context.Context) []string {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
	waited := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	d.mu.Lock()
	held := make(map[string]string, len(d.held))
	for r, tok := range d.held {
		held[r] = tok
	}
	inst := d.inst
	d.mu.Unlock()
	var released []string
	for r, tok := range held {
		reply, err := d.Client.FCall(context.WithoutCancel(ctx), fnLandRelease, nil, r, inst, tok).StringSlice()
		switch {
		case err == nil && word(reply, 0) == "OK":
			released = append(released, r)
		case d.Out != nil:
			// A lease not given back on the way out is held until its TTL:
			// the next instance waits that long, so the line says so.
			fmt.Fprintf(d.Out, "LAND-DUTY repo=%s release=refused reply=%s%s\n", r, oneline.Field(strings.Join(reply, " ")), errField(err))
		}
	}
	sort.Strings(released)
	return released
}

// errField is " err=<text>" for a non-nil error, "" otherwise.
func errField(err error) string {
	if err == nil {
		return ""
	}
	return " err=" + oneline.Field(err.Error())
}

func (d *LandDuty) print(p LandPass, err error) {
	if d.Out == nil {
		return
	}
	for _, r := range p.Lines {
		_, _ = fmt.Fprintln(d.Out, r.String())
	}
	if err != nil {
		_, _ = fmt.Fprintf(d.Out, "LAND-DUTY repo=%s err=%s\n", p.Repo, oneline.Field(err.Error()))
	}
}

func (d *LandDuty) ttl() time.Duration {
	if d.TTL > 0 {
		return d.TTL
	}
	return DefaultLandLeaseTTL
}

func (d *LandDuty) setHeld(repo, token string, held bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.held == nil {
		d.held = map[string]string{}
	}
	if held {
		d.held[repo] = token
	} else if d.held[repo] == token {
		delete(d.held, repo)
	}
}

// Pass is one repo's pass under lease:land:<repo>: take it (or return HELD
// or WAIT), land every stream with a landable member, write the pass line
// and give the lease back in one call. The worker renews the lease every
// third of its TTL while it runs; a lease lost to another worker cancels
// the pass.
func (d *LandDuty) Pass(ctx context.Context, instance, repo string) (LandPass, error) {
	p := LandPass{Repo: repo}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return p, err
	}
	token := hex.EncodeToString(b[:])
	ttl := d.ttl()
	reply, err := d.Client.FCall(ctx, fnLandTake, nil, repo, instance, token, d.Host, ttl.Milliseconds()).StringSlice()
	if err != nil {
		return p, fmt.Errorf("%s %s: %w", fnLandTake, repo, err)
	}
	p.Status = word(reply, 0)
	switch p.Status {
	case "TAKEN":
	case "HELD":
		p.Holder = word(reply, 1)
		return p, nil
	case "WAIT":
		return p, nil
	default:
		return p, fmt.Errorf("%s %s: %v", fnLandTake, repo, reply)
	}
	d.setHeld(repo, token, true)
	start := time.Now()
	pctx, cancel := context.WithCancel(ctx)
	lost := make(chan string, 1)
	renewErr := make(chan string, 1)
	beat := make(chan struct{})
	go func() {
		defer close(beat)
		t := time.NewTicker(ttl / 3)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				r, err := d.Client.FCall(pctx, fnLandRenew, nil, repo, instance, token, ttl.Milliseconds()).StringSlice()
				if err == nil && word(r, 0) == "LOST" {
					lost <- word(r, 1)
					cancel()
					return
				}
				if pctx.Err() == nil && (err != nil || word(r, 0) != "OK") {
					// A renew that did not land is on the pass line once
					// (its first failure), never a quiet retry every TTL/3.
					select {
					case renewErr <- fmt.Sprintf("renew: reply=%s%s", oneline.Field(strings.Join(r, " ")), errField(err)):
					default:
					}
				}
			}
		}
	}()
	lines, err := d.land(pctx, repo)
	cancel()
	<-beat
	p.Lines, p.Took = lines, time.Since(start)
	select {
	case r := <-renewErr:
		p.Lines = append(p.Lines, LandLine{Repo: repo, State: "error", Err: r})
	default:
	}
	select {
	case h := <-lost:
		d.setHeld(repo, token, false)
		return p, fmt.Errorf("lease:land:%s lost to %s during the pass", repo, orDashStr(h))
	default:
	}
	var opened, landed, parked, unread int
	for _, l := range lines {
		if l.PR > 0 && (l.State == "waiting" || l.State == "landed" || l.State == "red") {
			opened++
		}
		if l.State == "landed" {
			landed++
		}
		parked += len(l.Parked)
		unread += len(l.Unread)
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	r, perr := d.Client.FCall(context.WithoutCancel(ctx), fnLandPass, nil, repo, instance, token, p.Took.Milliseconds(),
		len(lines), opened, landed, parked, unread, errText).StringSlice()
	d.setHeld(repo, token, false)
	if perr != nil {
		return p, errors.Join(err, fmt.Errorf("%s %s: %w", fnLandPass, repo, perr))
	}
	if word(r, 0) != "OK" {
		return p, errors.Join(err, fmt.Errorf("lease:land:%s lost to %s before the pass line", repo, orDashStr(word(r, 1))))
	}
	return p, err
}

// land runs the land sequence for every stream in ws:order with a landable
// member or an open landing, oldest stream rank first. One stream's error is
// on its line; the pass goes on to the next.
func (d *LandDuty) land(ctx context.Context, repo string) ([]LandLine, error) {
	c := d.Client
	streams, err := c.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("ws:order: %w", err)
	}
	cfg, err := stream.LoadConfig(ctx, c, repo)
	if err != nil {
		return nil, err
	}
	var gh *stream.GitHub
	var out []LandLine
	for _, s := range streams {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		// The worker runs off the reconciler tick, for minutes: it reads the
		// pit stop afresh before every stream, so a stop set mid-pass builds,
		// pushes and merges nothing more.
		if free, err := d.unheld(ctx, s); err != nil {
			return out, err
		} else if !free {
			continue
		}
		slug, err := stream.Slug(s)
		if err != nil {
			// A stream whose name makes no slug is on the pass line as an
			// error, never skipped in silence.
			out = append(out, LandLine{Repo: repo, Stream: s, State: "error", Err: "slug: " + err.Error()})
			continue
		}
		members, skips, err := stream.Members(ctx, c, repo, []string{s}, cfg.MinScore)
		if err != nil {
			out = append(out, LandLine{Repo: repo, Stream: s, Slug: slug, State: "error", Err: err.Error()})
			continue
		}
		line := LandLine{Repo: repo, Stream: s, Slug: slug, State: "none"}
		for _, sk := range skips {
			if sk.Why == "no-read-at-head" {
				line.Unread = append(line.Unread, sk.N)
			} else {
				line.Skips = append(line.Skips, sk)
			}
		}
		prev, open, err := stream.LoadLanding(ctx, c, repo, slug)
		if err != nil {
			line.State, line.Err = "error", err.Error()
			out = append(out, line)
			continue
		}
		open = open && prev.State == "open" && prev.PR > 0
		if len(members) == 0 && !open {
			if len(line.Unread) > 0 || len(line.Skips) > 0 {
				out = append(out, line)
			}
			continue
		}
		if gh == nil {
			if d.GitHub == nil {
				line.State, line.Err = "error", "no GitHub client"
				out = append(out, line)
				continue
			}
			if gh, err = d.GitHub(); err != nil {
				gh = nil
				line.State, line.Err = "error", err.Error()
				out = append(out, line)
				continue
			}
		}
		out = append(out, d.landStream(ctx, repo, s, slug, gh, line))
	}
	return out, nil
}

// unheld is whether no pit stop holds the stream now: a fresh
// pitstop.HeldOpen read (the worker's context outlives the pass that started
// it, so the pass's holds are stale here).
func (d *LandDuty) unheld(ctx context.Context, s string) (bool, error) {
	hs, err := pitstop.HeldOpen(ctx, d.Client)
	if err != nil {
		return false, fmt.Errorf("pitstop: %w", err)
	}
	_, held := hs.Stream(s)
	return !held, nil
}

func (d *LandDuty) base() string {
	if d.Base != "" {
		return d.Base
	}
	return "dev"
}

func (d *LandDuty) workroot() string {
	if d.Workroot != "" {
		return d.Workroot
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "rowan-working", "tmp")
}

// landStream is one stream's land run with every landable member in one
// batch, conflicts parked, and a rebase task per parked conflict.
func (d *LandDuty) landStream(ctx context.Context, repo, s, slug string, gh *stream.GitHub, line LandLine) LandLine {
	if d.Request == nil {
		line.State, line.Err = "error", "no CI request seam"
		return line
	}
	mirror := ""
	if d.Mirror != nil {
		mirror = d.Mirror(repo)
	}
	o := stream.RunOptions{
		Options: stream.Options{Repo: repo, Streams: []string{s}, Base: d.base(), Mirror: mirror,
			Workdir:     filepath.Join(d.workroot(), fmt.Sprintf("land-%s-%d", strings.ReplaceAll(slug, "+", "_"), time.Now().UnixNano())),
			TestTimeout: d.TestTimeout, MinScore: -1, By: LandActor, Author: "Rowan <rowan@mas-bandwidth.com>",
			Log: d.Log, GH: gh, ParkConflicts: true, NoTest: true},
		// No sleeping on CI: one read of the CI word, and the next tick
		// resumes the open landing at its wait.
		CIWait: 0, Tick: time.Second, Request: d.Request, CIURL: d.CIURL, BaseTip: d.BaseTip,
	}
	rep, err := stream.Run(ctx, d.Client, o)
	line.State, line.CI = rep.State, rep.CIRequest
	if line.State == "" {
		line.State = "error"
	}
	line.PR = rep.Landing.PR
	if line.PR == 0 {
		line.PR = rep.Build.PR
	}
	switch {
	case rep.State == "landed":
		for _, m := range rep.Merge.Landing.Members {
			line.Members = append(line.Members, m.N)
		}
	case len(rep.Build.Build.Kept) > 0 && !rep.Resumed:
		for _, m := range rep.Build.Build.Kept {
			line.Members = append(line.Members, m.N)
		}
	default:
		for _, m := range rep.Landing.Members {
			line.Members = append(line.Members, m.N)
		}
	}
	if err != nil {
		line.Err = err.Error()
	}
	// A member task the one move refused stays where it was: the receipt
	// names it (land stream prints the same refusals).
	if len(rep.Merge.Skipped) > 0 {
		line.Err = strings.TrimPrefix(line.Err+"; ", "; ") + "moves refused: " + strings.Join(rep.Merge.Skipped, "; ")
	}
	line.Parked = rep.Build.Build.Parked
	for _, p := range line.Parked {
		files, ok := strings.CutPrefix(p.Why, "conflict:")
		if !ok || p.Task == "" {
			continue
		}
		id, perr := d.rebase(ctx, repo, s, slug, rep, p, files)
		switch {
		case perr != nil:
			line.Err = strings.TrimPrefix(line.Err+"; ", "; ") + fmt.Sprintf("rebase #%d: %v", p.N, perr)
		case id != "":
			line.Rebase = append(line.Rebase, id)
		}
	}
	return line
}

// rebase pushes one parked conflict's rebase task to its author: the owner
// of the member's builder task when that owner is a friend, else the
// coordinator.
func (d *LandDuty) rebase(ctx context.Context, repo, s, slug string, rep stream.RunReport, p stream.Parked, files string) (string, error) {
	c := d.Client
	owner, err := c.HGet(ctx, "task:"+p.Task, "owner").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	to := ""
	if owner != "" {
		ok, err := c.SIsMember(ctx, "friends", owner).Result()
		if err != nil {
			return "", err
		}
		if ok {
			to = owner
		}
	}
	if to == "" {
		if to, err = (&RouteDuty{Client: c}).coordinator(ctx); err != nil {
			return "", err
		}
	}
	if to == "" {
		return "", errors.New("no author and no coordinator")
	}
	name := prkey.Name(repo)
	id := fmt.Sprintf("rebase-%d-%s", p.N, stream.Short(p.Head))
	if name != "nova-tools" {
		id += "-" + name
	}
	branch, head := rep.Build.Branch, rep.Build.Build.Head
	if branch == "" {
		branch = stream.DefaultBranch(slug, 1)
	}
	t := RebaseTask{ID: id, Stream: s, Repo: repo, N: p.N, Head: p.Head, Base: d.base(), To: to,
		Title: fmt.Sprintf("STREAM: %s | rebase %s#%d at %s onto %s: it conflicts with %s at %s in %s",
			s, name, p.N, stream.Short(p.Head), d.base(), branch, stream.Short(head), strings.ReplaceAll(files, ",", " "))}
	if d.Push != nil {
		return d.Push(ctx, t)
	}
	return pushRebase(ctx, c, t)
}

// pushRebase is the default Push: task.Push (kind rebase, front of the
// author's queue) into the first sprint of sprint:order.
func pushRebase(ctx context.Context, c *redis.Client, t RebaseTask) (string, error) {
	sprints, err := c.ZRange(ctx, "sprint:order", 0, 0).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	if len(sprints) == 0 {
		return "", task.ErrNoSprint
	}
	res, err := task.Push(ctx, store.New(c), task.PushRequest{
		Sprint: sprints[0], ID: t.ID, Kind: task.KindRebase, Title: t.Title, Effects: task.EffectsExternal,
		Repo: t.Repo, PR: t.N, Head: t.Head, Ref: t.Base, To: t.To, Front: true, Actor: LandActor, ErrOut: io.Discard,
	})
	if err != nil {
		return "", err
	}
	switch res {
	case task.PushCreated, task.PushExists, task.PushClosed:
		return t.ID, nil
	}
	return "", fmt.Errorf("task push %s: %s", t.ID, res)
}
