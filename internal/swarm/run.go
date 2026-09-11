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

	said := false // anything that makes this run exit 1
	quarantined := map[int]bool{}
	watching := map[int]*running{}

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
			fin, usagePath := in.settle(sc, d.File.JobDir, rec, end, now())
			said = said || end == EndUnknown
			fmt.Fprintf(out, "RUN RECLAIM slot=%d id=%s end=%s usage=%s\n", n, oneline.Field(sc.ID), oneline.Field(end), oneline.Field(usagePath))
			_ = fin
			if err := p.Free(n); err != nil {
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
			fmt.Fprintf(out, "RUN RECLAIM slot=%d id=%s end=unlaunched usage=-\n", n, oneline.Field(d.File.Job))
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
	started, done, failed, killed := 0, 0, 0, 0
	launchFailed := 0
	tasks := bounded.Capped(out, in.Max, "RUN", "task", "nova-swarm status --pool "+p.Dir+" --max 0")

	for {
		// Start what can be started, while the dispatcher's own deadline is ahead of us.
		for !now().After(deadline) && !p.Stopped() && len(watching) < in.Workers {
			slot, ok := freeSlot(p, in.Workers, quarantined, watching)
			if !ok {
				break
			}
			sc, text, claimed, err := p.ClaimNext()
			if err != nil || !claimed {
				break
			}
			r, line, code := in.launch(sc, text, slot, quarantined)
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
					Reap(sf.JobPgid, TerminateGrace)
					Reap(sf.Pgid, TerminateGrace)
				}
				continue
			}
			line, end := in.finish(r, quarantined, now())
			switch end {
			case EndKilled, EndUnverifiable:
				killed++
			case EndDone:
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
	fmt.Fprintf(out, "RUN OK started=%d done=%d failed=%d killed=%d pending=%d after=%s\n",
		started, done, failed, killed, len(pending), trimDuration(now().Sub(deadline.Add(-time.Duration(in.Hours*float64(time.Hour))))))
	fmt.Fprintf(out, "RUN NOTE %s\n", oneline.Escape(remedy(p, failed+launchFailed, killed, len(pending), len(quarantined))))
	if len(pending) > 0 && started == 0 && len(watching) == 0 {
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
func freeSlot(p *Pool, workers int, quarantined map[int]bool, watching map[int]*running) (int, bool) {
	for n := 1; n <= workers; n++ {
		if quarantined[n] || watching[n] != nil {
			continue
		}
		if _, err := os.Stat(p.slotPath(n)); err == nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// launch is rule 18's transaction, from this side: reserve, spawn, wait for the identity,
// and kill what did not identify itself.
func (in RunInput) launch(sc Sidecar, text []byte, slot int, quarantine map[int]bool) (*running, string, int) {
	p := in.Pool
	nonce, err := Nonce()
	if err != nil {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(err.Error())), 1
	}
	jobDirFor := func(n int) string { return in.Worker.JobDir(n, sc.ID) }
	got, err := p.claimFree(in.Workers, quarantine, sc.ID, nonce, os.Getpid(), in.Now(), jobDirFor)
	if err != nil {
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), 1
	}
	slot, jobDir := got, jobDirFor(got)
	if err := in.prepare(sc, text, slot, jobDir); err != nil {
		_ = p.Free(slot)
		return nil, fmt.Sprintf("RUN LAUNCH-FAILED id=%s slot=%d after=0s: %s", oneline.Field(sc.ID), slot, oneline.Escape(redactedReason(err))), 1
	}
	sc.Job, sc.Slot, sc.Started = jobDir, slot, Stamp(in.Now())
	_ = p.WriteSidecar(Running, sc)

	cmd := exec.Command(in.Supervisor, "supervise", "--pool", p.Dir, "--task", sc.ID,
		"--slot", strconv.Itoa(slot), "--nonce", nonce, "--worker", in.WorkerFile)
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
			r := &running{sc: sc, slot: slot, nonce: nonce, jobDir: jobDir, started: in.Now(), deadline: taskDeadline(sc, in.Worker)}
			return r, fmt.Sprintf("RUN START id=%s slot=%d pid=%d pgid=%d started=%s deadline=%s tokens=%s job=%s",
				oneline.Field(sc.ID), slot, sf.Pid, sf.Pgid, oneline.Field(Stamp(r.started)),
				trimDuration(r.deadline), oneline.Field(sc.BudgetWord()), oneline.Field(jobDir)), 0
		}
		time.Sleep(20 * time.Millisecond)
		waited += 20 * time.Millisecond
	}
	KillGroup(cmd.Process.Pid)
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
	if !Alive(sf.Pid) {
		return false, false
	}
	return true, now.Sub(r.started) > r.deadline+TerminateGrace*2
}
