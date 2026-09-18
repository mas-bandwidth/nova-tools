package pulse

// fleet hygiene (docs/SPEC-PULSE.md ## Fleet hygiene, #1139): the mechanical clean-as-we-work
// that keeps a bench's disk from filling while the loop sleeps. `--install` writes the
// hygiene script and a ten-minute systemd timer and runs one sweep, `--dry-run` prints one
// `WOULD rm -rf <path>` for every deletion a sweep would make, and `--status` reads each
// bench's last HYGIENE line and its free space now. Every removal of a computed path goes
// through safepath.RemoveUnder. ssh comes from --ssh so a test fakes it.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

const (
	hygLiveWindow     = 15 * time.Minute
	hygFinishedGrace  = 6 * time.Hour
	hygTempGrace      = 24 * time.Hour
	hygFreeFloorKB    = 25 * 1024 * 1024
	hygCacheCeilKB    = 20 * 1024 * 1024
	hygDefaultTimeout = 120 * time.Second

	hygieneScript = `#!/bin/sh
# Written by nova-pulse fleet hygiene --install. Clean as we work; safe to run by hand.
exec nova-pulse fleet hygiene --install
`
	hygieneTimer = `[Unit]
Description=nova-pulse bench hygiene: clean as we work

[Timer]
OnBootSec=10min
OnUnitActiveSec=10min

[Install]
WantedBy=timers.target
`

	hygieneStatusScript = `log="$HOME/nova-bench/bench-hygiene.log"
if [ -f "$log" ]; then tail -n 200 "$log"; fi
df -P -k "$HOME" 2>/dev/null | tail -n 1`
)

// HygieneInput is everything a hygiene act needs, held apart from flag parsing.
type HygieneInput struct {
	Root    string // the bench home; empty is the user's home
	Benches string // the fleet file, for --status
	SSH     string // the ssh program; empty is "ssh"
	Bench   string // this bench's name; empty is the short hostname
	Timeout time.Duration
	Max     int // at most this many WOULD lines; 0 is all
	Now     func() time.Time
	Stdout  io.Writer
	Stderr  io.Writer
}

// hygieneAction is one deletion a sweep decided on.
type hygieneAction struct {
	path string
	kind string // "job", "slot", "reap" or "cache"
}

// hygieneResult is one sweep's reading: what it would remove and the counts the HYGIENE
// line names.
type hygieneResult struct {
	actions      []hygieneAction
	slots        int
	jobsDeleted  int
	slotsDeleted int
	reaped       int
	cacheDropped bool
	cacheGB      int
	freeBeforeKB int
	freeAfterKB  int
}

func hygieneNow(in HygieneInput) time.Time {
	if in.Now != nil {
		return in.Now().UTC()
	}
	return time.Now().UTC()
}

func hygieneRoot(in HygieneInput) string {
	if strings.TrimSpace(in.Root) != "" {
		return in.Root
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func hygieneBenchName(in HygieneInput) string {
	if strings.TrimSpace(in.Bench) != "" {
		return strings.TrimSpace(in.Bench)
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown"
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return host
}

func hygieneSudo() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}

func hygieneSystemd() bool {
	return exec.Command("systemctl", "--version").Run() == nil
}

// hygieneDFFree is the free space under root in KB, from one df reading. A df that cannot
// answer is zero free: a bench that will not say is not a bench to trust.
func hygieneDFFree(root string) int {
	out, err := exec.Command("df", "-P", "-k", root).Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0
	}
	kb, err := strconv.Atoi(f[3])
	if err != nil {
		return 0
	}
	return kb
}

// hygieneCacheGB is the Go build cache's size in GB, from one du reading.
func hygieneCacheGB(cache string) int {
	out, err := exec.Command("du", "-sk", cache).Output()
	if err != nil {
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return 0
	}
	kb, err := strconv.Atoi(f[0])
	if err != nil {
		return 0
	}
	return kb / (1024 * 1024)
}

// hygieneNamedByProcess reports whether any process names dir on its command line. It is
// the second half of the liveness rule: a job a process names is live whatever its clock
// says.
func hygieneNamedByProcess(dir string) bool {
	if _, err := exec.LookPath("pgrep"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "pgrep", "-f", dir).Run() == nil
}

// hygieneJobLive is the liveness rule: a job whose harness-output.log is under fifteen
// minutes old with no RESULT.md, or that any process names, is live and never touched.
func hygieneJobLive(dir string, now time.Time) bool {
	harness := filepath.Join(dir, "harness-output.log")
	if fi, err := os.Stat(harness); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "RESULT.md")); os.IsNotExist(err) && now.Sub(fi.ModTime()) < hygLiveWindow {
			return true
		}
	}
	return hygieneNamedByProcess(dir)
}

