package pulse

// The hygiene verbs: the Go half of the bench's bin/bench-hygiene.sh, the
// clean-as-we-work pass a systemd timer runs on every bench. It is six verbs,
// never a bare `rm`:
//
//	run [--dry-run]              reap dead slots, delete read jobs, drop the cache when low; one HYGIENE line
//	reap <slot>                  delete <slot>/data, <slot>/tmp and each job's scratch
//	delete-job <slot> <job>      delete <root>/<slot>/jobs/<job> whole
//	delete-slot <slot>           delete an empty slot
//	drop-cache                   delete <home>/.cache/go-build
//	log [n]                      the last n lines of the per-bench action log
//
// Every deletion goes through internal/safepath.RemoveUnder: the path is the
// join of one of the two literal roots under <home>, a slot name and a job name
// that match [A-Za-z0-9._-]+, resolved, checked for symlinks, and checked to sit
// strictly below its root. Nothing else can be removed by this verb.
//
// Each deletion is one line in <home>/hygiene.log: <utc> <verb> <path>, the
// format the shell timer wrote.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// HygieneProcs answers whether any process names a slot in its command line or
// its working directory. The real one reads /proc; tests drive a fake.
type HygieneProcs interface {
	Busy(path string) bool
}

// HygieneDisk answers the two numbers the run verb's one line needs.
type HygieneDisk interface {
	FreeGB() int
	Free() string
	SizeGB(path string) int
}

// HygieneInput is the hygiene verb family's input, held apart from flag parsing.
type HygieneInput struct {
	Verb   string // run, reap, delete-job, delete-slot, drop-cache, log
	Slot   string
	Job    string
	DryRun bool
	N      int // log: how many trailing lines

	Home      string   // every root, the log and the cache hang under it
	Roots     []string // default <home>/rowan-swarm-root, <home>/rowan-working/tmp
	LogPath   string   // default <home>/hygiene.log
	CachePath string   // default <home>/.cache/go-build
	Hostname  string   // default the short local host name

	Now    func() time.Time
	Procs  HygieneProcs
	Disk   HygieneDisk
	Stdout io.Writer
	Stderr io.Writer
}

type hygiene struct {
	in    HygieneInput
	now   time.Time
	roots []string
	log   string
	cache string
	host  string
	disk  HygieneDisk
}

// Hygiene runs one hygiene subcommand and returns its exit code: 0 when it ran,
// 2 on every refusal. One HYGIENE line is the only stdout a run prints; each
// deletion is one line in <home>/hygiene.log.
func Hygiene(in HygieneInput) int {
	h, code := newHygiene(in)
	if code != 0 {
		return code
	}
	switch in.Verb {
	case "run":
		return h.run()
	case "reap":
		return h.reapVerb()
	case "delete-job":
		return h.deleteJobVerb()
	case "delete-slot":
		return h.deleteSlotVerb()
	case "drop-cache":
		return h.dropCacheVerb()
	case "log":
		return h.logVerb()
	}
	return h.refuse(fmt.Sprintf("HYGIENE REFUSED: unknown subcommand %q (pass run, reap, delete-job, delete-slot, drop-cache or log)", oneline.Field(in.Verb)))
}

