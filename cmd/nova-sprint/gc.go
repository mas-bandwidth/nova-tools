package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// gc is the verb that reclaims the machinery's scratch (sprint.GC is the rule;
// docs/SPEC-SPRINT.md section 1, "gc"): on the machine it runs on, or, with --machine,
// on that machine through the fleet runner (internal/bench's ssh), which runs the same
// verb there. The sprint's run loop runs it on every machine (gcLoop, sprint.GCDue).

func init() {
	verbClasses["gc"] = classMachine
	// it works on the directories of the machine it runs on (or the one --machine names)
	notServed = append(notServed, "gc")
	verbExit["gc"] = "exit codes: 0 GC OK, 1 a removal or a read failed (GC FAILED names each; the summary is GC INCOMPLETE) or --machine did not answer, 2 usage"
	verbEffect["gc"] = "local write: removes, on this machine (or --machine's, through the fleet runner), the job directories of finished or absent lanes, a landed or dropped card's job directory after one hour whatever its git state (class landed), reader checkouts of recorded findings, lander worktrees and bench directories past --max-age, and trims the go caches to their cap, and with --judgment writes one open judgment through the sprint store (the disk guard's below-stop seat judgment); never a path under no known scratch root, never a clone with work that is nowhere else except a landed or dropped card's job; --dry-run removes nothing; --class landed runs only that class"
}

// gcRunner runs one line on a machine through the fleet runner: its exit status, and an
// error when the runner itself did not start.
type gcRunner func(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error)

// gcRemote is the fleet runner: internal/bench's ssh.
func gcRemote(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	return bench.Exec{}.Shell(ctx, host, line, stdout, stderr)
}

// gcRemoteBin is how the remote line names nova-sprint: the go install directories first,
// which a non-login ssh leaves off PATH.
const gcRemoteBin = `PATH="$HOME/go/bin:$HOME/bin:/usr/local/bin:$PATH" nova-sprint`

func (a *app) cmdGC(args []string, stdout, stderr io.Writer) int {
	const name = "gc"
	fs, c := a.verbSetup(name)
	machine := fs.String("machine", "", "run gc on this machine (a host name ssh reaches) through the fleet runner, rather than on this one")
	dry := fs.Bool("dry-run", false, "print every removal with the bytes it would free, and remove nothing")
	aiRoot := fs.String("ai-root", "", "the AI root the working directories are under (else NOVA_AI_ROOT, else ~/ai, else the one the home's <name>-working links name); an absolute path")
	maxAge := fs.String("max-age", "2d", "how old a bench directory, a lander worktree or a job no runner names is before it goes: days (2d) or a Go duration (36h)")
	class := fs.String("class", "", "which class to run: empty is every class, including landed; landed is only the jobs of landed or dropped cards")
	judgment := fs.String("judgment", "", "write one open judgment with this text through the sprint store (the disk guard's below-stop judgment) and remove nothing; wants a store address")
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) > 0:
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	if *judgment != "" {
		return a.judgeVolume(*judgment, stdout, stderr)
	}
	age, err := gcAge(*maxAge)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *aiRoot != "" && !filepath.IsAbs(*aiRoot) {
		return refuse(stderr, name, "--ai-root wants an absolute path, got "+oneline.Escape(*aiRoot))
	}
	only := *class
	if only != "" && only != sprint.GCLandedName {
		return refuse(stderr, name, "--class wants landed or nothing, got "+oneline.Escape(only))
	}
	if m := *machine; m != "" && !gcIsLocal(m) {
		if err := bench.CheckHost(m); err != nil {
			return refuse(stderr, name, "--machine: "+err.Error())
		}
		return gcOn(context.Background(), gcRemote, m, *dry, *maxAge, *aiRoot, stdout, stderr)
	}
	res := a.gcLocal(*dry, age, *aiRoot, only)
	if c.json {
		facts := map[string]any{"freed": res.Freed, "volume": res.Volume, "failed": res.Failed, "dry_run": res.Dry, "classes": res.Classes, "lines": orEmpty(res.Detail)}
		line := res.Lines()[len(res.Lines())-1]
		if res.Failed > 0 {
			facts["status"], facts["exit"] = "incomplete", 1
		}
		sayOK(stdout, true, name, line, facts)
	} else {
		for _, l := range res.Lines() {
			fmt.Fprintln(stdout, l)
		}
	}
	if res.Failed > 0 {
		return 1
	}
	return 0
}

