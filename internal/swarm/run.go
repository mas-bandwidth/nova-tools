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

// The codes `launch` answers with. They are named because the caller COUNTS by them: a task
// refused before it ran lands in failed/ and belongs in `failed=`, and a launch that broke
// down is a LAUNCH-FAILED job whose task may be in failed/ or still pending.
const (
	launchStarted = 0 // the supervisor identified itself; the job is running
	launchBroken  = 1 // RUN LAUNCH-FAILED: the launch broke down
	launchRefused = 2 // RUN INPUT-LIMIT: the task was refused before the launch, and is in failed/
)

// running is one job this dispatcher is watching.
type running struct {
	sc         Sidecar
	slot       int
	nonce      string
	exitAttest string
	jobDir     string
	started    time.Time
	deadline   time.Duration
	adopted    bool
	notes      int
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
		if reason, text := SandboxGate(in.Sandbox, p.Dir, in.Worker.KeyFile, errOut); reason != "" {
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
			r := &running{sc: sc, slot: n, nonce: d.File.Nonce, exitAttest: d.File.ExitAttest, jobDir: d.File.JobDir, started: started,
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
			// A PROVIDER'S INPUT LIMIT IS THE SAME CLASS ON THIS PATH TOO (#103). The
			// supervisor names it on exit.json, and this branch reads that word; the log is
			// asked as well, for the job whose supervisor died before it could classify --
			// one function, so an end word named on the live path and not on this one is
			// how a class goes missing (the shape of read 6, finding 2).
			var limit string
			end, limit = InputLimitEnd(d.File.JobDir, end, rec.RC, rec.Reason, in.Worker.InputLimitPhrases)
			fin, usagePath := in.settle(sc, d.File.JobDir, rec, end, now())
			said = said || end == EndUnknown
			// ITS FILES MOVE AS RULE 12 SAYS (SPEC-SWARM.md:372). Finalize writes the
			// usage file and the report copy; it does not move the job, and this branch
			// never did either -- so a recovered job sat in running/ with no slot and
			// nothing watching it, forever.
			sc.Class, sc.End, sc.RC, sc.Ended = fin.Class, end, rec.RC, Stamp(now())
			sc.Violation = violationWord(end, rec.Survivors)
			if end == EndInputLimit {
				sc.Limit = limit
			}
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
			// A reservation whose launch is unproven is rewritten to `orphaned` with its
			// nonce KEPT -- under slots.lock, after a recheck that it still reads reserved
			// with that nonce, and adopting instead if an identify landed in between.
			//
			// THE ADOPTION IS NOT A QUARANTINE, and the exit code is written after the
			// recheck rather than before it. SPEC-SWARM.md:541 gives exit 1 to "a run that
			// ended with a quarantined slot or a LAUNCH-FAILED job", and an adopted slot
			// is neither: it is a supervisor that identified while this dispatcher was
			// reading, whose job this pass then finishes. Before this the pass printed
			// RUN ADOPT, RUN DONE, `RUN OK done=1` and `RUN NOTE the pool drained` -- and
			// exited 1 over them, because `said` had been set a few lines above and the
			// adopting branch only took the slot back out of the quarantine map. Seen on
			// ubuntu CI 2026-09-12 by the hangup test, which is the one test that reaches
			// this branch with a supervisor that really is alive.
			if d.File.State == SlotReserved {
				if adopted, sf, err := p.Orphan(n, d.File.Nonce); err == nil && adopted {
					sc, _ := p.ReadSidecar(Running, sf.Job)
					started := parseStamp(sf.LaunchedAt, now())
					watching[n] = &running{sc: sc, slot: n, nonce: sf.Nonce, exitAttest: sf.ExitAttest, jobDir: sf.JobDir, started: started,
						deadline: taskDeadline(sc, in.Worker), adopted: true}
					fmt.Fprintf(out, "RUN ADOPT id=%s slot=%d pid=%d started=%s remaining=%s\n",
						oneline.Field(sf.Job), n, sf.Pid, oneline.Field(sf.LaunchedAt), trimDuration(taskDeadline(sc, in.Worker)-now().Sub(started)))
					continue
				}
			}
			said = true
			quarantined[n] = true
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
			case launchStarted:
				started++
				watching[slot] = r
				tasks.Line(line)
			case launchRefused:
				// THE COUNTS ARE THE TRUTH ABOUT THE POOL (SPEC-SWARM.md:608-612), "never
				// about the output" -- D2's lesson, arriving on the launch side. A task
				// refused before its launch is in failed/ with `end=input-limit`, and it was
				// counted NOWHERE: `RUN OK` printed `failed=` from the main loop alone and
				// only RUN NOTE's remedy saw it (Fable's read of #150, finding 1). The
				// LAUNCH-FAILED paths below are counted apart still, because they end in
				// three different places -- failed/ after the handshake, running/ before it
				// -- and one word for three destinations is the bug this line is fixing.
				// It is counted ONCE: `remedy` is handed failed+launchFailed, so a refusal
				// in both would be two failures in a pool that holds one.
				said = true
				failed++
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
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(err.Error())), launchBroken
	}
	jobDirFor := func(n int) string { return in.Worker.JobDir(n, sc.ID) }
	got, err := p.claimFree(in.Workers, unionOf(quarantine, retired), sc.ID, nonce, os.Getpid(), in.Now(), jobDirFor)
	if err != nil {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), launchBroken
	}
	slot, jobDir := got, jobDirFor(got)
	CheckKillPoint("after-reserve")
	if err := in.prepare(sc, text, slot, jobDir); err != nil {
		_ = p.Free(slot)
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), launchBroken
	}
	// THE TASK BUDGET NAMES THE WINDOW IT FITS, AND IT IS CHECKED BEFORE THE LAUNCH (#103).
	// Two Freddy reads of whole specs spent 215 seconds each to be told by the provider that
	// they did not fit; a task that says how big its window is can be told that here, for
	// nothing. The size is MEASURED off the prompt this tool just wrote -- the same bytes the
	// harness is handed -- and it is not launched, so no provider is paid for it.
	size, _ := PromptSize(jobDir)
	if reason, over := OverMaxInput(sc, size); over {
		_ = p.Free(slot)
		// The CLASS is the record, and `launch` is left alone: `unlaunched` is rule 17's
		// word for a reservation that never launched and goes back to PENDING, and one
		// token has one meaning (lesson 119). This task is not pending; it cannot run as
		// written, and `end=input-limit` is why.
		sc.End, sc.Limit = EndInputLimit, reason
		_ = p.WriteSidecar(Running, sc)
		_ = p.Claim(sc.ID, Running, Failed)
		return nil, fmt.Sprintf("RUN INPUT-LIMIT id=%s slot=%d after=0s input=%s max=%s dest=failed: %s",
			oneline.Field(sc.ID), slot, oneline.Field(promptSizeWord(jobDir)), oneline.Field(maxInputWord(sc)),
			oneline.Escape(oneline.Cap(reason, oneline.TailBytes))), launchRefused
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
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: the supervisor would not start: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), launchBroken
	}
	// The identity of the child THIS dispatcher started, taken at the one moment it is not
	// in doubt and carried to the one place that may end it. It is never stored by pid:
	// see proc_windows.go.
	childStarted := StartStamp(cmd.Process.Pid)
	_ = os.WriteFile(filepath.Join(jobDir, "supervisor.pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	CheckKillPoint("after-spawn")
	// THE SUPERVISOR'S OWN END IS AN OBSERVABLE, and the handshake below waits on it as
	// well as on the identity. A supervisor that DIED before it identified itself -- it
	// lost its compare-and-swap, or it could not write the slot file at all -- will never
	// write one, and the whole launch timeout spent waiting for it is a diagnosis
	// postponed, not a chance taken (#92, `no identity within 10s`). The clock stays as
	// the OUTER bound, for the supervisor that is alive and merely slow.
	gone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(gone) }()

	// (4) THE HANDSHAKE, holding run.lock and no other lock while it waits.
	timeout := in.LaunchTimeout
	if timeout <= 0 {
		timeout = DefaultLaunchTimeout
	}
	// ONE DEADLINE FOR THE WHOLE HANDSHAKE, taken once, off the monotonic clock (Stella,
	// #126). The old loop counted its own sleeps -- `waited += 20ms` -- and a count of
	// sleeps is not a measure of time spent: the read between two sleeps may now wait out a
	// Windows collision for up to SteadyWindow, and 500 turns of that is 1010s inside a 10s
	// bound, with run.lock held. So the clock is read, not counted; the read is given only
	// what is LEFT of it (ReadSlotBy); and `after=` reports the time that actually passed
	// rather than the sleeps that were scheduled.
	start := time.Now()
	sf, identified, supervisorGone := in.awaitIdentity(slot, nonce, start.Add(timeout), gone)
	if identified {
		CheckKillPoint("after-identify")
		CheckKillPoint("after-handshake")
		CheckKillPoint("after-release")
		r := &running{sc: sc, slot: slot, nonce: nonce, exitAttest: sf.ExitAttest, jobDir: jobDir, started: in.Now(), deadline: taskDeadline(sc, in.Worker)}
		return r, fmt.Sprintf("RUN START id=%s slot=%d pid=%d pgid=%d started=%s deadline=%s tokens=%s job=%s",
			oneline.Field(sc.ID), slot, sf.Pid, sf.Pgid, oneline.Field(Stamp(r.started)),
			trimDuration(r.deadline), oneline.Field(sc.BudgetWord()), oneline.Field(jobDir)), launchStarted
	}
	waited := time.Since(start)
	KillGroup(cmd.Process.Pid, childStarted)
	sc.Launch = "failed"
	_ = p.WriteSidecar(Running, sc)
	_ = p.Claim(sc.ID, Running, Failed)
	_ = p.Free(slot)
	if supervisorGone {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=%s: the supervisor exited without writing an identity%s",
			oneline.Field(sc.ID), slot, trimDuration(waited), abortedReason(jobDir, nonce)), launchBroken
	}
	return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=%s: no identity within %ds",
		oneline.Field(sc.ID), slot, trimDuration(waited), int(timeout.Seconds())), launchBroken
}

