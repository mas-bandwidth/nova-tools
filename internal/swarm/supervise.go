package swarm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// THE SUPERVISOR: the child the runner forks, and the process that owns a job.
//
// It exists because two files written by the parent are not one atomic step: a crash
// between the fork and the write left a running worker nobody tracked. So the CHILD writes
// its own identity, by compare-and-swap against the reservation it was handed, and it
// writes the durable completion evidence its parent -- or a replacement dispatcher that
// cannot `wait` on somebody else's child -- reads afterwards. An outcome with no evidence is
// `unknown`, never guessed.
//
// The order below is rule 18's, and a kill at any boundary leaves a pool the next
// dispatcher decides by rule 17.

// SuperviseInput is everything `supervise` is handed.
type SuperviseInput struct {
	Pool           *Pool
	Task           string
	Slot           int
	Nonce          string
	Worker         Worker
	Sidecar        Sidecar
	Key            string
	UsageInterval  time.Duration
	Stdout, Stderr io.Writer
	Now            func() time.Time
}

// Supervise is the whole of the supervisor's life. It returns the exit code.
func Supervise(in SuperviseInput) int {
	p := in.Pool
	jobDir := in.Worker.JobDir(in.Slot, in.Task)
	self := os.Getpid()

	CheckPausePoint("before-identify")
	CheckKillPoint("before-identify")

	// (3) IDENTIFY, before doing anything else. The write lands only if the slot file still
	// reads reserved with the nonce this supervisor was handed.
	identity := SlotFile{
		State: SlotLaunched, Pid: self, Pgid: pgidOf(self), PidStarted: StartStamp(self),
		LaunchedAt: Stamp(in.Now()),
	}
	if err := p.Identify(in.Slot, in.Nonce, identity); err != nil {
		return abort(in, jobDir, err)
	}
	started := in.Now()
	if err := WriteJSON(PidPath(jobDir), PidRecord{
		Job: in.Task, Slot: in.Slot, State: SlotLaunched, Pid: self, Pgid: pgidOf(self),
		PidStarted: identity.PidStarted, Nonce: in.Nonce, Started: Stamp(started),
	}); err != nil {
		return abort(in, jobDir, err)
	}

	// (5) RELEASE TO WORK: the harness, as this supervisor's child, in a process group of
	// the JOB's own so that the survivor check after it exits asks about the job and not
	// about the supervisor asking.
	logPath := filepath.Join(jobDir, "harness.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return endWith(in, jobDir, started, ExitRecord{RC: -1, End: EndFailed, Reason: "the harness log could not be opened: " + redactedReason(err)})
	}
	harness := in.Worker.Harness
	if resolved, err := exec.LookPath(harness); err == nil {
		harness = resolved
	}
	cmd := exec.Command(harness, harnessArgs(in.Worker, jobDir)...)
	cmd.Dir = in.Worker.SlotDir(in.Slot)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = childEnv(in.Worker, in.Slot, in.Task, in.Key)
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return endWith(in, jobDir, started, ExitRecord{RC: -1, End: EndFailed, Reason: "the harness would not start: " + redactedReason(err)})
	}
	jobPgid := cmd.Process.Pid
	_ = WriteJSON(PidPath(jobDir), PidRecord{
		Job: in.Task, Slot: in.Slot, State: SlotLaunched, Pid: self, Pgid: pgidOf(self), JobPgid: jobPgid,
		PidStarted: identity.PidStarted, Nonce: in.Nonce, Started: Stamp(started),
	})
	_ = p.UpdateSlot(in.Slot, in.Nonce, func(sf SlotFile) SlotFile { sf.JobPgid = jobPgid; return sf })
	CheckKillPoint("supervisor-after-release")

	record := watch(in, cmd, jobDir, jobPgid, started)
	logFile.Close()
	CheckKillPoint("between-exit-and-exit-json")
	return endWith(in, jobDir, started, record)
}

