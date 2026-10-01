package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/binstamp"
	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// cmdMember is `nova-swarm member`: this machine as one member of a sprint's
// fleet (or one of its readers). Every few seconds it beats, reads its queue
// from the sprint, pushes the commit of every work card whose child ended and
// reports it, takes up to its width and starts each card taken as one child
// through `nova-swarm native`. The fleet table is the dispatcher; the loop is
// internal/member.
func cmdMember(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("member", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &flags{verb: "member", fs: fs}
	as := fs.String("as", "", "")
	width := fs.Int("width", 0, "")
	reader := fs.Bool("reader", false, "")
	sprintBin := fs.String("sprint", "nova-sprint", "")
	harness := fs.String("harness", "", "")
	model := fs.String("model", "", "")
	root := fs.String("root", "", "")
	slots := fs.String("slots", "", "")
	resultsRoot := fs.String("results-root", "", "")
	deadline := newSecondsFlag(fs, "deadline", 0) // the override: a card with no route
	tokensWord := fs.String("tokens", "", "")
	every := newSecondsFlag(fs, "every", 3*time.Second)
	once := fs.Bool("once", false, "")
	ticks := fs.Int("ticks", 0, "")
	auth := fs.String("auth", "", "")
	config := fs.String("config", "", "")
	workerFile := fs.String("worker", "", "")
	noWall := fs.Bool("no-wall", false, "")
	ghBin := fs.String("gh", "gh", "")
	passFlag := fs.String("pass", "", "")
	diskFloor := fs.Int("disk-floor", 10, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*as, "as", "the member's name in the fleet table (a reader's in the readers table with --reader)")
	// a member runs the width its fleet row names (fleet up --width, fleet sync), read with
	// its queue every tick; --width is a reader's, or a twin's override
	if *reader {
		f.wantCount(*width, "width", "the most reads this reader runs at once")
	} else if *width < 0 {
		f.add("--width is an override of the fleet row's width and is at least 1; leave it out to run the row's")
	}
	f.want(*harness, "harness", "the harness binary path a card runs under (nova-swarm native --harness)")
	// the card decides its model: the deal writes the route it drew into the packet
	// (provider/model, tokens, deadline); --model, --tokens and --deadline are the
	// override a card with no route runs on (a store with no route: a twin, one machine)
	if *model != "" {
		if _, ok := providerOf(*model); !ok {
			f.add(fmt.Sprintf("--model %q is not provider/model (one slash, both sides nonempty); every card would be refused by native", *model))
		}
	}
	f.want(*root, "root", "the root directory slots and results sit under")
	if *tokensWord != "" {
		f.tokens(*tokensWord) // the word is read here, once, so a typo is one refusal and not one failed card each
	}
	if deadline.d < 0 {
		f.add("--deadline is the wall bound per card a card with no route runs to, above 0")
	}
	if every.d <= 0 || every.d > 5*time.Second {
		f.add("--every is between 1ms and 5s: the beat it carries has a 15s deadline in the fleet")
	}
	// The store is nova-sprint's to know: the member passes its environment
	// through (NOVA_SPRINT_REDIS, or a seat) and names no address itself.
	if *as != "" && !safepath.NameOK(*as) {
		f.add(fmt.Sprintf("--as %q is not a name (letters, digits, - _ .)", *as))
	}
	var onceGiven, ticksGiven bool
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "once":
			onceGiven = true
		case "ticks":
			ticksGiven = true
		}
	})
	if onceGiven && ticksGiven {
		f.add("give --once or --ticks <n>, not both")
	}
	if ticksGiven && *ticks <= 0 {
		f.add("give --ticks 1 or more, or leave it out to run until stopped")
	}
	if *diskFloor < 0 {
		f.add("--disk-floor is the free GiB the slots' volume must keep for the member to start a card: 0 or more (0 checks nothing; default 10)")
	}
	pass := splitNames(*passFlag)
	for _, n := range pass {
		if !envNameRE.MatchString(n) {
			f.add(fmt.Sprintf("--pass %q is not an environment name (letters, digits, _)", n))
		}
	}
	if *workerFile != "" {
		// the worker description names the secret its harness reads: that one name is
		// handed through too (docs/SPEC-CARD-CONTRACT.md, the child's environment)
		if w, problems := swarm.LoadWorker(*workerFile); len(problems) == 0 && w.Secret != "" {
			pass = append(pass, w.Secret)
		}
	}
	if f.refused(stderr) {
		return 2
	}
	if *slots == "" {
		*slots = filepath.Join(*root, "slots")
	}
	if *resultsRoot == "" {
		*resultsRoot = filepath.Join(*root, "results")
	}
	for _, d := range []string{*slots, *resultsRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refuse(stderr, " member", err.Error())
		}
	}
	self, err := os.Executable()
	if err != nil {
		return refuse(stderr, " member", "own executable: "+err.Error())
	}
	sp := &execSprint{bin: *sprintBin, actor: *as}
	rn := &nativeRunner{
		self: self, sprintBin: *sprintBin, harness: *harness, model: *model, root: *root, slots: *slots,
		resultsRoot: *resultsRoot, deadline: deadline.d, tokens: *tokensWord, auth: *auth, config: *config,
		worker: *workerFile, noWall: *noWall, stderr: stderr, pass: pass,
	}
	// a work card's commit is pushed by the member, outside the wall, at its
	// finish (memberpush.go); a read pushes nothing
	var pu member.Pusher
	if !*reader {
		gp := newGitPusher(*root, *slots, *sprintBin)
		gp.gh = *ghBin
		pu = gp
	}
	// the machine's CPU, a sample a second, for the beat's load (hostload.Sampler); a
	// reader beats nothing
	var meter *hostload.Sampler
	if !*reader {
		meter = hostload.NewSampler(hostload.Local())
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		go meter.Run(ctx)
	}
	// a launch the member is done with leaves no checkout behind, and none is started on a
	// volume under the floor (slotclean.go); what a crash or a kill left is swept first
	var room func() (bool, string)
	if *diskFloor > 0 {
		room = diskRoom(*slots, *diskFloor, diskFree)
	}
	m := member.New(member.Config{As: *as, Width: *width, Reader: *reader, Meter: meter, Room: room}, sp, rn, pu, stdout)
	kind := "member"
	if *reader {
		kind = "reader"
	}
	widthWord := "row" // the fleet row's, read every tick
	if *width > 0 {
		widthWord = "override:" + strconv.Itoa(*width)
	}
	modelWord := "card" // each card's route, from its packet
	if *model != "" {
		modelWord = "card,override:" + *model
	}
	fmt.Fprintf(stdout, "MEMBER %s as=%s width=%s every=%s sprint=%s harness=%s model=%s\n", oneline.Field(kind), oneline.Field(*as), oneline.Field(widthWord), oneline.Field(every.d.String()), oneline.Field(*sprintBin), oneline.Field(*harness), oneline.Field(modelWord))
	if note := passNote(*model, pass, *auth); note != "" {
		fmt.Fprintln(stdout, note)
	}
	if removed, kept := rn.prune(time.Now()); removed > 0 {
		fmt.Fprintf(stdout, "NOTE sweep: removed %d ended launch directories under %s, kept the newest %d\n", removed, oneline.Field(*slots), kept)
	}
	n, replaced := memberLoop(m, every.d, loopTicks(*once, ticksGiven, *ticks), func() string { return binstamp.Of(self) }, stdout, stderr)
	if replaced {
		return exitReplaced
	}
	fmt.Fprintf(stdout, "MEMBER OK as=%s ticks=%d running=%d\n", oneline.Field(*as), n, m.Running())
	return 0
}

