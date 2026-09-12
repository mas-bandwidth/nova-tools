package swarm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE DISPATCHER.
//
// One command spins a swarm up and one line reads it down. What this loop is careful about
// is the three ways the prototype lost track of a worker: a slot found by scanning a map
// that another code path was mutating, a slot whose child survived the kill and was reused
// on the next iteration, and a dispatcher whose slot map lived in its own memory and died
// with it. Here the slot files on disk are the only authority, a surviving child's slot is
// retired for the rest of the run, and a dispatcher that starts over a pool with slot files
// present adopts, reclaims or quarantines every one of them BEFORE it claims a task.

// RunInput is one dispatcher run.
type RunInput struct {
	Pool           *Pool
	Worker         Worker
	Key            string
	Workers        int
	Hours          float64
	Max            int
	LaunchTimeout  time.Duration
	UsageInterval  time.Duration
	Backoff        time.Duration // the wait before retrying a 429; zero takes the default
	Stdout, Stderr io.Writer
	Now            func() time.Time
	Supervisor     string // this binary, re-invoked as `supervise`
	WorkerFile     string // the worker description, handed on to the supervisor
	// THE LAUNCH SEAM (docs/SPEC-SANDBOX.md, the dispatcher caller). Sandbox is the
	// resolved nova-sandbox binary every job runs inside. NoSandbox is rule 11's one loud
	// workaround, typed by a person: it runs the jobs with no OS containment and says so
	// once per job. The two are exclusive and the verb refuses both at once.
	Sandbox   string
	NoSandbox bool
}

// WorkerCap is the ceiling on --workers (Glenn, 2026-09-10). A request above it is a
// REFUSAL and not a silent clamp: the prototype prints a note and clamps, and a caller who
// asked for 200 workers has a belief about throughput that a note at the top of a log does
// not correct.
const WorkerCap = 64

// DefaultLaunchTimeout is how long the runner waits for a supervisor to identify itself. A
// tool property like a timeout, not a fact about anybody's pool.
const DefaultLaunchTimeout = 10 * time.Second

// running is one job this dispatcher is watching.
type running struct {
	sc       Sidecar
	slot     int
	nonce    string
	jobDir   string
	started  time.Time
	deadline time.Duration
	adopted  bool
	notes    int
}

