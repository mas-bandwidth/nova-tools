package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Bench is one row of the benches table: the columns pull-and-gather reads to run a remote
// slot and pull its result back. Nothing in the row is executed as data; the host is only
// ever an argument to ssh or rsync.
type Bench struct {
	Name string // the word a card's slot names, printed on the BENCH line
	Host string // the ssh alias ssh runs and rsync pulls from; "local" is the machine the batch runs on
	Root string // the swarm root on that host, absolute there
}

// pullRetryInterval is how long gather waits before its one retry of a failed pull: a
// network drop after RESULT.md was written gets one more chance, and nothing retries twice.
// It is a variable so a test can shrink the wait; the shipped value is 30 seconds.
var pullRetryInterval = 30 * time.Second

// benchCardDir is a card's local job directory: <root>/<bench>-<n>/jobs/<label> for a remote
// card, <root>/<n>/jobs/<label> for a local one, unchanged from before this section.
func benchCardDir(root, bench string, slot int, label string) string {
	name := strconv.Itoa(slot)
	if bench != "" {
		name = bench + "-" + name
	}
	return filepath.Join(root, name, "jobs", label)
}

// remoteJobDir is the same directory on the bench's own root, the place ssh writes
// RESULT.md and usage.tsv before rsync pulls them back.
func remoteJobDir(b Bench, slot int, label string) string {
	return filepath.Join(b.Root, strconv.Itoa(slot), "jobs", label)
}

// parseRemoteSlot splits an allocated slot's name into its bench and slot number. "b2:3" is
// slot 3 on bench b2; a bare "3" is local slot 3 with an empty bench.
func parseRemoteSlot(s string) (bench string, slot int, err error) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		n, e := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		if e != nil || n < 1 {
			return "", 0, fmt.Errorf("wants <bench>:<slot> with a positive slot, got %q", s)
		}
		return strings.TrimSpace(s[:i]), n, nil
	}
	n, e := strconv.Atoi(strings.TrimSpace(s))
	if e != nil || n < 1 {
		return "", 0, fmt.Errorf("wants a positive slot number, got %q", s)
	}
	return "", n, nil
}

// remoteCommand is the ssh argv that runs one card on a bench: the same runner the local row
// uses, pointed at the bench root, so the bench's card writes where rsync later pulls from.
func remoteCommand(b Bench, runner, label string, slot int, model, card string) *exec.Cmd {
	return exec.Command("ssh", b.Host, runner, label, strconv.Itoa(slot), model, card, b.Root)
}

// pullRemote reads a remote card's three files back into the local job directory by rsync:
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

// fileExists reports whether a path exists as a regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