func newHygiene(in HygieneInput) (*hygiene, int) {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if strings.TrimSpace(in.Home) == "" || !filepath.IsAbs(in.Home) {
		fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: --home is required and is an absolute path, got %q (pass the bench home; every root hangs under it)\n", oneline.Field(in.Home))
		return nil, 2
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	roots := in.Roots
	if len(roots) == 0 {
		roots = []string{filepath.Join(in.Home, "rowan-swarm-root"), filepath.Join(in.Home, "rowan-working", "tmp")}
	}
	logPath := in.LogPath
	if logPath == "" {
		logPath = filepath.Join(in.Home, "hygiene.log")
	}
	cache := in.CachePath
	if cache == "" {
		cache = filepath.Join(in.Home, ".cache", "go-build")
	}
	host := in.Hostname
	if host == "" {
		host = shortHost()
	}
	disk := in.Disk
	if disk == nil {
		disk = OSDisk{Home: in.Home}
	}
	return &hygiene{in: in, now: now().UTC(), roots: roots, log: logPath, cache: cache, host: host, disk: disk}, 0
}

func shortHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "bench"
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// appendLog writes one <utc> <rest> line to the per-bench action log.
func (h *hygiene) appendLog(rest string) {
	f, err := os.OpenFile(h.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(h.in.Stderr, "HYGIENE NOTE %s could not be written: %s\n", oneline.Field(h.log), oneline.Err(err))
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", h.now.UTC().Format(time.RFC3339), rest)
}

// removeUnder is the one removal: safepath decides, the log records it, and a
// refusal names the path on stderr. In a dry run it prints WOULD and changes
// nothing, not even the log.
func (h *hygiene) removeUnder(verb, path string, roots ...string) bool {
	if h.in.DryRun {
		fmt.Fprintf(h.in.Stdout, "WOULD %s %s\n", verb, path)
		return true
	}
	if err := safepath.RemoveUnderRoots(path, roots...); err != nil {
		fmt.Fprintf(h.in.Stderr, "HYGIENE REFUSED: %s (pass a path strictly below one of the two roots)\n", oneline.Err(err))
		return false
	}
	h.appendLog(verb + " " + path)
	return true
}

func (h *hygiene) remove(verb, path string) bool { return h.removeUnder(verb, path, h.roots...) }

func (h *hygiene) refuse(msg string) int {
	fmt.Fprintln(h.in.Stderr, msg)
	return 2
}

// slotPath is the slot with this name when one root holds it as a real
// directory: it is the join of a literal root and a safe name, resolved and
// checked to sit strictly below the root.
func (h *hygiene) slotPath(name string) (string, bool) {
	if !safepath.NameOK(name) {
		return "", false
	}
	for _, root := range h.roots {
		p := filepath.Join(root, name)
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := safepath.ResolvedUnder(p, root); err != nil {
			continue
		}
		return p, true
	}
	return "", false
}

func (h *hygiene) rootsPhrase() string {
	var parts []string
	for _, r := range h.roots {
		parts = append(parts, oneline.Field(r))
	}
	return strings.Join(parts, " or ")
}

func (h *hygiene) reapVerb() int {
	slot, ok := h.slotPath(h.in.Slot)
	if !ok {
		return h.refuse(fmt.Sprintf("REAP REFUSED: %s is not a slot (pass a directory name directly below %s)", oneline.Field(h.in.Slot), h.rootsPhrase()))
	}
	h.reapSlot(slot)
	return 0
}

func (h *hygiene) deleteJobVerb() int {
	slot, ok := h.slotPath(h.in.Slot)
	if !ok {
		return h.refuse(fmt.Sprintf("DELETE-JOB REFUSED: %s is not a slot (pass a directory name directly below %s)", oneline.Field(h.in.Slot), h.rootsPhrase()))
	}
	if !safepath.NameOK(h.in.Job) {
		return h.refuse(fmt.Sprintf("DELETE-JOB REFUSED: %s is not a job name (use letters, digits, dot, dash or underscore)", oneline.Field(h.in.Job)))
	}
	job := filepath.Join(slot, "jobs", h.in.Job)
	if !dirExists(job) {
		return h.refuse(fmt.Sprintf("DELETE-JOB REFUSED: %s is not a job (pass a directory name below %s)", oneline.Field(job), oneline.Field(filepath.Join(slot, "jobs"))))
	}
	if !h.remove("delete-job", job) {
		return 2
	}
	return 0
}

func (h *hygiene) deleteSlotVerb() int {
	slot, ok := h.slotPath(h.in.Slot)
	if !ok {
		return h.refuse(fmt.Sprintf("DELETE-SLOT REFUSED: %s is not a slot (pass a directory name directly below %s)", oneline.Field(h.in.Slot), h.rootsPhrase()))
	}
	if !emptyDir(filepath.Join(slot, "jobs")) {
		return h.refuse(fmt.Sprintf("DELETE-SLOT REFUSED: %s still holds jobs (delete its jobs first)", oneline.Field(slot)))
	}
	if !h.remove("delete-slot", slot) {
		return 2
	}
	return 0
}

func (h *hygiene) dropCacheVerb() int {
	if !dirExists(h.cache) {
		return 0
	}
	if !h.removeUnder("drop-cache", h.cache, filepath.Join(h.in.Home, ".cache")) {
		return 2
	}
	return 0
}

func (h *hygiene) logVerb() int {
	n := h.in.N
	if n <= 0 {
		n = 20
	}
	raw, err := os.ReadFile(h.log)
	if err != nil {
		return 0
	}
	text := strings.TrimRight(string(raw), "\n")
	if text == "" {
		return 0
	}
	lines := strings.Split(text, "\n")
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	for _, line := range lines {
		fmt.Fprintln(h.in.Stdout, line)
	}
	return 0
}

// run is the timer's verb: it walks both roots, leaves every live slot alone,
// reaps the dead, deletes the read jobs, drops the cache when the disk is low,
// and prints one HYGIENE line (and, unless --dry-run, logs it too).
func (h *hygiene) run() int {
	before := h.disk.Free()
	slots, reaped, jobs, dropped := 0, 0, 0, 0
	for _, root := range h.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !safepath.NameOK(e.Name()) {
				continue
			}
			slot := filepath.Join(root, e.Name())
			if _, err := safepath.ResolvedUnder(slot, root); err != nil {
				continue
			}
			slots++
			if h.liveSlot(slot) {
				continue
			}
			if dirExists(filepath.Join(slot, "data")) || dirExists(filepath.Join(slot, "tmp")) {
				h.reapSlot(slot)
				reaped++
			}
			newest := h.newestLog(slot)
			for _, job := range h.jobDirs(slot) {
				_, harvested := os.Stat(filepath.Join(job, ".harvested"))
				stale := newest > 0 && (h.now.Unix()-newest)/3600 >= 6
				if harvested == nil || stale {
					if h.remove("delete-job", job) {
						jobs++
					}
				}
			}
			if emptyDir(filepath.Join(slot, "jobs")) && !dirExists(filepath.Join(slot, "data")) {
				if h.remove("delete-slot", slot) {
					dropped++
				}
			}
		}
	}
	size := h.disk.SizeGB(h.cache)
	cache := "kept"
	if h.disk.FreeGB() < 25 || size > 20 {
		if h.removeUnder("drop-cache", h.cache, filepath.Join(h.in.Home, ".cache")) {
			cache = "dropped"
		}
	}
	line := fmt.Sprintf("HYGIENE %s slots=%d reaped=%d jobs-deleted=%d slots-deleted=%d cache=%s(%dG) free %s -> %s",
		h.host, slots, reaped, jobs, dropped, cache, size, before, h.disk.Free())
	if !h.in.DryRun {
		h.appendLog(line)
	}
	fmt.Fprintln(h.in.Stdout, line)
	return 0
}

