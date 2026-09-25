// The reap verb clears what a SIGKILL left behind, which is the one hole in the run
// verb's contract that the run verb cannot close from inside.
//
// Measured on the Studio in a 20-run soak, 2026-09-18. `nova-sandbox run` deletes its
// volume on every path out — a clean exit, an error, a signal it can catch, a --timeout —
// and a delete that fails prints SANDBOX LEAK. A SIGKILL is none of those: the tool is
// gone between one instruction and the next, so there is no path out to take and no line
// to print. Both halves of the containment then leak. The volume stays mounted, and the
// contained command's own `sleep 60` is reparented to PID 1 with its working directory
// ON that volume, which holds it open against every unmount — so the leak is not even
// one a later `diskutil apfs deleteVolume` clears by itself. `nova-sandbox check` says
// nothing about it: check asks what the backend can enforce, not what this machine is
// still holding.
//
// So: a verb that names the debt and clears it. It touches nothing it did not make —
// `nova-` is the whole of its authority, exactly as the run verb's delete is — and it
// never takes a volume from a LIVE run, which is what the owner marker at the volume root
// is for. A reaper that cannot tell a working card from an orphan is a reaper nobody
// dares to run, and a reaper nobody runs is the same as no reaper at all.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// ownerMarker is the file a run leaves at its volume's root: the pid of the tool that
// made the volume and the moment that process started. Both, because a pid is a small
// number the operating system hands out again, and a marker matched on the pid alone
// would keep an orphan forever the first time the number came round.
//
// It lives on the volume because the volume is the only thing the run owns — and the
// volume is thrown away with it, so the marker is never a file anyone cleans up.
const ownerMarker = ".nova-sandbox-owner"

// reapGrace is how long a process that holds a volume open is given after SIGTERM before
// SIGKILL. Production code's own wait, never a test's.
const reapGrace = 2 * time.Second

// reapRemedy is the one remedy line every refusal of this verb carries.
const reapRemedy = "run: nova-sandbox reap [--dry-run]"

const reapUsage = `nova-sandbox reap: clear the disposable volumes a killed run left behind (darwin)

usage:
  nova-sandbox reap [--dry-run]

  --dry-run   print what a reap would take and touch NOTHING: no signal is sent,
              no volume is deleted.

` + "`run`" + ` deletes its volume on every path out it can take. A SIGKILL is not one of
them: the tool is gone before it can delete, its volume stays mounted, and the
command's own children are reparented to PID 1 still holding that volume open --
so nothing survives to print SANDBOX LEAK and the machine is dirty in silence.

reap lists every volume named nova-*, and for each one that no LIVE run owns it
kills what holds the volume open (SIGTERM, then SIGKILL after a short grace) and
deletes the volume. A volume whose ` + ownerMarker + ` names a running tool --
the pid alive AND started when the marker says -- is reported and left alone.

One line per volume and one closing count, on stderr:

  SANDBOX REAP volume=<name> procs=<n> deleted=<yes|no>
  SANDBOX REAP OK volumes=<n>

Exit 0 when the machine is clean, 3 when anything remained -- including every
--dry-run that found something, so that ` + "`nova-sandbox reap --dry-run`" + ` is a gate a
card can end on.
`

// The seams. Each is a var so a test replaces it, and none of them is reachable from
// caller input: this verb takes one boolean.
var (
	// reapProcs answers which processes hold a mount open.
	reapProcs = processesUnder
	// reapSignal sends one signal to one process.
	reapSignal = signalProcess
	// reapAlive answers whether a pid is still running.
	reapAlive = processAlive
	// reapProcStart is what the operating system says about when a process started.
	reapProcStart = processStart
	// reapGraceSleep is the wait between the TERM and the KILL.
	reapGraceSleep = func() { time.Sleep(reapGrace) }
)

// reapFlags is the verb's argv: one flag, parsed by hand like every other verb's.
type reapFlags struct {
	dryRun bool
	help   bool
	bad    []sandbox.Refusal
}