// judgeVolume writes one open judgment (sprint.VolumeJudgment) through the
// sprint store, the disk guard's below-stop seat judgment: the machine held its
// own deals, and the judgment names the volume and its free figure. It removes
// nothing. A run with no store address refuses, as a judgment with no store is
// nowhere.
func (a *app) judgeVolume(text string, stdout, stderr io.Writer) int {
	const name = "gc"
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	} else if short, _, dotted := strings.Cut(host, "."); dotted {
		host = short
	}
	addr := firstEnv(a.getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR")
	if strings.TrimSpace(addr) == "" {
		fmt.Fprintf(stderr, "%s gc: GC FAILED: no store address to write the judgment to; run: NOVA_SPRINT_REDIS=<address> nova-sprint gc --judgment '<text>'\n", prog)
		return 1
	}
	st, err := a.store(common{verb: "gc", redis: addr, actor: a.getenv("NOVA_SPRINT_ACTOR"), epoch: -1})
	if err != nil {
		return refuse(stderr, name, "the sprint's store could not be read: "+err.Error())
	}
	if _, err := st.Run(context.Background(), store.Step{
		Verb:  "gc",
		Actor: sprint.MachineActor,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			if n, ok := sprint.VolumeJudgment(s, host, text); ok {
				return sprint.Plan{Notes: []sprint.Note{n}}
			}
			return sprint.Plan{}
		},
	}); err != nil {
		return refuse(stderr, name, "the judgment was not written: "+err.Error())
	}
	fmt.Fprintf(stdout, "GC OK judgment=%s\n", oneline.Escape(text))
	return 0
}

// gcAge reads --max-age: whole days (2d) or a Go duration, above 0.
func gcAge(s string) (time.Duration, error) {
	var d time.Duration
	var err error
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		days, err = strconv.Atoi(n)
		d = time.Duration(days) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--max-age wants days (2d) or a Go duration (36h) above 0, got %s", oneline.Escape(s))
	}
	return d, nil
}

// gcIsLocal says m names this machine: localhost, or its host name, whole or short.
func gcIsLocal(m string) bool {
	if m == "localhost" {
		return true
	}
	h, err := os.Hostname()
	if err != nil {
		return false
	}
	short, _, _ := strings.Cut(h, ".")
	return m == h || m == short
}

// gcLine is the remote line of gc on another machine: the same verb, its flags carried
// (--ai-root when given; without it the machine finds its own, as gcLocal does).
func gcLine(dry bool, maxAge, aiRoot string) string {
	line := gcRemoteBin + " gc --max-age " + bench.Quote(maxAge)
	if aiRoot != "" {
		line += " --ai-root " + bench.Quote(aiRoot)
	}
	if dry {
		line += " --dry-run"
	}
	return line
}

// gcOn runs gc on machine through run; its lines are the remote verb's.
func gcOn(ctx context.Context, run gcRunner, machine string, dry bool, maxAge, aiRoot string, stdout, stderr io.Writer) int {
	code, err := run(ctx, machine, gcLine(dry, maxAge, aiRoot), stdout, stderr)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "%s gc: GC FAILED machine=%s: the fleet runner did not start: %s\n", prog, oneline.Escape(machine), oneline.Err(err))
		return 1
	case code == bench.NoAnswer:
		fmt.Fprintf(stderr, "%s gc: GC FAILED machine=%s: did not answer (ssh exit %d); nothing was removed there\n", prog, oneline.Escape(machine), code)
		return 1
	case code != 0:
		return 1
	}
	return 0
}