// Run is the dispatcher. It returns the exit code.
func Run(in RunInput) int {
	p := in.Pool
	out, errOut := in.Stdout, in.Stderr
	now := in.Now

	fmt.Fprintf(out, "RUN POOL workers=%d hours=%s worker=%s model=%s pool=%s\n",
		in.Workers, trimFloat(in.Hours), oneline.Field(in.Worker.Name), oneline.Field(in.Worker.Model), oneline.Field(p.Dir))

	// THE PROBE, ONCE, BEFORE THE FIRST WORKER (SPEC-SANDBOX rule 10 and test 23). It
	// costs a process and it answers a question about the MACHINE, not about a job, so it
	// runs here rather than per task -- and a machine that cannot prove its wall starts no
	// worker at all. `--no-sandbox` is the one workaround and it skips the probe, because
	// there is then nothing to prove.
	if !in.NoSandbox {
		if reason, text := SandboxGate(in.Sandbox, p.Dir, in.Worker.KeyFile); reason != "" {
			fmt.Fprintf(errOut, "RUN REFUSED reason=%s: %s\n", oneline.Field(reason), oneline.Escape(text))
			return 2
		}
	}

	said := false // anything that makes this run exit 1
	// TWO DIFFERENT THINGS, counted apart. `quarantined` is rule 17's: a SLOT FILE this
	// dispatcher refuses to decide, which SPEC-SWARM.md:541 says makes the run exit 1.
	// `retired` is rule 11's aftermath: a slot taken out of the map for the rest of this
	// run because a survivor may still be writing in its data home. The spec has no
	// sentence for the second, and the PR body proposes one.
	quarantined := map[int]bool{}
	retired := map[int]bool{}
	watching := map[int]*running{}

	// The counters RUN OK prints are declared before the start-up pass, because that pass
	// finishes jobs too: a recovered job lands in done/ or failed/ and belongs in the
	// numbers that describe the pool afterwards (read 4, F2).
	started, done, failed, killed := 0, 0, 0, 0
	recovered := 0
	launchFailed := 0

	// The start-up pass, before a single pending task is claimed.
	slots, bad, err := p.SlotNumbers()
	if err != nil {
		fmt.Fprintf(errOut, "RUN REFUSED: the slot directory could not be read: %s\n", oneline.Escape(redactedReason(err)))
		return 2
	}
	for _, name := range bad {
		said = true
		fmt.Fprintf(out, "RUN QUARANTINE slot=- id=-: the slot directory holds %s, which is not a slot file\n", oneline.Field(name))
	}
	for _, n := range slots {
		d := p.Decide(n)
		switch d.Kind {
		case DecideAdopt:
			sc, _ := p.ReadSidecar(Running, d.File.Job)
			started := parseStamp(d.File.LaunchedAt, now())
			r := &running{sc: sc, slot: n, nonce: d.File.Nonce, jobDir: d.File.JobDir, started: started,
				deadline: taskDeadline(sc, in.Worker), adopted: true}
			watching[n] = r
			fmt.Fprintf(out, "RUN ADOPT id=%s slot=%d pid=%d started=%s remaining=%s\n",
				oneline.Field(d.File.Job), n, d.File.Pid, oneline.Field(d.File.LaunchedAt),
				trimDuration(r.deadline-now().Sub(started)))
		case DecideReclaim, DecideUnknown:
			sc, _ := p.ReadSidecar(Running, d.File.Job)
			if sc.ID == "" {
				sc = Sidecar{ID: d.File.Job, RC: -1}
			}
			end := EndUnknown
			rec := ExitRecord{RC: -1}
			if d.Exit != nil {
				rec = *d.Exit
				end = rec.End
				if end == "" {
					end = EndDone
				}
			}
			// RULE 7 ON THE RECOVERY PATH (SPEC-SWARM.md:109-111), VERBATIM: "A worker
			// silent past its deadline is reaped and its job is re-queued once, with
			// `requeued=1` in the new task's sidecar; a job reaped a second time goes to
			// `failed/` with `reaped=2` and is not re-queued again." The rule is about
			// the JOB, not about which dispatcher was alive to see it: a supervisor that
			// reaped its worker while the dispatcher was dead left `end=killed` on
			// exit.json, and this branch finalized it straight into failed/ with a stale
			// `reaped` and no second attempt (read 4, F1). The reap is counted here, once,
			// exactly as `finish` counts it, and BEFORE the files move -- the new task's
			// text is the old task's text, read from where the old task still is.
			if end == EndKilled {
				sc.Reaped++
			}
			// RULE 11 IS ABOUT THE JOB, NOT ABOUT WHICH DISPATCHER WAS ALIVE TO SEE IT
			// (SPEC-SWARM.md:154-156), VERBATIM: a job whose group has a survivor "is
			// quarantined: it moves to `failed/` with `violation=background` in the
			// sidecar, and `triage` does not count it." The supervisor makes that group
			// check inside the group's own process and records the count on exit.json;
			// this branch read the record and ignored `Survivors`, so the identical exit
			// record was quarantined when the dispatcher lived (finish.go) and folded
			// into a coordinator's page when it died (read 6, finding 1).
			if rec.Survivors > 0 && end == EndDone {
				end = EndViolation
			}
			fin, usagePath := in.settle(sc, d.File.JobDir, rec, end, now())
			said = said || end == EndUnknown
			// ITS FILES MOVE AS RULE 12 SAYS (SPEC-SWARM.md:372). Finalize writes the
			// usage file and the report copy; it does not move the job, and this branch
			// never did either -- so a recovered job sat in running/ with no slot and
			// nothing watching it, forever.
			sc.Class, sc.End, sc.RC, sc.Ended = fin.Class, end, rec.RC, Stamp(now())
			sc.Violation = violationWord(end, rec.Survivors)
			if fin.Class == ClassMalformed {
				sc.Malformed = fin.MalformedLine
			}
			dest := destinationFor(end, fin.Class, rec.RC)
			_ = p.WriteSidecar(Running, sc)
			requeued := false
			if end == EndKilled {
				requeued = in.requeue(sc, now())
			}
			_ = p.Claim(sc.ID, Running, dest)
			// A DATA HOME THAT MAY STILL HAVE A WRITER IN IT IS NOT FREE: rule 11's
			// aftermath retires the slot on the live path (finish.go), and a recovered
			// violation is the same fact about the same data home.
			if rec.Survivors > 0 {
				retired[n] = true
			}
			// THE COUNTS ARE THE TRUTH ABOUT THE POOL (SPEC-SWARM.md:608-612), "never
			// about the output". A recovered job lands in done/ or failed/ during THIS
			// pass, and `RUN OK` said `done=0 failed=0` over it because only the main
			// loop touched the counters (read 4, F2). It is counted where it landed, and
			// `recovered=<n>` says how many of the counted jobs this pass recovered
			// rather than started.
			recovered++
			switch {
			case end == EndKilled, end == EndUnverifiable:
				killed++
			case dest == Done:
				done++
			default:
				failed++
			}
			fmt.Fprintf(out, "RUN RECLAIM slot=%d id=%s end=%s dest=%s usage=%s requeued=%t\n",
				n, oneline.Field(sc.ID), oneline.Field(end), oneline.Field(dest), oneline.Field(usagePath), requeued)
			if retired[n] {
				// The slot stays out of the map for the rest of this run; it is not freed.
			} else if err := p.Free(n); err != nil {
				fmt.Fprintf(errOut, "RUN QUARANTINE slot=%d id=%s: the slot file could not be released: %s\n", n, oneline.Field(sc.ID), oneline.Escape(redactedReason(err)))
				quarantined[n] = true
			}
		case DecideUnlaunched:
			sc, _ := p.ReadSidecar(Running, d.File.Job)
			if sc.ID != "" {
				sc.Launch = "unlaunched"
				_ = p.WriteSidecar(Running, sc)
				_ = p.Claim(sc.ID, Running, Pending)
			}
			_ = p.Free(n)
			// ONE GRAMMAR LINE HAS ONE SHAPE (SPEC-SWARM.md:566): `RUN RECLAIM … end=<…>
			// dest=<done|failed|-> usage=<path|->`. The reclaim above prints `dest=`; this
			// one did not, so the same line came out two ways and a reader parsing it by
			// field found the field missing. An unlaunched task goes back to pending/,
			// which is neither done nor failed: the dash the grammar names for exactly that.
			fmt.Fprintf(out, "RUN RECLAIM slot=%d id=%s end=unlaunched dest=%s usage=- requeued=false\n", n, oneline.Field(d.File.Job), Dash)
		default:
			said = true
			quarantined[n] = true
			// A reservation whose launch is unproven is rewritten to `orphaned` with its
			// nonce KEPT -- under slots.lock, after a recheck that it still reads reserved
			// with that nonce, and adopting instead if an identify landed in between.
			if d.File.State == SlotReserved {
				if adopted, sf, err := p.Orphan(n, d.File.Nonce); err == nil && adopted {
					delete(quarantined, n)
					sc, _ := p.ReadSidecar(Running, sf.Job)
					started := parseStamp(sf.LaunchedAt, now())
					watching[n] = &running{sc: sc, slot: n, nonce: sf.Nonce, jobDir: sf.JobDir, started: started,
						deadline: taskDeadline(sc, in.Worker), adopted: true}
					fmt.Fprintf(out, "RUN ADOPT id=%s slot=%d pid=%d started=%s remaining=%s\n",
						oneline.Field(sf.Job), n, sf.Pid, oneline.Field(sf.LaunchedAt), trimDuration(taskDeadline(sc, in.Worker)-now().Sub(started)))
					continue
				}
			}
			fmt.Fprintf(out, "RUN QUARANTINE slot=%d id=%s: %s\n", n, oneline.Field(dashOr(d.File.Job)), oneline.Escape(d.Reason))
		}
	}

	deadline := now().Add(time.Duration(in.Hours * float64(time.Hour)))
	tasks := bounded.Capped(out, in.Max, "RUN", "task", "nova-swarm status --pool "+p.Dir+" --max 0")

	for {
		// Start what can be started, while the dispatcher's own deadline is ahead of us.
		for !now().After(deadline) && !p.Stopped() && len(watching) < in.Workers {
			slot, ok := freeSlot(p, in.Workers, quarantined, retired, watching)
			if !ok {
				break
			}
			sc, text, claimed, err := p.ClaimNext()
			if err != nil || !claimed {
				break
			}
			r, line, code := in.launch(sc, text, slot, quarantined, retired)
			switch code {
			case 0:
				started++
				watching[slot] = r
				tasks.Line(line)
			default:
				said = true
				launchFailed++
				tasks.Line(line)
			}
		}
		if len(watching) == 0 {
			break
		}
		// Poll what is running. The dispatcher waits on pids it started or adopted, and it
		// never matches a process by its command line.
		for slot, r := range watching {
			if alive, over := in.state(r, now()); alive && !over {
				continue
			} else if alive && over {
				// An adopted job's deadline is held from the RECORDED start, by whoever is
				// watching it now.
				sf, err := p.ReadSlot(slot)
				if err == nil {
					Reap(sf.JobPgid, sf.JobStarted, TerminateGrace)
					Reap(sf.Pgid, sf.PidStarted, TerminateGrace)
				}
				continue
			}
			// `recovered=<n>` is every job this pass finished that it did not START: the
			// start-up pass's reclaims and the jobs it ADOPTED from a dead dispatcher
			// alike (read 5, finding 4). An adopted job ends in this loop, with the same
			// RUN line it would have had, so it is counted here.
			if r.adopted {
				recovered++
			}
			line, end, dest := in.finish(r, retired, now())
			// D2 (the real run, 2026-09-11): two jobs printed `RUN DONE … dest=failed`
			// and RUN OK said `started=2 done=2 failed=0` with both of them in failed/.
			// The counts are the truth about the POOL, so a job is counted by WHERE IT
			// LANDED; only the two ends that are neither -- killed at a deadline, and a
			// budget that could not be verified -- are counted by their end.
			switch {
			case end == EndKilled, end == EndUnverifiable:
				killed++
			case dest == Done:
				done++
			default:
				failed++
			}
			if end != EndDone {
				said = said || end == EndUnknown
			}
			tasks.Line(line)
			delete(watching, slot)
		}
		if len(watching) == 0 && (now().After(deadline) || p.Stopped()) {
			break
		}
		if len(watching) == 0 {
			if pending, _ := p.List(Pending); len(pending) == 0 {
				break
			}
			if now().After(deadline) || p.Stopped() {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	tasks.More()

	pending, _ := p.List(Pending)
	fmt.Fprintf(out, "RUN OK started=%d done=%d failed=%d killed=%d pending=%d recovered=%d after=%s\n",
		started, done, failed, killed, len(pending), recovered, trimDuration(now().Sub(deadline.Add(-time.Duration(in.Hours*float64(time.Hour))))))
	fmt.Fprintf(out, "RUN NOTE %s\n", oneline.Escape(remedy(p, failed+launchFailed, killed, len(pending), len(quarantined)+len(retired))))
	if len(pending) > 0 && started == 0 && len(watching) == 0 {
		said = true
	}
	// SPEC-SWARM.md:541 -- exit 1 covers "a `run` that ended with a quarantined slot or a
	// `LAUNCH-FAILED` job". A slot RETIRED mid-run (rule 11's survivors, a data home that
	// may still have a writer in it) is such a slot, and before this the whole pass exited
	// 0 over it: the next reader saw a green RUN OK above a pool one worker smaller.
	if len(quarantined) > 0 || len(retired) > 0 {
		said = true
	}
	if said {
		return 1
	}
	return 0
}

// freeSlot is the allocation, and it asks ONE authority: the slot files. Never a directory
// scan, never a lock file in the slot, never a timer. A slot whose file exists in any state
// is held, so a reserved slot counts against --workers.
func freeSlot(p *Pool, workers int, quarantined, retired map[int]bool, watching map[int]*running) (int, bool) {
	for n := 1; n <= workers; n++ {
		if quarantined[n] || retired[n] || watching[n] != nil {
			continue
		}
		if _, err := os.Stat(p.slotPath(n)); err == nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// unionOf is the slots this run will not allocate: rule 17's quarantined slot files and
// rule 11's retired ones.
func unionOf(a, b map[int]bool) map[int]bool {
	out := map[int]bool{}
	for n := range a {
		out[n] = true
	}
	for n := range b {
		out[n] = true
	}
	return out
}

// launch is rule 18's transaction, from this side: reserve, spawn, wait for the identity,
// and kill what did not identify itself.
func (in RunInput) launch(sc Sidecar, text []byte, slot int, quarantine, retired map[int]bool) (*running, string, int) {
	p := in.Pool
	nonce, err := Nonce()
	if err != nil {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(err.Error())), 1
	}
	jobDirFor := func(n int) string { return in.Worker.JobDir(n, sc.ID) }
	got, err := p.claimFree(in.Workers, unionOf(quarantine, retired), sc.ID, nonce, os.Getpid(), in.Now(), jobDirFor)
	if err != nil {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), 1
	}
	slot, jobDir := got, jobDirFor(got)
	CheckKillPoint("after-reserve")
	if err := in.prepare(sc, text, slot, jobDir); err != nil {
		_ = p.Free(slot)
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), 1
	}
	sc.Job, sc.Slot, sc.Started = jobDir, slot, Stamp(in.Now())
	_ = p.WriteSidecar(Running, sc)

	// The SUPERVISOR is the process that samples usage, so the interval has to reach it:
	// before this, `--usage-interval` was decoded, carried into RunInput and dropped at the
	// fork, and every job sampled at the supervisor's own default.
	supervisorArgs := []string{"supervise", "--pool", p.Dir, "--task", sc.ID,
		"--slot", strconv.Itoa(slot), "--nonce", nonce, "--worker", in.WorkerFile}
	// THE WALL TRAVELS WITH THE JOB. The supervisor is the process that spawns the
	// harness, so it is the process that wraps it; the dispatcher hands it the binary it
	// resolved once, so that thirty jobs do not do thirty PATH lookups and a machine that
	// changes underneath a pass cannot give two jobs two different walls.
	if in.NoSandbox {
		// Rule 11: one loud line per job, on stderr, BEFORE the job starts.
		fmt.Fprintln(in.Stderr, UnsandboxedLine(sc.ID, slot))
		supervisorArgs = append(supervisorArgs, "--no-sandbox")
	} else {
		supervisorArgs = append(supervisorArgs, "--sandbox", in.Sandbox)
	}
	if in.UsageInterval > 0 {
		supervisorArgs = append(supervisorArgs, "--usage-interval", strconv.Itoa(int(in.UsageInterval.Seconds())))
	}
	cmd := exec.Command(in.Supervisor, supervisorArgs...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if log, err := os.OpenFile(filepath.Join(jobDir, "supervisor.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = log, log
		defer log.Close()
	}
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = p.Free(slot)
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: the supervisor would not start: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), 1
	}
	// The identity of the child THIS dispatcher started, taken at the one moment it is not
	// in doubt and carried to the one place that may end it. It is never stored by pid:
	// see proc_windows.go.
	childStarted := StartStamp(cmd.Process.Pid)
	_ = os.WriteFile(filepath.Join(jobDir, "supervisor.pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	CheckKillPoint("after-spawn")
	go func() { _ = cmd.Wait() }()

	// (4) THE HANDSHAKE, holding run.lock and no other lock while it waits.
	timeout := in.LaunchTimeout
	if timeout <= 0 {
		timeout = DefaultLaunchTimeout
	}
	waited := time.Duration(0)
	for waited < timeout {
		sf, err := p.ReadSlot(slot)
		if err == nil && sf.State == SlotLaunched && sf.Nonce == nonce {
			CheckKillPoint("after-identify")
			CheckKillPoint("after-handshake")
			CheckKillPoint("after-release")
			r := &running{sc: sc, slot: slot, nonce: nonce, jobDir: jobDir, started: in.Now(), deadline: taskDeadline(sc, in.Worker)}
			return r, fmt.Sprintf("RUN START id=%s slot=%d pid=%d pgid=%d started=%s deadline=%s tokens=%s job=%s",
				oneline.Field(sc.ID), slot, sf.Pid, sf.Pgid, oneline.Field(Stamp(r.started)),
				trimDuration(r.deadline), oneline.Field(sc.BudgetWord()), oneline.Field(jobDir)), 0
		}
		time.Sleep(20 * time.Millisecond)
		waited += 20 * time.Millisecond
	}
	KillGroup(cmd.Process.Pid, childStarted)
	sc.Launch = "failed"
	_ = p.WriteSidecar(Running, sc)
	_ = p.Claim(sc.ID, Running, Failed)
	_ = p.Free(slot)
	return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=%s: no identity within %ds",
		oneline.Field(sc.ID), slot, trimDuration(waited), int(timeout.Seconds())), 1
}

// prepare builds the job directory: the slot refreshed one way from the home copy, the
// harness config carrying the variable's NAME, the note file created empty before the worker
// starts, and the prompt.
func (in RunInput) prepare(sc Sidecar, text []byte, slot int, jobDir string) error {
	if err := in.Worker.RefreshSlot(slot); err != nil {
		return err
	}
	// The job directory is made ON PURPOSE, and not as a side effect of making the data
	// home inside it: the data home is the thing demanded test 9's tripwire is free to
	// move, and a directory that exists only because something else needed a path under
	// it is a directory that disappears when that something changes.
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return err
	}
	if _, err := in.Worker.WriteHarnessConfig(slot); err != nil {
		return err
	}
	if err := os.MkdirAll(in.Worker.DataHome(slot, sc.ID), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(NotePath(jobDir), nil, 0o644); err != nil {
		return err
	}
	prompt := Prompt(PromptInput{
		ID: sc.ID, JobDir: jobDir, Deadline: taskDeadline(sc, in.Worker), Files: sc.Files,
		Tokens: sc.BudgetWord(), Board: in.Worker.Board, Template: sc.Template, Task: text,
		NoteFile: NotePath(jobDir), Result: ResultPath(jobDir), ResultTmp: ResultPath(jobDir) + ".tmp",
	})
	return writeAtomic(filepath.Join(jobDir, "PROMPT.md"), prompt, 0o644)
}

// state reports whether a job is still alive, and whether it is past its deadline.
func (in RunInput) state(r *running, now time.Time) (alive, over bool) {
	sf, err := in.Pool.ReadSlot(r.slot)
	if err != nil {
		return false, false
	}
	// THE SLOT FILE IS THE IDENTITY, read afresh on every poll: the supervisor's pid AND
	// the start stamp it wrote beside it. A pid alone let a re-issued number read as the
	// still-running supervisor of a job whose exit.json was already on disk, and an ADOPTED
	// job -- whose supervisor this dispatcher never started -- had no identity at all
	// (read 5, finding 3).
	if !Alive(sf.Pid, sf.PidStarted) {
		return false, false
	}
	return true, now.Sub(r.started) > r.deadline+TerminateGrace*2
}