// exitReplaced is member's exit when its binary was replaced under it: not 0, so
// a supervisor that restarts only a failed loop restarts it too (the loop units
// in fleet/templates restart on any exit).
const exitReplaced = 3

// loopTicks is the tick count a bounded run stops at: 1 for --once, --ticks' n,
// 0 for a run until stopped.
func loopTicks(once, ticksGiven bool, ticks int) int {
	if once {
		return 1
	}
	if ticksGiven {
		return ticks
	}
	return 0
}

// memberLoop ticks m every `every` (limit > 0: at most limit ticks) and returns the
// ticks it ran. A loop runs the code it was started with for as long as it runs: a
// release installed under it would leave the fleet worked by the code before it
// (2026-10-01: six members kept the binaries they began with until restarted by
// hand). So before each tick it reads its binary's stamp, and when that changed
// since it began: with no child running it stops at once, saying so; with children
// running it drains, taking no new card (said once) and reporting each child as it
// ends, and stops when the last is reported. replaced is true when it stopped so.
func memberLoop(m *member.Member, every time.Duration, limit int, stamp func() string, stdout, stderr io.Writer) (n int, replaced bool) {
	began := stamp()
	draining := false
	for {
		if began != "" && stamp() != began {
			if m.Running() == 0 {
				fmt.Fprintf(stdout, "MEMBER STOP the binary this member runs was replaced; its supervisor starts the new one\n")
				return n, true
			}
			if !draining {
				draining = true
				m.Drain()
				fmt.Fprintf(stdout, "MEMBER DRAIN the binary this member runs was replaced: taking no new card, %d running; it stops when the last child is reported\n", m.Running())
			}
		}
		n++
		acted, err := m.Tick(time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm member: tick %d: %s\n", n, oneline.Escape(err.Error()))
		}
		if acted > 0 || err != nil {
			fmt.Fprintf(stdout, "tick %d acted=%d running=%d %s\n", n, acted, m.Running(), oneline.Field(time.Now().Format("15:04:05")))
		}
		if limit > 0 && n >= limit {
			return n, false
		}
		time.Sleep(every)
	}
}

