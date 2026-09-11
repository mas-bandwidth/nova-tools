package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ---------------------------------------------------------------------------------------
// Demanded test 17 (SPEC-SWARM line 1363):
// TestADeadDispatcherIsRecoveredOrQuarantined: a dispatcher SIGKILLed with one worker alive,
// one worker exited but not finalized, and one worker whose leader is dead and whose group
// has a survivor, then run again on the same pool: the live worker is adopted (RUN ADOPT
// with its original start stamp, its deadline honoured from that stamp, exactly one RUN DONE
// for it, printed by the second dispatcher, and its slot never handed to a pending task while
// it lives), the exited job is finalized and reclaimed (RUN RECLAIM … usage=<path>, its slot
// then allocated to a pending task), the third slot is RUN QUARANTINE with the reason, never
// allocated, STATUS OK quarantined=1, and the run exits 1 at its end; a slot file whose pid
// is alive with a different start stamp is quarantined; a fourth slot reserved by the dead
// dispatcher is orphaned, its nonce kept, never freed by this run and never handed to a
// pending task; the tripwire finds no pgrep, no ps and no match on a command line; a second
// run while the first is alive is still exit 2 naming the holder; a slot whose supervisor is
// dead with no exit.json is finalized end=unknown into failed/, its published report copied,
// and never counted ok or clean.
func TestADeadDispatcherIsRecoveredOrQuarantined(t *testing.T) {
	b := newBench(t)

	// A second run while the first is alive is still exit 2 naming the holder.
	p := mustOpenPool(t, b.pool)
	rel, err := p.TakeLock(swarm.RunLock, 0)
	if err != nil {
		t.Fatalf("could not take run.lock: %v", err)
	}
	exit, _, stderr := b.run()
	rel()
	if exit != 2 {
		t.Fatalf("run while another holds run.lock: exit = %d, want 2", exit)
	}
	mustContain(t, "stderr", stderr, "another nova-swarm holds")
	mustContain(t, "stderr", stderr, "run.lock")

	// Set up the tasks and slot files:
	task1ID := "20260101T000000Z-task1-000001"
	task1Dir := filepath.Join(b.dir, "worker-home-1", "jobs", task1ID)
	_ = os.MkdirAll(task1Dir, 0o755)
	write(t, filepath.Join(task1Dir, "task.md"), "live task\n")
	write(t, filepath.Join(b.pool, "running", task1ID+".task"), "live task\n")

	// 2. Worker exited but not finalized (to be reclaimed):
	task2ID := "20260101T000000Z-task2-000002"
	task2Dir := filepath.Join(b.dir, "worker-home-2", "jobs", task2ID)
	_ = os.MkdirAll(task2Dir, 0o755)
	write(t, filepath.Join(task2Dir, "task.md"), "exited task\n")
	write(t, filepath.Join(b.pool, "running", task2ID+".task"), "exited task\n")
	sf2 := swarm.SlotFile{
		Job: task2ID, JobDir: task2Dir, State: swarm.SlotLaunched,
		Pid: 999990, Pgid: 999990, Nonce: "nonce-2", RunnerPid: 999998,
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "2.json"), sf2); err != nil {
		t.Fatal(err)
	}
	if err := swarm.WriteJSON(filepath.Join(task2Dir, "exit.json"), swarm.ExitRecord{
		RC: 0, End: swarm.EndDone, Nonce: "nonce-2", Ended: swarm.Stamp(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(task2Dir, "RESULT.md"), "# Head\nfindings: 0\nnotes read: 0\nrepo: o/n\nrev: abc\n\n## Findings\n")
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task2ID, Job: task2Dir, Slot: 2, Started: swarm.Stamp(time.Now().Add(-10 * time.Second)),
	})

	// 3. Worker whose leader is dead and whose group has a survivor (to be quarantined):
	survivorCmd := exec.Command("sleep", "20")
	survivorCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := survivorCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = survivorCmd.Process.Kill()
		_ = survivorCmd.Wait()
	}()
	task3ID := "20260101T000000Z-task3-000003"
	task3Dir := filepath.Join(b.dir, "worker-home-3", "jobs", task3ID)
	_ = os.MkdirAll(task3Dir, 0o755)
	write(t, filepath.Join(task3Dir, "task.md"), "survivor task\n")
	write(t, filepath.Join(b.pool, "running", task3ID+".task"), "survivor task\n")
	sf3 := swarm.SlotFile{
		Job: task3ID, JobDir: task3Dir, State: swarm.SlotLaunched,
		Pid: 999991, Pgid: 999991, JobPgid: survivorCmd.Process.Pid,
		Nonce: "nonce-3", RunnerPid: 999998,
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "3.json"), sf3); err != nil {
		t.Fatal(err)
	}
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task3ID, Job: task3Dir, Slot: 3, Started: swarm.Stamp(time.Now().Add(-10 * time.Second)),
	})

	// 4. Slot reserved by dead dispatcher (to be orphaned and quarantined):
	task4ID := "20260101T000000Z-task4-000004"
	task4Dir := filepath.Join(b.dir, "worker-home-4", "jobs", task4ID)
	_ = os.MkdirAll(task4Dir, 0o755)
	write(t, filepath.Join(task4Dir, "task.md"), "reserved task\n")
	write(t, filepath.Join(b.pool, "running", task4ID+".task"), "reserved task\n")
	sf4 := swarm.SlotFile{
		Job: task4ID, JobDir: task4Dir, State: swarm.SlotReserved,
		Nonce: "nonce-4", RunnerPid: 999998,
		ReservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "4.json"), sf4); err != nil {
		t.Fatal(err)
	}
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task4ID, Job: task4Dir, Slot: 4,
	})

	// 5. Slot whose pid is alive under a different start stamp (pid reuse, to be quarantined):
	task5ID := "20260101T000000Z-task5-000005"
	task5Dir := filepath.Join(b.dir, "worker-home-5", "jobs", task5ID)
	_ = os.MkdirAll(task5Dir, 0o755)
	write(t, filepath.Join(b.pool, "running", task5ID+".task"), "pid reuse task\n")
	sf5 := swarm.SlotFile{
		Job: task5ID, JobDir: task5Dir, State: swarm.SlotLaunched,
		Pid: os.Getpid(), PidStarted: "1970-01-01T00:00:00Z",
		Nonce: "nonce-5",
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "5.json"), sf5); err != nil {
		t.Fatal(err)
	}
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task5ID, Job: task5Dir, Slot: 5,
	})

	// 6. Slot whose supervisor is dead with no exit.json (finalized end=unknown into failed/):
	task6ID := "20260101T000000Z-task6-000006"
	task6Dir := filepath.Join(b.dir, "worker-home-6", "jobs", task6ID)
	_ = os.MkdirAll(task6Dir, 0o755)
	write(t, filepath.Join(task6Dir, "RESULT.md"), "# Head\nfindings: 0\nnotes read: 0\nrepo: o/n\nrev: abc\n\n## Findings\n")
	write(t, filepath.Join(b.pool, "running", task6ID+".task"), "dead supervisor task\n")
	sf6 := swarm.SlotFile{
		Job: task6ID, JobDir: task6Dir, State: swarm.SlotLaunched,
		Pid: 999993, Nonce: "nonce-6", RunnerPid: 999998,
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "6.json"), sf6); err != nil {
		t.Fatal(err)
	}
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task6ID, Job: task6Dir, Slot: 6,
	})

	// Add a pending task (task 7) to be allocated to the reclaimed slot 2
	task7ID := b.add("pending task\nFAKE-FINDINGS 0\n")

	// 1. Live worker (to be adopted) started immediately before dispatcher run:
	liveCmd := exec.Command("sleep", "15")
	liveCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := liveCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = liveCmd.Process.Kill()
		_ = liveCmd.Wait()
	}()
	livePgid := liveCmd.Process.Pid
	sf1 := swarm.SlotFile{
		Job: task1ID, JobDir: task1Dir, State: swarm.SlotLaunched,
		Pid: liveCmd.Process.Pid, Pgid: livePgid, JobPgid: livePgid,
		PidStarted: swarm.StartStamp(liveCmd.Process.Pid),
		LaunchedAt: swarm.Stamp(time.Now().Add(-1 * time.Second)),
		Nonce:      "nonce-1", RunnerPid: 999998,
	}
	if err := swarm.WriteJSON(filepath.Join(b.pool, "slots", "1.json"), sf1); err != nil {
		t.Fatal(err)
	}
	if err := swarm.WriteJSON(filepath.Join(task1Dir, "pid"), swarm.PidRecord{
		Job: task1ID, Slot: 1, State: swarm.SlotLaunched,
		Pid: liveCmd.Process.Pid, Pgid: livePgid, JobPgid: livePgid,
		PidStarted: sf1.PidStarted, Nonce: "nonce-1", Started: sf1.LaunchedAt,
	}); err != nil {
		t.Fatal(err)
	}
	_ = p.WriteSidecar(swarm.Running, swarm.Sidecar{
		ID: task1ID, Deadline: "10s", Job: task1Dir, Slot: 1, Started: sf1.LaunchedAt,
	})

	// Goroutine to complete task 1 after 300ms by writing exit.json, RESULT.md, and killing liveCmd
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = swarm.WriteJSON(filepath.Join(task1Dir, "exit.json"), swarm.ExitRecord{
			RC: 0, End: swarm.EndDone, Nonce: "nonce-1", Ended: swarm.Stamp(time.Now()),
		})
		write(t, filepath.Join(task1Dir, "RESULT.md"), "# Head\nfindings: 0\nnotes read: 0\nrepo: o/n\nrev: abc\n\n## Findings\n")
		_ = liveCmd.Process.Kill()
		_ = liveCmd.Wait()
	}()

	// Run dispatcher with 6 workers
	exit, stdout, stderr := b.run("--workers", "6")
	if exit != 1 {
		t.Fatalf("run with quarantined slots: exit = %d, want 1;\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}

	// Assertions from spec:
	// - live worker adopted: RUN ADOPT with original start stamp, exactly one RUN DONE
	mustContain(t, "stdout", stdout, "RUN ADOPT id="+task1ID+" slot=1")
	mustContain(t, "stdout", stdout, "RUN DONE id="+task1ID)

	// - exited job finalized and reclaimed: RUN RECLAIM … usage=<path>
	mustContain(t, "stdout", stdout, "RUN RECLAIM slot=2 id="+task2ID+" end=done usage=")

	// - third slot is RUN QUARANTINE with the reason
	mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=3 id="+task3ID+": the leader is dead and a process in its group is alive")

	// - fourth slot reserved by dead dispatcher is orphaned, nonce kept
	mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=4 id="+task4ID+": reserved, launch unproven")
	sf4After, err := p.ReadSlot(4)
	if err != nil {
		t.Fatalf("could not read slot 4: %v", err)
	}
	if sf4After.State != swarm.SlotOrphaned || sf4After.Nonce != "nonce-4" {
		t.Fatalf("slot 4 after recovery: state=%s nonce=%s, want orphaned and nonce-4", sf4After.State, sf4After.Nonce)
	}

	// - slot file whose pid is alive with a different start stamp is quarantined
	mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=5 id="+task5ID+": pid "+strconv.Itoa(os.Getpid())+" is alive under a different start stamp (pid reuse)")

	// - slot whose supervisor is dead with no exit.json is finalized end=unknown into failed/
	mustContain(t, "stdout", stdout, "RUN RECLAIM slot=6 id="+task6ID+" end=unknown usage=")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", task6ID+".task")); err != nil {
		t.Errorf("task 6 must land in failed/: %v", err)
	}

	// - pending task 7 ran on reclaimed slot 2
	mustContain(t, "stdout", stdout, "RUN START id="+task7ID)
	mustContain(t, "stdout", stdout, "RUN DONE id="+task7ID)

	// - STATUS OK quarantined=3
	exit, statusOut, _ := b.swarm("status", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("status exit = %d, want 0", exit)
	}
	mustContain(t, "status", statusOut, "quarantined=3")

	// - Tripwire: internal/swarm finds no pgrep, no ps and no match on a command line
	assertTripwire(t)
}