// gcLocal is one pass on this machine: the home, the AI root (aiRoot, else NOVA_AI_ROOT,
// else ~/ai, else the one the home's <name>-working links name: sprint.GCAIRoot), the
// bench root (~/nova-bench) and land's clones.
func (a *app) gcLocal(dry bool, age time.Duration, aiRoot, only string) sprint.GCResult {
	home, ai := a.gcRoots(aiRoot)
	benchRoot := ""
	if home != "" {
		benchRoot = filepath.Join(home, "nova-bench")
	}
	land, err := a.landRoot()
	if err != nil {
		land = ""
	}
	cl := &friendClean{}
	req := sprint.GCReq{
		Home: home, AIRoot: ai, BenchRoot: benchRoot, LandRoot: land,
		MaxAge: age, Now: a.now(), Dry: dry,
		Dirty:     cl.dirty,
		Worktrees: func(clone string) []string { return gcWorktrees(clone, dry) },
		Volume:    sprint.GCVolumeUse,
	}
	cards := a.cardsByID()
	if only == sprint.GCLandedName {
		return sprint.GCLanded(req, cards)
	}
	return sprint.AppendGC(sprint.GC(req), sprint.GCLanded(req, cards))
}

// cardsByID reads the sprint's cards when this process was given a store
// (NOVA_SPRINT_REDIS or NOVA_REDIS_ADDR). No address is no read: gc says so
// and removes no landed job, and a seat login is not opened from here. A store
// that does not answer is the same. Placed cards come from the work and fleet
// tables; a card that is not placed (a dropped one) is read when a job names it.
func (a *app) cardsByID() func(string) *sprint.Card {
	addr := firstEnv(a.getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR")
	if strings.TrimSpace(addr) == "" {
		return nil
	}
	st, err := a.store(common{verb: "gc", redis: addr, actor: a.getenv("NOVA_SPRINT_ACTOR"), epoch: -1})
	if err != nil {
		return nil
	}
	return cardsFrom(st)
}

func cardsFrom(st *store.Store) func(string) *sprint.Card {
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet}, nil)
	if err != nil || snap == nil {
		return nil
	}
	by := map[string]*sprint.Card{}
	for _, t := range []*sprint.Table{snap.Fleet, snap.Work} {
		if t == nil {
			continue
		}
		for _, c := range t.Cards() {
			if c != nil && c.ID != "" {
				by[c.ID] = c
			}
		}
	}
	missed := map[string]*sprint.Card{}
	return func(id string) *sprint.Card {
		if c, ok := by[id]; ok {
			return c
		}
		if c, ok := missed[id]; ok {
			return c
		}
		found := cardRecord(ctx, st, id)
		missed[id] = found
		return found
	}
}

func cardRecord(ctx context.Context, st *store.Store, id string) *sprint.Card {
	var found *sprint.Card
	for _, logical := range []string{sprint.Fleet, sprint.Work} {
		recs, err := st.Records(ctx, logical, []string{id})
		if err != nil {
			continue
		}
		for _, c := range recs {
			if c != nil && c.ID == id {
				found = c
			}
		}
	}
	return found
}

// gcRoots is this machine's home and the AI root as given (aiRoot, else NOVA_AI_ROOT,
// else ~/ai); sprint.GC falls back to the home's links when that is no directory.
func (a *app) gcRoots(aiRoot string) (home, ai string) {
	home = a.getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir() // ignored: no home is no root, and the pass finds nothing
	}
	ai = aiRoot
	if ai == "" {
		ai = a.getenv("NOVA_AI_ROOT")
	}
	if ai == "" && home != "" {
		ai = filepath.Join(home, "ai")
	}
	return home, ai
}

// gcWorktrees is a clone's linked worktrees (git worktree list, the main one left out),
// after git forgets the ones whose directory is gone (prune; never on a dry run).
func gcWorktrees(clone string, dry bool) []string {
	o := gitrun.Options{C: clone, OwnRepo: true}
	ctx := context.Background()
	if !dry {
		// ignored: a stale entry is pruned by the next pass
		_, _ = gitrun.Output(ctx, o, "-c", "core.fsmonitor=false", "worktree", "prune")
	}
	out, err := gitrun.Output(ctx, o, "-c", "core.fsmonitor=false", "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var wts []string
	for i, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && i > 0 {
			wts = append(wts, p)
		}
	}
	return wts
}

// cmdRunGC is run with the gc loop beside it: a run that ticks also runs gc on every
// machine (gcLoop). A test's run never starts it.
func (a *app) cmdRunGC(args []string, stdout, stderr io.Writer) int {
	if !testing.Testing() && !slices.ContainsFunc(args, func(s string) bool { return s == "-h" || s == "-help" || s == "--help" }) {
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		go a.gcLoop(ctx, stdout)
	}
	return a.cmdRun(args, stdout, stderr)
}