// hygieneJobDeletable is the other half: a harvested job goes whole, and a finished job
// nobody read goes after six hours. A stale unfinished job goes the same way.
func hygieneJobDeletable(dir string, now time.Time) bool {
	if _, err := os.Stat(filepath.Join(dir, ".harvested")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(dir, "RESULT.md")); err == nil {
		return now.Sub(fi.ModTime()) > hygFinishedGrace
	}
	if fi, err := os.Stat(filepath.Join(dir, "harness-output.log")); err == nil {
		return now.Sub(fi.ModTime()) > hygFinishedGrace
	}
	return false
}

// hygienePlan walks the bench's own trees and decides every touch without making one.
func hygienePlan(root string, now time.Time) hygieneResult {
	res := hygieneResult{freeBeforeKB: hygieneDFFree(root)}
	slots, _ := filepath.Glob(filepath.Join(root, "*-swarm-root", "*"))
	for _, slot := range slots {
		fi, err := os.Stat(slot)
		if err != nil || !fi.IsDir() {
			continue
		}
		res.slots++
		entries, _ := filepath.Glob(filepath.Join(slot, "jobs", "*"))
		remaining := 0
		slotLive := false
		for _, dir := range entries {
			dfi, err := os.Stat(dir)
			if err != nil || !dfi.IsDir() {
				continue
			}
			if hygieneJobLive(dir, now) {
				slotLive = true
				remaining++
				continue
			}
			if hygieneJobDeletable(dir, now) {
				res.actions = append(res.actions, hygieneAction{dir, "job"})
				res.jobsDeleted++
				continue
			}
			remaining++
		}
		if !slotLive && remaining == 0 {
			res.actions = append(res.actions, hygieneAction{slot, "slot"})
			res.slotsDeleted++
			for _, sub := range []string{"data", "tmp", "scratch"} {
				p := filepath.Join(slot, sub)
				if _, err := os.Stat(p); err == nil {
					res.actions = append(res.actions, hygieneAction{p, "reap"})
					res.reaped++
				}
			}
		}
	}

	work, _ := filepath.Glob(filepath.Join(root, "rowan-working", "tmp", "*"))
	for _, dir := range work {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		if hygieneJobLive(dir, now) {
			continue
		}
		if hygieneJobDeletable(dir, now) {
			res.actions = append(res.actions, hygieneAction{dir, "reap"})
			res.reaped++
		}
	}

	temps, _ := filepath.Glob(filepath.Join(root, "runner-nova-tools-*", "_work", "_temp", "*"))
	for _, p := range temps {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) > hygTempGrace {
			res.actions = append(res.actions, hygieneAction{p, "reap"})
			res.reaped++
		}
	}

	cache := filepath.Join(root, ".cache", "go-build")
	if _, err := os.Stat(cache); err == nil {
		res.cacheGB = hygieneCacheGB(cache)
		if res.freeBeforeKB < hygFreeFloorKB || res.cacheGB > hygCacheCeilKB/(1024*1024) {
			res.actions = append(res.actions, hygieneAction{cache, "cache"})
			res.cacheDropped = true
		}
	}
	res.freeAfterKB = res.freeBeforeKB
	return res
}

// hygieneExecute carries out a plan under the safe remover.
func hygieneExecute(root string, res *hygieneResult, stderr io.Writer) {
	for _, a := range res.actions {
		if err := safepath.RemoveUnder(root, a.path); err != nil {
			fmt.Fprintf(stderr, "HYGIENE NOTE %s: %s\n", oneline.Field(a.path), oneline.Err(err))
		}
	}
	res.freeAfterKB = hygieneDFFree(root)
}

// HygieneDryRun walks the same trees as a sweep and prints one WOULD line per deletion it
// would make, deleting nothing.
func HygieneDryRun(in HygieneInput) int {
	root := hygieneRoot(in)
	res := hygienePlan(root, hygieneNow(in))
	printed := 0
	for _, a := range res.actions {
		if in.Max > 0 && printed >= in.Max {
			break
		}
		fmt.Fprintf(in.Stdout, "WOULD rm -rf %s\n", a.path)
		printed++
	}
	return 0
}