// execSprint runs the sprint's verbs as the nova-sprint binary, with this
// process's environment (the store address is nova-sprint's own flag or
// environment, never the member's).
type execSprint struct {
	bin, actor string
	env        []string // added to this process's environment: none in production, a test's store address
}

// sprintVerbBudget is how long one sprint verb may run before the member stops waiting
// for it: a verb is one store round trip or one planned batch, and a stuck one is a
// tick that never ends.
const sprintVerbBudget = 120 * time.Second

func (s *execSprint) Run(args ...string) (int, []byte) {
	cmd, cancel := subproc.CommandFor(context.Background(), sprintVerbBudget, s.bin, args...)
	defer cancel()
	cmd.Env = append(append(os.Environ(), s.env...), "NOVA_SPRINT_ACTOR="+s.actor)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return 2, []byte(err.Error())
	}
	if code != 0 && out.Len() == 0 {
		return code, errb.Bytes()
	}
	return code, out.Bytes()
}

// nativeRunner runs one packet as one `nova-swarm native` child in its own
// slot directory, its results under <results-root>/<card>/.
type nativeRunner struct {
	self, sprintBin, harness, model, root, slots, resultsRoot string
	deadline                                                  time.Duration
	tokens, auth, config, worker                              string
	noWall                                                    bool
	stderr                                                    io.Writer
	env                                                       []string // added to this process's environment: none in production, a test's
	pass                                                      []string // the secret names handed to native (--pass, the worker's secret)

	live, kept map[string]bool // launches started and not yet ended; failed ones ended and kept (slotclean.go)
}

// started marks a launch running, so no prune of the pool touches its directory until the
// member ends it (slotclean.go).
func (r *nativeRunner) started(name string) {
	if r.live == nil {
		r.live = map[string]bool{}
	}
	r.live[name] = true
}