// gcLoopFirst is how long the loop waits before its first round: a run refused at its
// flags has exited by then.
const gcLoopFirst = 2 * time.Minute

// gcLoopEvery is how often the loop reads what is due.
const gcLoopEvery = time.Minute

// gcLoop runs gc on this machine and on every machine row of the config, as sprint.GCDue
// says: each once an hour and on a volume at the alarm. Each run's class lines and summary
// are said, each prefixed with the time and the machine.
func (a *app) gcLoop(ctx context.Context, stdout io.Writer) {
	local, _ := os.Hostname() // ignored: an unnamed machine is "local"
	local, _, _ = strings.Cut(local, ".")
	if local == "" {
		local = "local"
	}
	known := map[string]*sprint.GCMachine{}
	probed := map[string]time.Time{}
	saidInventory := false
	wait := gcLoopFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = gcLoopEvery
		names := []string{local}
		if inv, err := a.inventory(ctx, ""); err != nil {
			if !saidInventory {
				fmt.Fprintf(stdout, "%s GC the config's machines were not read (%s): gc runs on this machine alone until they are\n", a.now().Format("15:04:05"), oneline.Err(err))
				saidInventory = true
			}
		} else {
			saidInventory = false
			for _, m := range inv {
				if !gcIsLocal(m.Machine) && bench.CheckHost(m.Machine) == nil && !slices.Contains(names, m.Machine) {
					names = append(names, m.Machine)
				}
			}
		}
		var ms []sprint.GCMachine
		for _, n := range names {
			k := known[n]
			if k == nil {
				k = &sprint.GCMachine{Name: n, Volume: -1}
				known[n] = k
			}
			now := a.now()
			if now.Sub(probed[n]) >= sprint.GCProbeEvery {
				probed[n] = now
				var out bytes.Buffer
				if n == local {
					fmt.Fprintln(&out, a.gcLocalVolume())
				} else {
					// ignored: a machine that does not answer keeps its last reading
					_, _ = gcRemote(ctx, n, sprint.GCProbeLine, &out, io.Discard)
				}
				if v, ok := sprint.GCVolumeIn(out.String()); ok {
					k.Volume = v
				}
			}
			ms = append(ms, *k)
		}
		for _, run := range sprint.GCDue(ms, a.now()) {
			var out bytes.Buffer
			if run.Machine == local {
				for _, l := range a.gcLocal(false, sprint.GCMaxAge, "", "").Lines() {
					fmt.Fprintln(&out, l)
				}
			} else {
				// ignored: the remote verb's lines say its failure; ssh's own is its exit
				_, _ = gcRemote(ctx, run.Machine, gcLine(false, "2d", ""), &out, &out)
			}
			k := known[run.Machine]
			k.Last = a.now()
			if v, ok := sprint.GCVolumeIn(out.String()); ok {
				k.Volume = v
			}
			said := false
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(l, "GC ") && !gcDetail(l) {
					fmt.Fprintf(stdout, "%s GC machine=%s why=%s: %s\n", a.now().Format("15:04:05"), oneline.Escape(run.Machine), oneline.Escape(run.Why), l)
					said = true
				}
			}
			if !said {
				fmt.Fprintf(stdout, "%s GC machine=%s why=%s: GC FAILED no answer: %s\n", a.now().Format("15:04:05"), oneline.Escape(run.Machine), oneline.Escape(run.Why), oneline.Escape(oneline.Cap(out.String(), 300)))
			}
		}
	}
}

// gcDetail says l is a removal's or a keep's line, which the loop leaves to the verb.
func gcDetail(l string) bool {
	for _, p := range []string{"GC REMOVED ", "GC WOULD-REMOVE ", "GC KEPT "} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// gcLocalVolume is this machine's fullest volume as a summary line GCVolumeIn reads.
func (a *app) gcLocalVolume() string {
	home, ai := a.gcRoots("")
	use := -1
	for _, d := range []string{home, filepath.Join(home, "nova-bench"), sprint.GCAIRoot(home, ai)} {
		if d == "" {
			continue
		}
		if v, err := sprint.GCVolumeUse(d); err == nil && v > use {
			use = v
		}
	}
	if use < 0 {
		return ""
	}
	return fmt.Sprintf("GC OK freed=0 volume=%d%%", use)
}