// reapSlot is the reap verb's body: <slot>/data, <slot>/tmp and each job's
// scratch. It never removes a job directory.
func (h *hygiene) reapSlot(slot string) {
	for _, name := range []string{"data", "tmp"} {
		if p := filepath.Join(slot, name); dirExists(p) {
			h.remove("reap", p)
		}
	}
	for _, job := range h.jobDirs(slot) {
		for _, sub := range []string{"scratch", ".nova-sandbox-tmp", filepath.Join("repo", "scratch")} {
			if p := filepath.Join(job, sub); dirExists(p) {
				h.remove("reap", p)
			}
		}
	}
}

func (h *hygiene) jobDirs(slot string) []string {
	entries, err := os.ReadDir(filepath.Join(slot, "jobs"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !safepath.NameOK(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(slot, "jobs", e.Name()))
	}
	return out
}

// liveSlot is the shell's liveness rule: a job whose harness log is under 15
// minutes old with no RESULT.md, or any process naming the slot in its command
// line or cwd, holds the whole slot.
func (h *hygiene) liveSlot(slot string) bool {
	now := h.now.Unix()
	for _, job := range h.jobDirs(slot) {
		info, err := os.Stat(filepath.Join(job, "harness-output.log"))
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err == nil {
			continue
		}
		if (now-info.ModTime().Unix())/60 < 15 {
			return true
		}
	}
	return h.in.Procs != nil && h.in.Procs.Busy(slot)
}

// newestLog is the newest harness-output.log mtime under the slot's jobs, or 0.
func (h *hygiene) newestLog(slot string) int64 {
	var newest int64
	for _, job := range h.jobDirs(slot) {
		info, err := os.Stat(filepath.Join(job, "harness-output.log"))
		if err != nil {
			continue
		}
		if m := info.ModTime().Unix(); m > newest {
			newest = m
		}
	}
	return newest
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func emptyDir(p string) bool {
	entries, err := os.ReadDir(p)
	return err != nil || len(entries) == 0
}

// OSDisk is the real disk: df for the free space, du for the build cache. It
// fails open -- a number it cannot read never turns into a deletion.
type OSDisk struct{ Home string }

func (o OSDisk) Free() string {
	out, err := exec.Command("df", "-h", o.Home).Output()
	if err != nil {
		return "-"
	}
	if field := dfField(string(out), 3); field != "" {
		return field
	}
	return "-"
}

func (o OSDisk) FreeGB() int {
	out, err := exec.Command("df", "-BG", o.Home).Output()
	if err != nil {
		return 1 << 30
	}
	if n, ok := parseGB(dfField(string(out), 3)); ok {
		return n
	}
	return 1 << 30
}

func (o OSDisk) SizeGB(path string) int {
	out, err := exec.Command("du", "-sBG", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return 0
	}
	n, _ := parseGB(fields[0])
	return n
}

// dfField returns the wanted whitespace field of df's second line (the data
// line), or "" when the output has no such line or field.
func dfField(out string, index int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Fields(lines[1])
	if index < 0 || len(fields) <= index {
		return ""
	}
	return fields[index]
}

func parseGB(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "G"))
	if err != nil {
		return 0, false
	}
	return n, true
}

// Busy is the authoritative liveness probe: any process whose cwd is at or
// below the path, or whose command line names it, holds the slot. It reads
// /proc and never signals anything.
func (o OSProcs) Busy(path string) bool {
	if path == "" {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	sep := string(os.PathSeparator)
	for _, e := range entries {
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join("/proc", pid, "cwd")); err == nil {
			if cwd == path || strings.HasPrefix(cwd, path+sep) {
				return true
			}
		}
		if raw, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil {
			if args := strings.ReplaceAll(string(raw), "\x00", " "); strings.Contains(args, path) {
				return true
			}
		}
	}
	return false
}