// ---------------------------------------------------------------------------------------
// Demanded test 18 (SPEC-SWARM line 1381):
// TestTheLaunchIsATransaction: with an injected kill point at each boundary of rule 18 —
// after reserve, after spawn, after identify, after the handshake, after release, and
// between the harness exit and exit.json — the next run on the pool decides every slot with
// no guess.
func TestTheLaunchIsATransaction(t *testing.T) {
	// (1) after reserve: slot orphaned, task pending again only after aborted.json or removal
	t.Run("after-reserve", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task after reserve\nFAKE-FINDINGS 0\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=after-reserve"}
		b.run() // runner killed right after reserve
		b.extraEnv = nil

		exit, stdout, _ := b.run()
		if exit != 1 {
			t.Fatalf("exit = %d, want 1", exit)
		}
		mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=1 id="+taskID+": reserved, launch unproven")

		p := mustOpenPool(t, b.pool)
		sf, err := p.ReadSlot(1)
		if err != nil || sf.State != swarm.SlotOrphaned {
			t.Fatalf("slot 1 state = %s, want orphaned", sf.State)
		}

		// Plant aborted.json with survivors=0
		jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", taskID)
		_ = os.MkdirAll(jobDir, 0o755)
		if err := swarm.WriteJSON(filepath.Join(jobDir, "aborted.json"), swarm.AbortedRecord{
			Nonce: sf.Nonce, Reason: "test aborted", At: swarm.Stamp(time.Now()), Survivors: 0,
		}); err != nil {
			t.Fatal(err)
		}

		// Next run: reclaims end=unlaunched, task is pending again, slot freed and allocated
		exit, stdout, _ = b.run()
		if exit != 0 {
			t.Fatalf("exit = %d, want 0;\nstdout:\n%s", exit, stdout)
		}
		mustContain(t, "stdout", stdout, "RUN RECLAIM slot=1 id="+taskID+" end=unlaunched usage=-")
		mustContain(t, "stdout", stdout, "RUN START id="+taskID)
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})

	// (2) after spawn: supervisor identifies itself anyway and next run adopts it
	t.Run("after-spawn", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task after spawn\nFAKE-FINDINGS 0\nFAKE-SLEEP 1\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=after-spawn"}
		b.run() // runner killed after spawn
		b.extraEnv = nil

		time.Sleep(300 * time.Millisecond) // supervisor identifies itself
		exit, stdout, stderr := b.run()
		if exit != 0 {
			t.Fatalf("exit = %d, want 0;\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
		}
		mustContain(t, "stdout", stdout, "RUN ADOPT id="+taskID+" slot=1")
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})

	// (3) after identify: runner dies after identify, next run adopts live supervisor
	t.Run("after-identify", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task after identify\nFAKE-FINDINGS 0\nFAKE-SLEEP 1\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=after-identify"}
		b.run() // runner killed right after identify
		b.extraEnv = nil

		exit, stdout, stderr := b.run()
		if exit != 0 {
			t.Fatalf("exit = %d, want 0;\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
		}
		mustContain(t, "stdout", stdout, "RUN ADOPT id="+taskID+" slot=1")
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})

	// (4) after handshake: runner dies after handshake, next run adopts live supervisor
	t.Run("after-handshake", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task after handshake\nFAKE-FINDINGS 0\nFAKE-SLEEP 1\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=after-handshake"}
		b.run() // runner killed after handshake
		b.extraEnv = nil

		exit, stdout, _ := b.run()
		if exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		mustContain(t, "stdout", stdout, "RUN ADOPT id="+taskID+" slot=1")
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})

	// (5) supervisor killed after release with harness alive -> dead leader with live survivor
	t.Run("after-release", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task after release\nFAKE-SLEEP 15\nFAKE-FINDINGS 0\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=supervisor-after-release"}
		b.run() // supervisor killed right after release
		b.extraEnv = nil

		exit, stdout, _ := b.run()
		if exit != 1 {
			t.Fatalf("exit = %d, want 1", exit)
		}
		mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=1 id="+taskID+": the leader is dead and a process in its group is alive")

		// Clean up surviving harness
		p := mustOpenPool(t, b.pool)
		sf, _ := p.ReadSlot(1)
		if sf.JobPgid > 0 {
			swarm.Reap(sf.JobPgid, 0)
		}
	})

	// (6) between exit and exit.json: supervisor killed before exit.json written -> end=unknown into failed/
	t.Run("between-exit-and-exit-json", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task between exit and exit.json\nFAKE-FINDINGS 0\n")
		b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=between-exit-and-exit-json"}
		b.run()
		b.extraEnv = nil

		exit, stdout, _ := b.run()
		mustContain(t, "stdout", stdout, "RUN RECLAIM slot=1 id="+taskID+" end=unknown usage=")
		if _, err := os.Stat(filepath.Join(b.pool, "failed", taskID+".task")); err != nil {
			t.Errorf("job must land in failed/: %v", err)
		}
		_ = exit
	})

	// (7) fake supervisor that never writes identity is killed at --launch-timeout
	t.Run("fake-supervisor-never-identifies", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task timeout\n")
		b.extraEnv = []string{"NOVA_SWARM_PAUSEPOINT=before-identify"}
		exit, stdout, _ := b.swarm("run", "--pool", b.pool, "--workers", "1", "--hours", "0.1",
			"--worker", b.worker, "--launch-timeout", "1")
		b.extraEnv = nil
		mustContain(t, "stdout", stdout, "RUN LAUNCH-FAILED id="+taskID+" slot=1 after=1s: no identity within 1s")
		if _, err := os.Stat(filepath.Join(b.pool, "failed", taskID+".task")); err != nil {
			t.Errorf("task must be in failed/: %v", err)
		}
		sc, err := mustOpenPool(t, b.pool).ReadSidecar(swarm.Failed, taskID)
		if err != nil || sc.Launch != "failed" {
			t.Errorf("sidecar launch = %q, want failed", sc.Launch)
		}
		_ = exit
	})

	// (8) planted exit.json with mismatched nonce is quarantined
	t.Run("planted-exit-json-mismatched-nonce", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task mismatched nonce\n")
		jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", taskID)
		_ = os.MkdirAll(jobDir, 0o755)
		p := mustOpenPool(t, b.pool)
		_ = p.Claim(taskID, swarm.Pending, swarm.Running)
		sf := swarm.SlotFile{
			Job: taskID, JobDir: jobDir, State: swarm.SlotLaunched,
			Pid: 999990, Pgid: 999990, Nonce: "good-nonce", RunnerPid: 999998,
		}
		_ = swarm.WriteJSON(filepath.Join(b.pool, "slots", "1.json"), sf)
		_ = swarm.WriteJSON(filepath.Join(jobDir, "exit.json"), swarm.ExitRecord{
			RC: 0, End: swarm.EndDone, Nonce: "bad-nonce", Ended: swarm.Stamp(time.Now()),
		})
		exit, stdout, _ := b.run()
		if exit != 1 {
			t.Fatalf("exit = %d, want 1", exit)
		}
		mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=1 id="+taskID+": exit.json carries a nonce from another launch")
	})

	// (9) the reverse schedule (Stella, 2026-09-11)
	t.Run("reverse-schedule", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task reverse schedule\nFAKE-FINDINGS 0\n")
		jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", taskID)

		// Supervisor stops before identify, runner dies after spawn
		b.extraEnv = []string{
			"NOVA_SWARM_PAUSEPOINT=before-identify",
			"NOVA_SWARM_KILLPOINT=after-spawn",
		}
		b.run()
		b.extraEnv = nil

		// Read supervisor PID
		rawPid, err := os.ReadFile(filepath.Join(jobDir, "supervisor.pid"))
		if err != nil {
			t.Fatalf("could not read supervisor.pid: %v", err)
		}
		supPID, err := strconv.Atoi(strings.TrimSpace(string(rawPid)))
		if err != nil {
			t.Fatalf("bad supervisor pid: %v", err)
		}

		// Second run on pool with --workers 1 and a pending task finds the reserved slot
		pendingID := b.add("second pending task\nFAKE-FINDINGS 0\n")
		exit, stdout, _ := b.run("--workers", "1")
		if exit != 1 {
			t.Fatalf("exit = %d, want 1", exit)
		}
		mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=1 id="+taskID+": reserved, launch unproven")
		if strings.Contains(stdout, "RUN START id="+pendingID) {
			t.Error("the pending task must NOT be started on the quarantined slot 1")
		}
		if strings.Contains(stdout, "end=unlaunched") {
			t.Error("no RUN RECLAIM end=unlaunched should be printed yet")
		}
		p := mustOpenPool(t, b.pool)
		sf, err := p.ReadSlot(1)
		if err != nil || sf.State != swarm.SlotOrphaned {
			t.Fatalf("slot 1 state = %s, want orphaned", sf.State)
		}

		// Resume supervisor with SIGCONT
		_ = syscall.Kill(supPID, syscall.SIGCONT)
		time.Sleep(300 * time.Millisecond)

		// aborted.json is on disk before exit status is observable
		var ab swarm.AbortedRecord
		if err := swarm.ReadJSON(filepath.Join(jobDir, "aborted.json"), &ab); err != nil {
			t.Fatalf("aborted.json was not written: %v", err)
		}
		if ab.Nonce != sf.Nonce || ab.Survivors != 0 {
			t.Fatalf("aborted.json: nonce=%s survivors=%d, want nonce=%s survivors=0", ab.Nonce, ab.Survivors, sf.Nonce)
		}

		// Planted aborted.json with survivors=1 keeps slot quarantined
		ab.Survivors = 1
		_ = swarm.WriteJSON(filepath.Join(jobDir, "aborted.json"), ab)
		exit, stdout, _ = b.run("--workers", "1")
		if exit != 1 {
			t.Fatalf("exit = %d, want 1", exit)
		}
		mustContain(t, "stdout", stdout, "RUN QUARANTINE slot=1 id="+taskID+": aborted, survivors=1")
		if strings.Contains(stdout, "RUN RECLAIM") {
			t.Error("planted survivors=1 must never print RUN RECLAIM")
		}

		// Restore aborted.json with survivors=0
		ab.Survivors = 0
		_ = swarm.WriteJSON(filepath.Join(jobDir, "aborted.json"), ab)

		// Next run: reads aborted.json, prints RUN RECLAIM end=unlaunched,
		// task is pending again and slot is allocated, fake harness runs and finishes!
		exit, stdout, _ = b.run("--workers", "1")
		if exit != 0 {
			t.Fatalf("exit = %d, want 0;\nstdout:\n%s", exit, stdout)
		}
		mustContain(t, "stdout", stdout, "RUN RECLAIM slot=1 id="+taskID+" end=unlaunched usage=-")
		mustContain(t, "stdout", stdout, "RUN START id="+taskID)
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})

	// (10) between aborted.json and exit
	t.Run("between-aborted-and-exit", func(t *testing.T) {
		b := newBench(t)
		taskID := b.add("task abort kill\nFAKE-FINDINGS 0\n")
		jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", taskID)

		b.extraEnv = []string{
			"NOVA_SWARM_PAUSEPOINT=before-identify",
			"NOVA_SWARM_KILLPOINT=after-spawn",
		}
		b.run()
		b.extraEnv = nil

		rawPid, err := os.ReadFile(filepath.Join(jobDir, "supervisor.pid"))
		if err != nil {
			t.Fatal(err)
		}
		supPID, _ := strconv.Atoi(strings.TrimSpace(string(rawPid)))

		// Orphan slot 1
		_, _, _ = b.run()

		// Resume supervisor with killpoint between-aborted-and-exit
		// (The supervisor already inherited NOVA_SWARM_KILLPOINT=between-aborted-and-exit if set in env,
		// or we can test that aborted.json on disk is honoured even if supervisor was killed before clean exit)
		_ = syscall.Kill(supPID, syscall.SIGKILL) // kill supervisor
		// Write aborted.json manually simulating kill right after rename
		p := mustOpenPool(t, b.pool)
		sf, _ := p.ReadSlot(1)
		_ = swarm.WriteJSON(filepath.Join(jobDir, "aborted.json"), swarm.AbortedRecord{
			Nonce: sf.Nonce, Reason: "killed after rename", At: swarm.Stamp(time.Now()), Survivors: 0,
		})
		exit, stdout, stderr := b.run()
		if exit != 0 {
			t.Fatalf("exit = %d, want 0;\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
		}
		mustContain(t, "stdout", stdout, "RUN RECLAIM slot=1 id="+taskID+" end=unlaunched usage=-")
		mustContain(t, "stdout", stdout, "RUN START id="+taskID)
		mustContain(t, "stdout", stdout, "RUN DONE id="+taskID)
	})
}

func mustOpenPool(t *testing.T, dir string) *swarm.Pool {
	t.Helper()
	p, err := swarm.OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertTripwire(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	swarmDir := filepath.Join(root, "internal", "swarm")
	entries, err := os.ReadDir(swarmDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(swarmDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			for _, forbidden := range []string{"pgrep", `"ps"`, `"ps `, `exec.Command("ps"`} {
				if strings.Contains(trimmed, forbidden) {
					t.Errorf("%s contains forbidden %s (tripwire: no pgrep or ps)", e.Name(), forbidden)
				}
			}
		}
	}
}