// watch holds the deadline and the budget beside the harness (rules 7 and 13).
func watch(in SuperviseInput, cmd *exec.Cmd, jobDir string, jobPgid int, started time.Time) ExitRecord {
	deadline := taskDeadline(in.Sidecar, in.Worker)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	interval := in.UsageInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	sample := time.NewTicker(interval)
	defer sample.Stop()
	timer := time.NewTimer(deadline)
	defer timer.Stop()

	dataHome := in.Worker.DataHome(in.Slot, in.Task)
	failures := 0
	var seen ProviderUsage
	spent, partial, observed := 0, false, false

	for {
		select {
		case err := <-done:
			// A WORKER THAT EXITS NON-ZERO IS A FAILED JOB (SPEC-SWARM.md:544), and `end`
			// is the column the token ledger reads: a job whose provider refused its key
			// with rc=7 was recorded `end=done`, so `COST TASK` called a spent failure a
			// spent success (the new-user audit, F1 and F4, 2026-09-11).
			rc, signal := exitOf(err)
			end := EndDone
			if rc != 0 {
				end = EndFailed
			}
			return ExitRecord{RC: rc, Signal: signal, End: end, Spent: spent, Observed: observed, Partial: partial}
		case <-timer.C:
			// The default action at the deadline: reap the worker and record what is on
			// disk. The swarm never waits forever.
			survived := Reap(jobPgid, TerminateGrace)
			<-done
			return ExitRecord{RC: -1, End: EndKilled, Survivors: boolCount(survived), Spent: spent, Observed: observed, Partial: partial}
		case <-sample.C:
			usage, err := ReadProviderUsage(in.Worker.Usage, dataHome)
			if err != nil {
				failures++
				if failures >= 3 {
					survived := Reap(jobPgid, TerminateGrace)
					<-done
					return ExitRecord{RC: -1, End: EndUnverifiable, Survivors: boolCount(survived), Spent: spent,
						Observed: observed, Partial: partial, Reason: err.Error()}
				}
				continue
			}
			failures = 0
			if !usage.Observed {
				continue
			}
			seen = usage
			sum, seenCols, part := seen.Sum()
			spent, partial, observed = sum, part, seenCols > 0
			if in.Sidecar.Unmetered || in.Sidecar.Tokens <= 0 || !observed {
				continue
			}
			if spent >= in.Sidecar.Tokens {
				// A STOP CONDITION ON OBSERVATIONS, not a ceiling on spend: usage arrives
				// after the tokens are spent, so the overshoot is bounded by one sample
				// interval plus the provider's own delay, and the usage row carries the
				// true final sum rather than the sum at the stop.
				survived := Reap(jobPgid, TerminateGrace)
				<-done
				return ExitRecord{RC: -1, End: EndBudget, Survivors: boolCount(survived), Spent: spent, Observed: true, Partial: partial}
			}
		}
	}
}

// endWith writes the completion evidence and exits. The evidence is written through .tmp and
// a rename AFTER the harness exits and BEFORE the supervisor exits, so a replacement
// dispatcher reads a whole record or none.
func endWith(in SuperviseInput, jobDir string, started time.Time, rec ExitRecord) int {
	rec.Nonce, rec.Ended = in.Nonce, Stamp(in.Now())
	// Rule 11's group check, made by the process that owns the group: anything still in the
	// job's own group after its leader has gone is a background subtask the prompt forbids.
	if jobPgid := readJobPgid(jobDir); jobPgid > 0 && GroupAlive(jobPgid) {
		if n, ok := GroupMembers(jobPgid, os.Getpid()); ok {
			rec.Survivors = n
		} else if rec.Survivors == 0 {
			rec.Survivors = 1
		}
		KillGroup(jobPgid)
	}
	if err := WriteJSON(ExitPath(jobDir), rec); err != nil {
		fmt.Fprintf(in.Stderr, "SUPERVISE FAILED slot=%d id=%s: the completion evidence could not be written: %s\n",
			in.Slot, in.Task, redactedReason(err))
		return 2
	}
	return 0
}

// abort is rule 18's losing path, and its ORDER is the rule: never spawn the harness; count
// the processes in its own group other than itself; write aborted.json through .tmp, fsync
// and rename, SO THE DURABLE ACKNOWLEDGEMENT EXISTS BEFORE ITS OWN DEATH CAN BE OBSERVED;
// then exit 2. It never claims an absence it did not observe.
func abort(in SuperviseInput, jobDir string, cause error) int {
	survivors := 0
	if n, ok := GroupMembers(pgidOf(os.Getpid()), os.Getpid()); ok {
		survivors = n
	}
	_ = os.MkdirAll(jobDir, 0o755)
	_ = WriteJSON(AbortedPath(jobDir), AbortedRecord{
		Nonce: in.Nonce, Reason: cause.Error(), At: Stamp(in.Now()), Survivors: survivors,
	})
	CheckKillPoint("between-aborted-and-exit")
	fmt.Fprintf(in.Stderr, "SUPERVISE ABORTED slot=%d id=%s: reservation changed\n", in.Slot, in.Task)
	return 2
}