func parseReap(args []string) reapFlags {
	var f reapFlags
	for _, a := range args {
		switch a {
		case "--dry-run":
			f.dryRun = true
		case "help", "--help", "-h":
			f.help = true
		default:
			f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
				Text: oneline.Escape(a) + " is not a flag of the reap verb; run: nova-sandbox reap --help"})
		}
	}
	return f
}

// reapVerb is the verb: the argv, the platform gate, and then reapAll.
func reapVerb(args []string, stdout, stderr io.Writer) int {
	f := parseReap(args)
	// The question, before any complaint about the argv that did not ask it.
	if f.help {
		fmt.Fprint(stdout, reapUsage)
		return 0
	}
	if len(f.bad) > 0 {
		code := refuseAll(stderr, f.bad)
		fmt.Fprintln(stderr, reapRemedy)
		return code
	}
	// One gate, shared with run: a platform with no disposable volumes has none to reap,
	// and the refusal says where the disposable place is there instead.
	if line, remedy, refused := noDisposableBody(runtime.GOOS); refused {
		fmt.Fprintln(stderr, line)
		fmt.Fprintln(stderr, remedy)
		return sandbox.ExitRefused
	}
	return reapAll(f.dryRun, stderr)
}

// reapAll is the whole of the work, with the platform already known. It is separate from
// the verb so the logic is tested on every platform with the disk, the process table, the
// signals and the grace all replaced.
func reapAll(dryRun bool, stderr io.Writer) int {
	vols, err := step(stderr, "list", func() ([]diskVolume, error) { return runVolumes.List() })
	if err != nil {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=volume_failed: the volumes on this machine could not be listed: %s\n%s\n",
			oneline.Err(err), reapRemedy)
		return sandbox.ExitRefused
	}
	remained := false
	for _, vol := range vols {
		if reapOne(dryRun, stderr, vol) {
			remained = true
		}
	}
	fmt.Fprintf(stderr, "SANDBOX REAP OK volumes=%d\n", len(vols))
	if remained {
		return exitLeak
	}
	return 0
}

// reapOne is one volume: whose it is, what holds it, and whether it goes. It answers
// whether anything REMAINED — a live run's volume does not count, because a card that is
// working is not a debt.
func reapOne(dryRun bool, stderr io.Writer, vol diskVolume) (remained bool) {
	name := oneline.Field(vol.Name)
	var procs []int
	if vol.Mount != "" {
		found, err := reapProcs(vol.Mount)
		if err != nil {
			fmt.Fprintf(stderr, "SANDBOX NOTE the processes holding %s open could not be listed: %s; the volume is left alone rather than deleted out from under something\n",
				oneline.Field(vol.Mount), oneline.Err(err))
			fmt.Fprintf(stderr, "SANDBOX REAP volume=%s procs=0 deleted=no\n", name)
			return true
		}
		procs = found
	}
	if pid, live := volumeIsLive(vol.Mount); live {
		fmt.Fprintf(stderr, "SANDBOX REAP volume=%s procs=%d deleted=no\n", name, len(procs))
		fmt.Fprintf(stderr, "SANDBOX NOTE %s belongs to a live run (pid=%d); a reap never takes a volume out from under a working card\n", name, pid)
		return false
	}
	if dryRun {
		fmt.Fprintf(stderr, "SANDBOX REAP volume=%s procs=%d deleted=no\n", name, len(procs))
		// Found, and still there: a dry run that answered 0 would be a gate that passes
		// on a dirty machine.
		return true
	}
	if left := killProcesses(procs); left > 0 {
		fmt.Fprintf(stderr, "SANDBOX NOTE %d process(es) still hold %s open after SIGKILL; the volume cannot be unmounted while they do\n", left, oneline.Field(vol.Mount))
	}
	if err := runVolumes.Delete(vol.Disk); err != nil {
		fmt.Fprintf(stderr, "SANDBOX REAP volume=%s procs=%d deleted=no\n", name, len(procs))
		fmt.Fprintf(stderr, "SANDBOX LEAK name=%s volume=%s remedy=\"diskutil apfs deleteVolume %s\"\n",
			name, oneline.Field(vol.Disk), oneline.Field(vol.Disk))
		fmt.Fprintf(stderr, "SANDBOX NOTE the volume could not be deleted: %s\n", oneline.Err(err))
		return true
	}
	fmt.Fprintf(stderr, "SANDBOX REAP volume=%s procs=%d deleted=yes\n", name, len(procs))
	return false
}