func (r *nativeRunner) Start(p member.Packet) (member.Child, error) {
	if !safepath.NameOK(p.Card) {
		return nil, fmt.Errorf("card %q is not a name", p.Card)
	}
	// one slot and one results root per launch (the card at its generation, or
	// attempt, in its epoch), so a result can only be this launch's; a launch
	// whose pid file names a live process is adopted, never run twice
	name := launchName(p)
	slot := filepath.Join(r.slots, name)
	results := filepath.Join(r.resultsRoot, name)
	logPath := filepath.Join(r.slots, name+".native.log")
	pidPath := filepath.Join(r.slots, name+".pid")
	job := filepath.Join(slot, "jobs", p.Card)
	if pid := livePID(pidPath); pid > 0 {
		c := &nativeChild{card: p.Card, logPath: logPath, results: results, job: job, done: make(chan struct{})}
		go func() {
			for processAlive(pid) {
				time.Sleep(time.Second)
			}
			close(c.done)
		}()
		r.started(name)
		return c, nil
	}
	if err := safepath.RemoveUnder(r.slots, slot); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return nil, err
	}
	cardPath := filepath.Join(r.slots, name+".card.md")
	if err := os.WriteFile(cardPath, []byte(member.CardText(p, r.sprintBin)), 0o644); err != nil {
		return nil, err
	}
	model, tokens, deadline, err := r.route(p)
	if err != nil {
		return nil, err
	}
	framePath := filepath.Join(r.slots, name+cardcontract.FrameName)
	if err := cardcontract.WriteFrame(framePath, frameOf(p, model, r.root)); err != nil {
		return nil, err
	}
	args := []string{"native", "--harness", r.harness, "--model", model, "--card", cardPath, "--frame", framePath, "--slot", slot,
		"--root", r.root, "--deadline", deadline.String(), "--tokens", tokens, "--label", p.Card, "--results-root", results}
	if r.auth != "" {
		args = append(args, "--auth", r.auth)
	}
	if r.config != "" {
		args = append(args, "--config", r.config)
	}
	if r.worker != "" {
		args = append(args, "--worker", r.worker)
	}
	if r.noWall {
		args = append(args, "--no-wall")
	}
	// A long-lived child: its own cancellable context and no deadline (its own --deadline
	// ends it), released when the wait returns.
	ctx, release := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, r.self, args...)
	cmd.Env = childEnviron(append(os.Environ(), r.env...), r.pass)
	logf, err := os.Create(logPath)
	if err != nil {
		release()
		return nil, err
	}
	// a file in the slot, never a pipe back to the member: the wait below is on native's
	// process alone, so a process native left cannot hold the finish (docs/SPEC-CARD-CONTRACT.md, the finish)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		release()
		logf.Close()
		return nil, err
	}
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	c := &nativeChild{card: p.Card, logPath: logPath, results: results, job: job, done: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		release()
		logf.Close()
		// ignored: the pid file of a child that has ended; a leftover names a dead pid, which the next start overwrites
		_ = safepath.RemoveUnder(r.slots, pidPath)
		close(c.done)
	}()
	r.started(name)
	return c, nil
}

// route is what one launch runs on: the packet's route (the card's model, budget and
// deadline, as the deal drew them), each part it leaves empty from the member's
// override; a launch with no model, budget or deadline from either is refused.
func (r *nativeRunner) route(p member.Packet) (model, tokens string, deadline time.Duration, err error) {
	model, tokens, deadline = p.Model, p.Tokens, time.Duration(p.Deadline)*time.Second
	if model == "" {
		model = r.model
	}
	if tokens == "" {
		tokens = r.tokens
	}
	if deadline <= 0 {
		deadline = r.deadline
	}
	switch {
	case model != "" && tokens != "" && deadline > 0:
	case p.Route != "":
		return "", "", 0, fmt.Errorf("card %s's route %s (model %q) names no tokens or deadline and this member has no override for them: give the card's model: pin its tokens: and deadline: lines, or start the member with --tokens and --deadline", p.Card, p.Route, p.Model)
	default:
		return "", "", 0, fmt.Errorf("card %s has no route (model %q tokens %q deadline %s) and this member no override for what is missing: add the tier's route with nova-config route add, or start the member with --model, --tokens and --deadline", p.Card, model, tokens, deadline)
	}
	return model, tokens, deadline, nil
}

