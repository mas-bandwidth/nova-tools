package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
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
	verbEffect["gc"] = "local write: removes, on this machine (or --machine's, through the fleet runner), the job directories of finished or absent lanes, reader checkouts of recorded findings, lander worktrees and bench directories past --max-age, sweeps the bench lane directories whose lane is gone, and trims the go caches to their cap, and says a bench whose /tmp is nearly full, naming it and its largest directories; never a path under no known scratch root, never a clone with work that is nowhere else; --dry-run removes nothing"
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
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) > 0:
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	age, err := gcAge(*maxAge)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *aiRoot != "" && !filepath.IsAbs(*aiRoot) {
		return refuse(stderr, name, "--ai-root wants an absolute path, got "+oneline.Escape(*aiRoot))
	}
	if m := *machine; m != "" && !gcIsLocal(m) {
		if err := bench.CheckHost(m); err != nil {
			return refuse(stderr, name, "--machine: "+err.Error())
		}
		code := gcOn(context.Background(), gcRemote, m, *dry, *maxAge, *aiRoot, stdout, stderr)
		if a.sweepBenchLanes(c, m, *dry, stdout, stderr) && code == 0 {
			code = 1
		}
		return code
	}
	res := a.gcLocal(*dry, age, *aiRoot)
	// the killed lane sweep and the /tmp judgment are this pass too: the tick runs this
	// verb on every machine, so a lane killed before its own remove has its directory
	// removed here, and a bench whose /tmp is over the mark raises one judgment
	// (docs/SPEC-SPRINT.md section 18, bench lanes). --json keeps its one object: the
	// sweep's lines go in the pass's own lines.
	if name := gcLocalName(); name != "" {
		var sweep bytes.Buffer
		dst := io.Writer(&sweep)
		if !c.json {
			dst = stdout
		}
		if a.sweepBenchLanes(c, name, *dry, dst, stderr) {
			res.Failed++
		}
		if c.json {
			for _, l := range strings.Split(strings.TrimRight(sweep.String(), "\n"), "\n") {
				if l != "" {
					res.Detail = append(res.Detail, l)
				}
			}
		}
	}
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

// gcLocalName is this machine's name as the fleet's machine rows name it: the short host
// name, empty when there is none.
func gcLocalName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	short, _, _ := strings.Cut(h, ".")
	return short
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

// sweepBenchLanes sweeps machine's bench lane directories whose lane is gone, and persists
// the bench's /tmp judgment when it is nearly full: a machine with a live Go lane holder is
// running something and is left alone; one with no holder has no live lane, so every
// directory under nova-bench/lanes there is a killed lane's (docs/SPEC-SPRINT.md section 18,
// bench lanes). The sweep and the /tmp judgment are the check beside the mechanism: a lane
// removes its own directory whatever the verdict; the sweep is for the lane killed before its
// remove, and the judgment names a bench whose /tmp is over BenchTmpOverPct and its largest
// directories. It reports whether anything failed; with dry set it changes nothing.
func (a *app) sweepBenchLanes(c *common, machine string, dry bool, stdout, stderr io.Writer) (failed bool) {
	sh := gcSweepShell(machine)
	st, serr := a.store(*c)
	var tmpOut bytes.Buffer
	tmpCtx, cancel := context.WithTimeout(context.Background(), sprint.BenchLaneStep)
	defer cancel()
	if code, err := sh.Shell(tmpCtx, machine, sprint.BenchTmpLine, &tmpOut, &tmpOut); err == nil && code == 0 {
		if tmp, err := sprint.BenchTmpFrom(machine, tmpOut.String()); err == nil {
			a.raiseBenchTmp(st, serr, machine, tmp, dry, stdout, stderr)
		}
	}
	if serr != nil { // ignored: no store, no live lanes to read, nothing swept
		return failed
	}
	rows, err := st.LaneRows(context.Background())
	if err != nil { // ignored: lanes that cannot be read are not swept away
		return failed
	}
	for _, r := range rows {
		if r.Machine == machine && r.Kind == sprint.LaneGo && len(r.Held) > 0 {
			return failed // a live lane: nothing swept while it runs
		}
	}
	swept, err := sprint.SweepBenchLanes(context.Background(), sh, machine, nil, dry)
	for _, d := range swept {
		verb := "GC REMOVED"
		if dry {
			verb = "GC WOULD-REMOVE"
		}
		fmt.Fprintf(stdout, "%s class=bench-lanes path=%s why=%s\n", verb, oneline.Escape(d), "its lane is gone")
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s gc: GC FAILED machine=%s: %s\n", prog, oneline.Escape(machine), oneline.Err(err))
		return true
	}
	return failed
}