// awaitIdentity is the handshake's wait: the slot file polled for THIS launch's identity
// until the deadline, with the supervisor's own exit as the other observable.
//
// THE DEADLINE IS THE WHOLE BOUND, and it is the caller's, taken once off the monotonic
// clock. Every retrying read inside the loop is handed the same deadline (ReadSlotBy), so a
// collision waited out on Windows is spent INSIDE the launch timeout and never added to it:
// the loop returns by `deadline` whether the reads are instant or every one of them
// collides for its whole SteadyWindow. The last poll is shortened to what is left, so a
// short configured timeout is not rounded up to the next 20ms either.
func (in RunInput) awaitIdentity(slot int, nonce string, deadline time.Time, gone <-chan struct{}) (SlotFile, bool, bool) {
	supervisorGone := false
	for time.Now().Before(deadline) {
		sf, err := in.Pool.ReadSlotBy(slot, deadline)
		if err == nil && sf.State == SlotLaunched && sf.Nonce == nonce {
			return sf, true, supervisorGone
		}
		// A supervisor that has EXITED is read one last time -- its identify may have
		// landed in the same instant it died -- and then believed.
		if supervisorGone {
			break
		}
		poll := 20 * time.Millisecond
		if left := time.Until(deadline); left < poll {
			poll = left
		}
		select {
		case <-gone:
			supervisorGone = true
			continue
		case <-time.After(poll):
		}
	}
	return SlotFile{}, false, supervisorGone
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
		return in.unreadable(r, err, now)
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

// unreadable is what this pass does about a slot file it could not read.
//
// A READ THAT FAILED IS NOT EVIDENCE THAT A JOB IS OVER. `state` finalized on any error at
// all, and on Windows a read of a path whose old file is delete-pending fails for
// microseconds every time somebody replaces it -- which the supervisor does, to its own
// slot file, milliseconds after it identifies and exactly when this poll first lands. One
// such read finished a RUNNING job: the dispatcher reaped a live supervisor at `after=0s`,
// then found no exit.json where it had just killed the process that writes it, and the
// pass exited 1 over `rc=-1 end=unknown` with the worker's own finished report beside it
// (run 34698330796). fileretry.go now waits that collision out; this is the rule underneath
// it, which holds for any unreadable record and not only that one.
//
// So the question is asked of an OBSERVABLE instead: the record is GONE (an answer), or
// the supervisor's own completion evidence carries this launch's nonce (an answer), or the
// job is still running. The clock is only the outer bound -- a record that stays unreadable
// past the job's own deadline is finalized rather than watched forever.
func (in RunInput) unreadable(r *running, err error, now time.Time) (alive, over bool) {
	if missing(err) {
		return false, false
	}
	var ex ExitRecord
	if readErr := ReadJSON(ExitPath(r.jobDir), &ex); readErr == nil && ex.Nonce == r.nonce && ExitAttestOK(ex.Attest, r.exitAttest) {
		return false, false
	}
	// THE OUTER BOUND IS THE JOB'S OWN CLOCK, from the job's START, which is the bound the
	// paragraph above, the PR body and the test all name (Rowan's read of #126, MEDIUM 1).
	// Measured from the first unreadable read instead, it was a SECOND clock stacked on the
	// first: a slot file that turned unreadable at the deadline was held to roughly twice
	// the deadline plus 12s. And `over` is dead on this path -- the caller answers
	// `alive && over` by reading the same slot file again, which fails for the same reason,
	// so nothing is ever reaped by it and the loop would spin to the second clock anyway.
	if now.Sub(r.started) > r.deadline+TerminateGrace*2 {
		return false, false
	}
	return true, false
}

// abortedReason is the supervisor's OWN word about why it never identified, where it left
// one: rule 18's aborted.json, carrying this launch's nonce. A LAUNCH-FAILED line with the
// reason on it is a line a person can act on; before this the only diagnosis of a lost
// launch was a file no verb prints.
func abortedReason(jobDir, nonce string) string {
	var ab AbortedRecord
	if err := ReadJSON(AbortedPath(jobDir), &ab); err == nil && ab.Nonce == nonce && ab.Reason != "" {
		return ": " + oneline.Escape(oneline.Cap(ab.Reason, oneline.TailBytes))
	}
	return ""
}
