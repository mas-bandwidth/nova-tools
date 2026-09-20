package pulse

// Fleet hygiene as a verb every bench installs (docs/SPEC-PULSE.md ## Fleet
// hygiene, #1139): the bench timer that deletes read job dirs and trims
// caches. `--install` writes the hygiene script and the ten-minute systemd
// timer on the bench it runs on (idempotent, refuses without sudo or without
// systemd), `--dry-run` prints what `--install` would write and writes
// nothing, and `--status --benches <file>` prints the last HYGIENE line and
// free space per bench. Every remote step goes through FleetRunner/ssh from
// --ssh so a test fakes the bench; no test reaches a machine.

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
)

// FleetHygieneInput is everything a fleet hygiene act needs, held apart from
// flag parsing. Root scopes the local acts (--install, --dry-run): empty is
// the user's home, set is a test's TempDir so no test writes HOME or /etc.
type FleetHygieneInput struct {
	Root    string
	Benches string
	SSH     string
	Timeout time.Duration
	Max     int
	Stdout  io.Writer
	Stderr  io.Writer
}

const (
	fleetHygieneScript = `#!/bin/sh
# Written by nova-pulse fleet hygiene --install. Clean as we work; safe to run by hand.
# The timer runs this every ten minutes: reap dead slots, delete read jobs
# and drop the build cache when the disk is low, printing one HYGIENE line.
exec nova-pulse hygiene run --home "$HOME"
`
	fleetHygieneTimer = `[Unit]
Description=nova-pulse bench hygiene: clean as we work

[Service]
Type=oneshot
ExecStart=%h/nova-bench/bench-hygiene.sh

[Timer]
OnBootSec=10min
OnUnitActiveSec=10min

[Install]
WantedBy=timers.target
`

	fleetHygieneStatusScript = `log="$HOME/hygiene.log"
if [ -f "$log" ]; then grep '^HYGIENE ' "$log" | tail -n 1; fi
df -P -k "$HOME" 2>/dev/null | tail -n 1`
)

func fleetHygieneRoot(in FleetHygieneInput) string {
	if strings.TrimSpace(in.Root) != "" {
		return in.Root
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return ""
}

func fleetHygieneHost() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "bench"
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return host
}

func fleetHygieneSudoOK() bool {
	if os.Geteuid() == 0 {
		return true
	}
	return exec.Command("sudo", "-n", "true").Run() == nil
}

func fleetHygieneSystemdOK() bool {
	return exec.Command("systemctl", "--version").Run() == nil
}