// raiseBenchTmp writes the one open judgment a bench whose /tmp is over BenchTmpOverPct
// raises, naming it and its largest directories; with dry it says what it would write and
// writes nothing. With no store the line is said and not kept. One judgment is open a bench
// while it stands: BenchTmpJudgment reads the open judgments the store holds.
func (a *app) raiseBenchTmp(st *store.Store, serr error, machine string, tmp sprint.BenchTmp, dry bool, stdout, stderr io.Writer) {
	var open []sprint.Open
	if serr == nil {
		open, _ = st.B.OpenNotes(context.Background())
	}
	n, ok := sprint.BenchTmpJudgment(&sprint.Snapshot{Now: a.now(), Open: open}, "gc", tmp)
	if !ok {
		return
	}
	if dry {
		fmt.Fprintf(stdout, "GC WOULD-NOTE %s\n", oneline.Escape(n.What))
		return
	}
	if serr != nil { // ignored: no store holds the judgment, so it is said and not kept
		fmt.Fprintf(stdout, "GC NOTE %s\n", oneline.Escape(n.What))
		return
	}
	if _, err := st.Run(context.Background(), store.NoteStep("gc", n)); err != nil {
		fmt.Fprintf(stderr, "%s gc: GC FAILED machine=%s: the /tmp judgment was not written: %s\n", prog, oneline.Escape(machine), oneline.Err(err))
		return
	}
	fmt.Fprintf(stdout, "GC NOTE %s\n", oneline.Escape(n.What))
}

// gcSweepShell is the shell the lane sweep runs through on machine: this machine's own shell
// when it is local (a bench sweeps its own lanes; no ssh to itself), the fleet runner's ssh
// otherwise.
func gcSweepShell(machine string) sprint.BenchShell {
	if gcIsLocal(machine) {
		return gcLocalShell{}
	}
	return gcRemoteShell{}
}

// gcRemoteShell is the fleet runner's ssh.
type gcRemoteShell struct{}

func (gcRemoteShell) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	return gcRemote(ctx, host, line, stdout, stderr)
}

// gcLocalShell runs one line on this machine through the login shell.
type gcLocalShell struct{}

func (gcLocalShell) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	cmd := subproc.Long(ctx, "sh", "-c", line)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// gcLocal is one pass on this machine: the home, the AI root (aiRoot, else NOVA_AI_ROOT,
// else ~/ai, else the one the home's <name>-working links name: sprint.GCAIRoot), the
// bench root (~/nova-bench) and land's clones.
func (a *app) gcLocal(dry bool, age time.Duration, aiRoot string) sprint.GCResult {
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
	return sprint.GC(sprint.GCReq{
		Home: home, AIRoot: ai, BenchRoot: benchRoot, LandRoot: land,
		MaxAge: age, Now: a.now(), Dry: dry,
		Dirty:     cl.dirty,
		Worktrees: func(clone string) []string { return gcWorktrees(clone, dry) },
		Volume:    sprint.GCVolumeUse,
	})
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
				for _, l := range a.gcLocal(false, sprint.GCMaxAge, "").Lines() {
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