// killProcesses asks, waits, and then takes: SIGTERM to everything holding the volume,
// the grace, then SIGKILL to whatever is left. It answers how many are STILL there after
// that, because those are the ones that will make the unmount fail, and a reader who is
// told the delete failed and not why has to go and find out.
func killProcesses(pids []int) int {
	if len(pids) == 0 {
		return 0
	}
	for _, pid := range pids {
		_ = reapSignal(pid, syscall.SIGTERM)
	}
	reapGraceSleep()
	var left []int
	for _, pid := range pids {
		if reapAlive(pid) {
			left = append(left, pid)
		}
	}
	for _, pid := range left {
		_ = reapSignal(pid, syscall.SIGKILL)
	}
	if len(left) == 0 {
		return 0
	}
	reapGraceSleep()
	still := 0
	for _, pid := range left {
		if reapAlive(pid) {
			still++
		}
	}
	return still
}

// volumeIsLive reads the owner marker and answers the one question a reap may not get
// wrong. Both halves must hold: the pid is RUNNING, and the process running under that
// number is the one that wrote the marker. A pid is a small number the operating system
// hands out again, and a guard on the number alone would keep an orphan for as long as
// some unrelated process happened to wear it.
//
// Every uncertainty resolves to NOT LIVE, with one exception. No marker, an unreadable
// marker, a marker with no pid: those are orphans, because the alternative is a volume
// that is kept forever, which is the leak this verb exists to end. The exception is a pid
// that IS alive whose start time cannot be read at all — there the process is real and
// only the evidence is missing, and a reap that killed it would be guessing.
func volumeIsLive(mount string) (int, bool) {
	if mount == "" {
		return 0, false
	}
	pid, start, ok := readOwnerMarker(mount)
	if !ok || !reapAlive(pid) {
		return pid, false
	}
	now, err := reapProcStart(pid)
	if err != nil || strings.TrimSpace(now) == "" || start == "-" {
		return pid, true
	}
	return pid, strings.TrimSpace(now) == strings.TrimSpace(start)
}

// readOwnerMarker reads the two fields the run verb wrote. Anything else is not a marker.
func readOwnerMarker(mount string) (pid int, start string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(mount, ownerMarker))
	if err != nil {
		return 0, "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, cut := strings.CutPrefix(strings.TrimSpace(line), "pid="); cut {
			pid, _ = strconv.Atoi(strings.TrimSpace(rest))
		}
		if rest, cut := strings.CutPrefix(strings.TrimSpace(line), "start="); cut {
			start = strings.TrimSpace(rest)
		}
	}
	if pid <= 0 {
		return 0, "", false
	}
	return pid, start, true
}

// writeOwnerMarker is the run verb's half: one small file at the volume root, written
// before the command starts, so that a reap after a SIGKILL can tell this volume from one
// a working card is using. A start time the machine will not give is written as `-`,
// which volumeIsLive reads as "alive means live" — the conservative answer.
func writeOwnerMarker(mount string, pid int) error {
	start, err := reapProcStart(pid)
	if err != nil || strings.TrimSpace(start) == "" {
		start = "-"
	}
	body := fmt.Sprintf("pid=%d\nstart=%s\n", pid, strings.TrimSpace(start))
	return os.WriteFile(filepath.Join(mount, ownerMarker), []byte(body), 0o600)
}