func fleetHygieneWriteIfChanged(path, body string, mode os.FileMode) bool {
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

// FleetHygieneInstall writes the script and the ten-minute timer once. A
// second --install rewrites nothing that already matches. It refuses without
// sudo or without systemd, and refuses studio, exit 2, writing nothing.
func FleetHygieneInstall(in FleetHygieneInput) int {
	host := fleetHygieneHost()
	if strings.EqualFold(host, "studio") {
		fmt.Fprintln(in.Stderr, "FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)")
		return 2
	}
	if !fleetHygieneSudoOK() {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=%s no-sudo (run it under sudo, or install the script by hand)\n", oneline.Field(host))
		return 2
	}
	if !fleetHygieneSystemdOK() {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED bench=%s no-systemd (install the timer by hand)\n", oneline.Field(host))
		return 2
	}
	root := fleetHygieneRoot(in)
	if strings.TrimSpace(root) == "" {
		fmt.Fprintln(in.Stderr, "FLEET REFUSED: refusing to guess (pass --root)")
		return 2
	}
	script := filepath.Join(root, "nova-bench", "bench-hygiene.sh")
	timer := filepath.Join(root, "bench-hygiene.timer")
	fleetHygieneWriteIfChanged(script, fleetHygieneScript, 0o755)
	fleetHygieneWriteIfChanged(timer, fleetHygieneTimer, 0o644)
	fmt.Fprintf(in.Stdout, "FLEET %s HYGIENE installed script=%s timer=%s\n", oneline.Field(host), oneline.Field(script), oneline.Field(timer))
	return 0
}

// FleetHygieneDryRun prints what --install would write and writes nothing.
func FleetHygieneDryRun(in FleetHygieneInput) int {
	root := fleetHygieneRoot(in)
	if strings.TrimSpace(root) == "" {
		fmt.Fprintln(in.Stderr, "FLEET REFUSED: refusing to guess (pass --root)")
		return 2
	}
	script := filepath.Join(root, "nova-bench", "bench-hygiene.sh")
	timer := filepath.Join(root, "bench-hygiene.timer")
	shown := 0
	for _, p := range []string{script, timer} {
		if raw, err := os.ReadFile(p); err == nil && (string(raw) == fleetHygieneScript || string(raw) == fleetHygieneTimer) {
			continue
		}
		if in.Max > 0 && shown >= in.Max {
			break
		}
		fmt.Fprintf(in.Stdout, "WOULD write %s\n", p)
		shown++
	}
	return 0
}

// FleetHygieneStatus reads each bench's last HYGIENE line and its free space
// now, one FLEET line per bench. A bench with no HYGIENE line is NO-HYGIENE,
// exit 2, never a green line for a bench nobody cleaned; an unreachable bench
// is exit 3. Studio is refused before any bench is contacted, and --status
// without --benches refuses to guess.
func FleetHygieneStatus(in FleetHygieneInput) int {
	if strings.TrimSpace(in.Benches) == "" {
		fmt.Fprintln(in.Stderr, "FLEET REFUSED: --status without --benches; refusing to guess (supply the benches file)")
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
			fmt.Fprintln(in.Stderr, "FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)")
			return 2
		}
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = fleetDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	runner := SSHRunner{Program: in.SSH}
	lines := make([]string, len(names))
	codes := make([]int, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			raw, err := runner.Run(ctx, b.SSH, fleetHygieneStatusScript)
			if err != nil && strings.TrimSpace(raw) == "" {
				lines[i] = "FLEET " + oneline.Field(b.Name) + " UNREACHABLE " + oneline.Escape(oneline.Err(err))
				codes[i] = 3
				return
			}
			hyg := fleetHygieneLastLine(raw)
			if hyg == "" {
				lines[i] = "FLEET " + oneline.Field(b.Name) + " NO-HYGIENE (run fleet hygiene --install)"
				codes[i] = 2
				return
			}
			lines[i] = fleetHygieneStatusLine(b.Name, hyg, fleetHygieneFreeGB(raw))
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

func fleetHygieneLastLine(raw string) string {
	last := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "HYGIENE ") {
			last = line
		}
	}
	return last
}

func fleetHygieneFreeGB(raw string) int {
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.Contains(f[0], "/") {
			continue
		}
		if kb, err := strconv.Atoi(f[3]); err == nil {
			return kb / (1024 * 1024)
		}
	}
	return 0
}

func fleetHygieneStatusLine(name, hyg string, freeGB int) string {
	slots, reaped, jobs, dropped, cache := "-", "-", "-", "-", "-"
	for _, f := range strings.Fields(hyg) {
		for _, key := range []string{"slots=", "reaped=", "jobs-deleted=", "slots-deleted=", "cache="} {
			if v, ok := strings.CutPrefix(f, key); ok {
				switch key {
				case "slots=":
					slots = v
				case "reaped=":
					reaped = v
				case "jobs-deleted=":
					jobs = v
				case "slots-deleted=":
					dropped = v
				case "cache=":
					cache = v
				}
			}
		}
	}
	return fmt.Sprintf("FLEET %s HYGIENE slots=%s reaped=%s jobs-deleted=%s slots-deleted=%s cache=%s free=%dG",
		oneline.Field(name), slots, reaped, jobs, dropped, cache, freeGB)
}