// launchName is the name of one launch: the card at its generation (a read:
// its attempt) in its epoch; a card dealt again is another launch.
func launchName(p member.Packet) string {
	// a path element, built by concatenation (the card id passed safepath.NameOK)
	e := ".e" + strconv.FormatUint(p.Epoch, 10)
	if p.Kind == "read" {
		return p.Card + ".a" + strconv.Itoa(p.Attempt) + e
	}
	return p.Card + ".g" + strconv.Itoa(p.Gen) + e
}

// livePID is the pid a pid file names when that process is alive, else 0.
func livePID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || !processAlive(pid) {
		return 0
	}
	return pid
}

// processAlive is whether a signal 0 reaches the process.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

type nativeChild struct {
	card, logPath, results, job string
	done                        chan struct{}
	err                         error
	once                        sync.Once
	result                      member.Result
}

func (c *nativeChild) Done() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

var nativeRC = regexp.MustCompile(`\bNATIVE (\S+) .*\brc=(-?\d+)\b.*\bharness=(\S+)`)

// nativeSpent is the NATIVE line's wall seconds and budget word, the launch's usage.
var nativeSpent = regexp.MustCompile(`\bNATIVE \S+ .*\bwall=([0-9.]+s)\b.*\bbudget=(\S+)`)

// nativeEnd is how a launch that did not finish ended, from its log: the provider's
// failure (a NATIVE PROVIDER- line), the budget (stopped=), the deadline (rc=-1 with
// neither, and no TERM from outside), else "".
func nativeEnd(log []byte) string {
	switch {
	case nativeProvider.Match(log):
		return member.EndProvider
	case nativeStopped.Match(log):
		return member.EndBudget
	case nativeKilled.Match(log) && !nativeTermed.Match(log):
		return member.EndDeadline
	}
	return ""
}

// nativeProviderWhy is the reason of a run the provider failed (nativeprovider.go's
// PROVIDER-FAIL line): the rest of the line after reason=. The 5xx hand-back's own line
// (swarm.Handback, PROVIDER-5XX) names none, and is its own reason.
var (
	nativeProviderWhy = regexp.MustCompile(`(?m)\bNATIVE PROVIDER-FAIL \S.* reason=(.+)$`)
	nativeHandback    = regexp.MustCompile(`(?m)\bNATIVE (PROVIDER-5XX \S.*)$`)
)

// providerReason is why the provider failed the run, from native's log: the PROVIDER-FAIL
// line's reason, else the 5xx hand-back's line (`provider: PROVIDER-5XX label=... ref=...`),
// else "".
func providerReason(log []byte) string {
	if m := nativeProviderWhy.FindSubmatch(log); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	if m := nativeHandback.FindSubmatch(log); m != nil {
		return "provider: " + strings.TrimSpace(string(m[1]))
	}
	return ""
}

// nativeStageFail is native's STAGE FAIL line's reason: the launch refused at staging,
// before any child ran (native.go; tla/CardContract.tla, StageRefused).
var nativeStageFail = regexp.MustCompile(`(?m)^STAGE FAIL .*\breason=(.+)$`)

var (
	nativeProvider = regexp.MustCompile(`\bNATIVE PROVIDER-`)
	nativeStopped  = regexp.MustCompile(`\bNATIVE \S+ .*\bstopped=`)
	nativeKilled   = regexp.MustCompile(`\bNATIVE \S+ .*\brc=-1\b`)
	nativeTermed   = regexp.MustCompile(`\breason=terminated\b`)
)

