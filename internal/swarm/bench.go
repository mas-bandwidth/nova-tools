package swarm

// Benches: a remote bench reached by ssh, with pinned cores (SPEC-SWARM.md, "Benches").
//
// A bench is one row of the benches table, read from a tab-separated file: one header line
// then one row per bench. This file holds only the fields the remote-run slice reads; the
// remaining columns of the table are parsed and ignored here, because another card owns
// them.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Bench is one row of the benches table. Only the fields the remote-run slice reads are
// carried: name, host, root, cores and harness. auth and wall are parsed and ignored.
type Bench struct {
	Name    string // one word; the local machine is the row "local"
	Host    string // an ssh alias from the caller's ssh config
	Root    string // the swarm root on that host, absolute there
	Cores   string // a taskset list "1-15" or "2,4,6", or "-" for no pinning
	Harness string // the harness binary on that host, absolute there
}

// ReadBenchTable reads a benches file: one header line then one tab-separated row per
// bench, the seven columns name, host, root, cores, harness, auth, wall. A row that does
// not parse is refused with its line number, and a name used twice is refused too.
func ReadBenchTable(path string) (map[string]Bench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--benches wants a readable table of one bench per row: %w", err)
	}
	out := map[string]Bench{}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 7 {
			return nil, fmt.Errorf("--benches line %d wants name<TAB>host<TAB>root<TAB>cores<TAB>harness<TAB>auth<TAB>wall, got %d fields", i+1, len(parts))
		}
		if parts[0] == "name" {
			continue // the header row
		}
		name := strings.TrimSpace(parts[0])
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--benches names %s twice", name)
		}
		out[name] = Bench{
			Name:    name,
			Host:    parts[1],
			Root:    parts[2],
			Cores:   parts[3],
			Harness: parts[4],
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--benches names no bench row")
	}
	return out, nil
}

// coreCount reports how many cores a bench's cores column names: the length of a list, the
// breadth of a range, or -1 for "-" (no pinning, an unbounded slot count).
func coreCount(cores string) int {
	if cores == "-" {
		return -1
	}
	if strings.Contains(cores, "-") {
		parts := strings.SplitN(cores, "-", 2)
		lo, errLo := strconv.Atoi(parts[0])
		hi, errHi := strconv.Atoi(parts[1])
		if errLo != nil || errHi != nil || hi < lo {
			return 0
		}
		return hi - lo + 1
	}
	return len(strings.Split(cores, ","))
}

// coreFor resolves the core slot n (1-based) pins to, so slot 3 on "1-15" is core 3 and on
// "2,4,6" is core 6. It returns the core string or an error when n exceeds the cores.
func coreFor(cores string, n int) (string, error) {
	if strings.Contains(cores, "-") {
		parts := strings.SplitN(cores, "-", 2)
		lo, err := strconv.Atoi(parts[0])
		if err != nil {
			return "", fmt.Errorf("cores %q does not name a range", cores)
		}
		return strconv.Itoa(lo + n - 1), nil
	}
	parts := strings.Split(cores, ",")
	if n < 1 || n > len(parts) {
		return "", fmt.Errorf("slot %d exceeds cores %q", n, cores)
	}
	return parts[n-1], nil
}

// scratchName is the on-disk directory a card's files return under: <bench>-<n> for a remote
// bench and plain <n> for the local machine.
func scratchName(c batchCard) string {
	if c.bench != "" {
		return c.bench + "-" + strconv.Itoa(c.slot)
	}
	return strconv.Itoa(c.slot)
}

// remoteRun copies the card to the bench -- the card only, nothing else -- then builds the
// ssh command that runs native there: ssh <host> [taskset -c <core>] <root>/bin/nova-swarm
// native ..., with the ssh child in a process group of its own.
func remoteRun(c batchCard, b Bench, localRoot string, deadline int, logFile *os.File) (*exec.Cmd, error) {
	cardDest := filepath.Join(b.Root, "cards", c.label+".md")
	if err := copyCardToBench(c.cardPath, b, cardDest); err != nil {
		return nil, fmt.Errorf("nova-swarm batch: card %s could not be copied to bench %s: %s",
			oneline.Field(c.label), oneline.Field(b.Name), oneline.Err(err))
	}
	argv := []string{"ssh", b.Host}
	if b.Cores != "-" {
		core, err := coreFor(b.Cores, c.slot)
		if err != nil {
			return nil, fmt.Errorf("ADMIT REFUSED bench=%s slots=%d cores=%s", b.Name, c.slot, b.Cores)
		}
		argv = append(argv, "taskset", "-c", core)
	}
	argv = append(argv,
		filepath.Join(b.Root, "bin", "nova-swarm"), "native",
		"--harness", b.Harness,
		"--model", c.model,
		"--label", c.label,
		"--card", cardDest,
		"--slot", filepath.Join(b.Root, strconv.Itoa(c.slot), "jobs", c.label),
		"--root", b.Root,
		"--deadline", strconv.Itoa(deadline),
	)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+localRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	ownGroup(cmd)
	return cmd, nil
}

// copyCardToBench runs rsync to move the card to the bench's cards directory, the one file
// that crosses before the run.
func copyCardToBench(local string, b Bench, dest string) error {
	cmd := exec.Command("rsync", local, b.Host+":"+dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// pullRetryInterval is how long gather waits before its one retry of a failed pull: a
// network drop after RESULT.md was written gets one more chance, and nothing retries twice.
// It is a variable so a test can shrink the wait; the shipped value is 30 seconds.
var pullRetryInterval = 30 * time.Second

// remoteJobDir is a card's job directory on the bench's own root, the place ssh writes
// RESULT.md and usage.tsv before rsync pulls them back.
func remoteJobDir(b Bench, slot int, label string) string {
	return filepath.Join(b.Root, strconv.Itoa(slot), "jobs", label)
}

// pullRemote reads a remote card's result files back into the local job directory by rsync:
// RESULT.md (required, retried once), usage.tsv and the native.log tail (best-effort). A
// second RESULT.md failure is reported so the card scores no-result or bench-unreachable.
func pullRemote(b Bench, slot int, label, localDir string) error {
	src := b.Host + ":" + remoteJobDir(b, slot, label) + "/"
	if err := runPull([]string{"-a", src + "RESULT.md", localDir + "/RESULT.md"}); err != nil {
		return err
	}
	_ = runRsync([]string{"-a", src + "usage.tsv", localDir + "/usage.tsv"})
	_ = runRsync([]string{"-a", src + "native.log", localDir + "/native.log"})
	return nil
}

// runPull runs one rsync argv and, on failure, retries it once after pullRetryInterval: a
// network drop after RESULT.md was written is recovered, and nothing retries twice.
func runPull(args []string) error {
	if err := runRsync(args); err == nil {
		return nil
	}
	time.Sleep(pullRetryInterval)
	return runRsync(args)
}

// runRsync shells out to rsync and returns its error verbatim when it is not on PATH or
// exits non-zero.
func runRsync(args []string) error {
	return exec.Command("rsync", args...).Run()
}