// UpdateSlot is a compare-and-swap on a launched slot, for the one field the supervisor
// learns after it has identified itself: the job's own process group.
func (p *Pool) UpdateSlot(n int, nonce string, mutate func(SlotFile) SlotFile) error {
	release, err := p.TakeLock(SlotsLock, SlotsWait)
	if err != nil {
		return err
	}
	defer release()
	cur, err := p.ReadSlot(n)
	if err != nil {
		return err
	}
	if cur.Nonce != nonce || cur.State != SlotLaunched {
		return fmt.Errorf("slot %d is no longer this launch's", n)
	}
	return writeSlot(p.slotPath(n), mutate(cur))
}

// taskDeadline is the task's own, or the worker description's where the task names none.
func taskDeadline(sc Sidecar, w Worker) time.Duration {
	if d, err := time.ParseDuration(sc.Deadline); err == nil && d > 0 {
		return d
	}
	return w.DefaultDeadline()
}

// harnessArgs is the harness's argv: the description's own arguments with this tool's two
// placeholders expanded, and the prompt FILE last where the description names no place for
// it. THE TASK TEXT IS NEVER AN ARGUMENT -- the prototype took it as $1, which puts a
// multi-paragraph task into the process table and into every ps a bench user runs.
//
// The placeholders exist because the model must REACH THE CHILD. On 2026-09-11 a real run
// against DeepSeek decoded `model`, required it, printed it on RUN POOL, and handed the
// harness nothing but a path: `opencode <job>/PROMPT.md` reads that path as a project
// directory, fails to chdir, exits 0, and two jobs died in two seconds under a green
// RUN OK. A description now writes the invocation it means --
// ["run", "--model", "{model}", "--", "{prompt}"] -- and this function fills it in.
func harnessArgs(w Worker, jobDir string) []string {
	prompt := filepath.Join(jobDir, "PROMPT.md")
	out := make([]string, 0, len(w.HarnessArgs)+1)
	placed := false
	for _, a := range w.HarnessArgs {
		if strings.Contains(a, PromptPlaceholder) {
			placed = true
		}
		out = append(out, expandHarnessArg(a, w, prompt))
	}
	if !placed {
		out = append(out, prompt)
	}
	return out
}

// The placeholders a worker description may write into harness_args.
const (
	ModelPlaceholder   = "{model}"
	PromptPlaceholder  = "{prompt}"
	BaseURLPlaceholder = "{base_url}"
)

func expandHarnessArg(a string, w Worker, prompt string) string {
	a = strings.ReplaceAll(a, ModelPlaceholder, w.Model)
	a = strings.ReplaceAll(a, PromptPlaceholder, prompt)
	return strings.ReplaceAll(a, BaseURLPlaceholder, w.BaseURL)
}

// childEnv is the child's whole environment, built rather than inherited: the key in the
// CHILD's environment only, the job's own data home, and PATH so the harness can find what
// it runs. No path this tool uses comes from the environment (SPEC.md, no guessing); PATH is
// here because a harness is a program and a program is found on one.
func childEnv(w Worker, slot int, id, key string) []string {
	pathVal := os.Getenv("PATH")
	if pathVal == "" {
		pathVal = os.Getenv("Path")
	}
	env := []string{
		"PATH=" + pathVal,
		"XDG_DATA_HOME=" + w.DataHome(slot, id),
		"NOVA_SWARM_JOB=" + w.JobDir(slot, id),
	}
	if runtime.GOOS == "windows" {
		env = append(env, "Path="+pathVal)
		for _, k := range []string{"SystemRoot", "SYSTEMROOT", "SystemDrive", "PATHEXT", "TEMP", "TMP", "COMSPEC"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
	}
	if w.EnvVar != "" && key != "" {
		env = append(env, w.EnvVar+"="+key)
	}
	return env
}

func readJobPgid(jobDir string) int {
	var pr PidRecord
	if err := ReadJSON(PidPath(jobDir), &pr); err != nil {
		return 0
	}
	return pr.JobPgid
}

func exitOf(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if status, ok := ee.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return -1, status.Signal().String()
		}
		return ee.ExitCode(), ""
	}
	return -1, ""
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}