// Result reads how the child ended: the NATIVE line's rc and harness word, and
// the finish the gh shim recorded in the job, else the newest RESULT.md under
// the card's results, in the contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3). The head is the result's, else the last push the git shim
// recorded in the job, else an older result's `rev:` line; a read's verdict and
// report fall back to the older shape too. A launch refused at staging (its STAGE FAIL
// line) ran no child: it ends EndStaging with the line's reason, and nothing else is read.
func (c *nativeChild) Result() member.Result {
	c.once.Do(func() {
		ran := false
		var end, usage, provider string
		if b, err := os.ReadFile(c.logPath); err == nil {
			if m := nativeStageFail.FindSubmatch(b); m != nil {
				// refused at staging: no child ran, so no result of this launch exists to read
				why := strings.TrimPrefix(strings.TrimSpace(string(m[1])), member.EndStaging+": ")
				c.result = member.Result{End: member.EndStaging, Staging: why, Report: "no child ran (see " + c.logPath + ")"}
				return
			}
			if m := nativeRC.FindSubmatch(b); m != nil {
				ran = string(m[1]) == "OK" && string(m[2]) == "0" && string(m[3]) == "ok"
			}
			if m := nativeSpent.FindSubmatch(b); m != nil {
				usage = "wall=" + string(m[1]) + " budget=" + string(m[2])
			}
			end = nativeEnd(b)
			provider = providerReason(b)
		}
		path := newestResult(c.results)
		var raw []byte
		if path != "" {
			raw, _ = os.ReadFile(path) // ignored: an unreadable result reads as no result, which the finish judges failed
		}
		// the shim's record of gh pr create or gh pr review is the finish when there is
		// one; a RESULT.md the child wrote as well rides in its body for the readers
		cr, shimmed := cardcontract.ReadFinish(c.job)
		if !shimmed {
			cr = typedrec.ParseCardResult(raw)
		} else if own := strings.TrimSpace(string(raw)); own != "" && !cardcontract.IsFinish(c.job, raw) {
			// native published the child's own RESULT.md beside the finish: it rides in the body
			cr.Body = strings.TrimSpace(cr.Body + "\n\n## RESULT.md\n\n" + own)
		}
		head, verdict, report := cr.Head, cr.Verdict, cr.Report
		if head == "" {
			_, head = cardcontract.LastPushed(c.job)
		}
		if !cr.Shaped {
			lh, lv, lr := readResult(path)
			if head == "" {
				head = lh
			}
			if verdict == "" {
				verdict = lv
			}
			if report == "" {
				report = lr
			}
		}
		if report == "" {
			if ran {
				report = "finished; the child published no one-line report"
			} else {
				report = "the child ended without a result (see " + c.logPath + ")"
			}
		}
		c.result = member.Result{Ran: ran, OK: ran, Shaped: cr.Shaped, Verdict: verdict, Head: head, Report: report, Title: cr.Title, Body: cr.Body, End: end, Usage: usage, Provider: provider}
	})
	return c.result
}

// frameOf is a launch's frame (docs/SPEC-CARD-CONTRACT.md layer 1): the repository and
// base the brief's header names, the packet's branch and attempt, and the commit to stage:
// a read's head under read, a later attempt's last pushed head of any earlier attempt
// (sprint.BaseOf, the packet's base_head and base_attempt), else the base's sha.
func frameOf(p member.Packet, model, root string) cardcontract.Frame {
	cb := swarm.ReadCardBase([]byte(p.Brief))
	first, _, _ := strings.Cut(p.Brief, "\n")
	f := cardcontract.Frame{Kind: p.Kind, Card: p.Card, Attempt: p.Attempt, Model: model,
		Repo: cb.Repo, BaseRef: cb.Ref, StageSha: cb.Sha, Branch: p.Branch, Why: p.Why, Finding: p.Finding, Fix: p.Fix, Stage: cb.Stage}
	if len(cb.Stage) > 0 {
		f.Recipes = filepath.Join(root, cardcontract.RecipesName)
	}
	mh, _ := cardhdr.ReadModel(first) // line 1's tier, by the one parser the deal reads it with
	f.Tier = mh.Tier
	if p.Kind == "read" {
		f.Branch, f.ReviewBase = p.WorkBranch, cb.Ref
		if p.WorkBase != "" {
			f.ReviewBase = p.WorkBase
		}
		if typedrec.IsFullSha(p.Head) {
			f.StageSha = p.Head
		}
		return f
	}
	if p.BaseHead != "" {
		f.StageSha, f.PrevHead, f.PrevFrom = p.BaseHead, p.BaseHead, p.BaseFrom
	}
	return f
}

// newestResult is the newest RESULT.md under dir, "" when none.
func newestResult(dir string) string {
	var best string
	var bestT time.Time
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "RESULT.md" && (best == "" || info.ModTime().After(bestT)) {
			best, bestT = path, info.ModTime()
		}
		return nil
	})
	return best
}