// HygieneInstall writes the hygiene script and its ten-minute timer once, runs one sweep,
// and prints one HYGIENE line. It refuses without sudo or systemd and refuses studio.
func HygieneInstall(in HygieneInput) int {
	name := hygieneBenchName(in)
	if strings.EqualFold(name, "studio") {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)\n")
		return 2
	}
	if os.Geteuid() != 0 && !hygieneSudo() {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=%s no-sudo (run it under sudo, or install the script by hand)\n", oneline.Field(name))
		return 2
	}
	if !hygieneSystemd() {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=%s no-systemd (install the timer by hand)\n", oneline.Field(name))
		return 2
	}

	root := hygieneRoot(in)
	res := hygienePlan(root, hygieneNow(in))
	hygieneExecute(root, &res, in.Stderr)

	script := filepath.Join(root, "nova-bench", "bench-hygiene.sh")
	timer := filepath.Join(root, "nova-bench", "bench-hygiene.timer")
	wroteScript := hygieneWriteIfChanged(script, hygieneScript, 0o755)
	wroteTimer := hygieneWriteIfChanged(timer, hygieneTimer, 0o644)
	if wroteScript || wroteTimer {
		_ = exec.Command("systemctl", "enable", "--now", "bench-hygiene.timer").Run()
	}

	line := hygieneLine(name, res)
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err == nil {
		if f, err := os.OpenFile(filepath.Join(root, "nova-bench", "bench-hygiene.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
	fmt.Fprintln(in.Stdout, line)
	return 0
}

// hygieneLine renders the one line per run.
func hygieneLine(host string, res hygieneResult) string {
	cache := "kept"
	if res.cacheDropped {
		cache = "dropped"
	}
	return fmt.Sprintf("HYGIENE %s slots=%d reaped=%d jobs-deleted=%d slots-deleted=%d cache=%s(%dG) free %d -> %d",
		oneline.Field(host), res.slots, res.reaped, res.jobsDeleted, res.slotsDeleted, cache, res.cacheGB,
		res.freeBeforeKB/(1024*1024), res.freeAfterKB/(1024*1024))
}

// hygieneWriteIfChanged writes body only when the file does not already hold it, so a
// second --install rewrites nothing that matches.
func hygieneWriteIfChanged(path, body string, mode os.FileMode) bool {
	if raw, err := os.ReadFile(path); err == nil && string(raw) == body {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return false
	}
	return true
}

// HygieneStatus reads each bench's last HYGIENE line and its free space now, one FLEET
// line per bench. A bench that answers no HYGIENE line is NO-HYGIENE, exit 2; an
// unreachable bench is exit 3. studio is refused before any bench is contacted.
func HygieneStatus(in HygieneInput) int {
	if strings.TrimSpace(in.Benches) == "" {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: --status without --benches; refusing to guess (supply the benches file)\n")
		return 2
	}
	benches, err := ReadFleetBenches(in.Benches)
	if err != nil {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	names := make([]string, 0, len(benches))
	for name := range benches {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.EqualFold(strings.TrimSpace(name), "studio") {
			fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)\n")
			return 2
		}
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = hygDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lines := make([]string, len(names))
	codes := make([]int, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			raw, err := fleetSSH(ctx, in.SSH, b.SSH, hygieneStatusScript)
			if err != nil && strings.TrimSpace(raw) == "" {
				lines[i] = "FLEET " + oneline.Field(b.Name) + " UNREACHABLE " + oneline.Err(err)
				codes[i] = 3
				return
			}
			hyg := hygieneLastLine(raw)
			if hyg == "" {
				lines[i] = "FLEET " + oneline.Field(b.Name) + " NO-HYGIENE (run fleet hygiene --install)"
				codes[i] = 2
				return
			}
			lines[i] = hygieneStatusLine(b.Name, hyg, hygieneDFFreeOf(raw))
		}(i, benches[name])
	}
	wg.Wait()

	exit := 0
	shown := 0
	for i := range lines {
		if in.Max > 0 && shown >= in.Max {
			break
		}
		fmt.Fprintln(in.Stdout, lines[i])
		shown++
		switch codes[i] {
		case 3:
			exit = 3
		case 2:
			if exit == 0 {
				exit = 2
			}
		}
	}
	return exit
}

// hygieneLastLine is the last HYGIENE line in a bench's answer.
func hygieneLastLine(raw string) string {
	last := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "HYGIENE ") {
			last = line
		}
	}
	return last
}

// hygieneDFFreeOf is the free space in GB from a df line in a bench's answer.
func hygieneDFFreeOf(raw string) int {
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.Contains(f[0], "/") {
			continue
		}
		kb, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		return kb / (1024 * 1024)
	}
	return 0
}

// hygieneStatusLine folds a stored HYGIENE line and the free space now into one FLEET line.
func hygieneStatusLine(name, hyg string, freeGB int) string {
	f := strings.Fields(hyg)
	body := ""
	if len(f) >= 7 {
		body = strings.Join(f[2:7], " ")
	}
	return fmt.Sprintf("FLEET %s HYGIENE %s free=%dG", oneline.Field(name), body, freeGB)
}