// readResult reads a RESULT.md in the shape before the card contract: the head
// from a `rev: <sha>` line, a read's verdict from a `verdict: ok|broken` line,
// the report from the "## One line" section (else the first prose line with no
// colon). It stays because briefs still say that shape: a read brief tells its
// reader "`## Head` with `verdict: ok` or `verdict: broken`, `## One line`", and
// a card whose brief names no repository is never framed, so its child writes
// what its brief says. A work card's finish never rests on it (member.Judge
// wants the contract's shape); a read's verdict and report do, until the briefs
// that say the old shape are gone.
func readResult(path string) (head, verdict, report string) {
	if path == "" {
		return "", "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	inOne := false
	first := "" // the first prose line, the report when there is no One line
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "rev:") && head == "" {
			head = strings.TrimSpace(strings.TrimPrefix(l, "rev:"))
		}
		if strings.HasPrefix(l, "verdict:") && verdict == "" {
			verdict = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "verdict:")))
		}
		if strings.HasPrefix(l, "## ") {
			inOne = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(l, "## ")), "one line")
			continue
		}
		if inOne && l != "" && report == "" {
			report = l
		}
		if !strings.HasPrefix(l, "#") && l != "" && !strings.Contains(l, ":") && first == "" {
			first = l
		}
	}
	if report == "" {
		report = first
	}
	if strings.ContainsAny(head, " \t") || len(head) > 64 {
		head = ""
	}
	return head, verdict, report
}

// envNameRE is an environment variable's name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// splitNames is a comma list of names, blanks dropped.
func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// childKept are the names a card's native child is started with, besides the
// prefixes in childKeptPrefixes and the secrets --pass names: what native, git
// and the harness need, and nothing that carries a credential.
var childKept = map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true, "TERM": true, "USER": true, "LOGNAME": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true}

// childKeptPrefixes are the families of names a native child is started with:
// the locale, the Go toolchain's settings, nova-swarm's own, the XDG
// directories and the opencode harness's settings.
var childKeptPrefixes = []string{"LC_", "GO", "NOVA_SWARM_", "NOVA_TEST_", "XDG_", "OPENCODE_"}

// secretNameRE is a name that carries a credential: never handed to a child
// unless --pass names it, whatever the lists above say.
var secretNameRE = regexp.MustCompile(`(?i)TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH`)

// childEnviron is the environment a card's native child starts with
// (docs/SPEC-CARD-CONTRACT.md, the child's environment): an allowlist, never a
// denylist. A name is kept when it is one of childKept or of a family in
// childKeptPrefixes and carries no credential, or when it is a secret pass
// names (the loop record's nova-secrets keys); everything else is dropped.
func childEnviron(env, pass []string) []string {
	passed := map[string]bool{}
	for _, n := range pass {
		passed[n] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		kept := childKept[name]
		for _, p := range childKeptPrefixes {
			kept = kept || strings.HasPrefix(name, p)
		}
		if passed[name] || (kept && !secretNameRE.MatchString(name)) {
			out = append(out, kv)
		}
	}
	return out
}

// localProviders are the providers a harness reaches with no key: a model on
// this machine.
var localProviders = map[string]bool{"ollama": true, "lmstudio": true, "llamacpp": true, "local": true}

// passNote is the one NOTE line a member prints at its start when no secret is
// handed to its children (--pass empty, no worker secret, no --auth file) and
// its model is not a local one: the children start with no provider key and
// fail at the provider (docs/SPEC-CARD-CONTRACT.md, the child's environment).
func passNote(model string, pass []string, auth string) string {
	provider, _, _ := strings.Cut(model, "/")
	if len(pass) > 0 || auth != "" || localProviders[strings.ToLower(provider)] {
		return ""
	}
	return "NOTE member --pass names no secret: a child's harness that reads its provider key from the environment starts without it and fails at the provider; run: nova-swarm member ... --pass <KEY> (the loop record's nova-secrets keys)"
}
