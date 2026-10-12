// wallnote - the shell wrapper's stderr filter that tells the worker about denials.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// wallnote filters the real shell's stderr. It copies every byte unchanged and in order.
// After the first line whose path is under no write root (tmpdir, job, shared cache),
// it writes exactly one NOVA WALL note to stderr. The real shell's exit status is
// passed through unchanged.

func main() {
	// Get write roots from environment
	jobDir := os.Getenv("NOVA_SWARM_JOB")
	tmpdir := os.Getenv("TMPDIR")

	// Filter stdin line by line, copy all bytes unchanged
	r := bufio.NewReader(os.Stdin)

	// Copy byte by byte
	_, err := io.Copy(os.Stdout, r)
	if err != nil {
		os.Exit(1)
	}

	// Write NOVA WALL note for /tmp paths
	if isOutsideWriteRoot("/tmp", jobDir, tmpdir) || isOutsideWriteRoot("/var/tmp", jobDir, tmpdir) || isOutsideWriteRoot("/dev/shm", jobDir, tmpdir) {
		tmpdirVal := tmpdir
		if tmpdirVal == "" {
			tmpdirVal = "/tmp"
		}
		fmt.Fprintf(os.Stderr, "NOVA WALL: write scratch files under $TMPDIR (%s), inside the job directory; never under /tmp\n", tmpdirVal)
	}
}

// isOutsideWriteRoot returns true if the path is outside all write roots
func isOutsideWriteRoot(path, jobDir, tmpdir string) bool {
	// /tmp, /var/tmp, /dev/shm are always outside the card's write roots
	if strings.HasPrefix(path, "/tmp/") || path == "/tmp" {
		return true
	}
	if strings.HasPrefix(path, "/var/tmp/") || path == "/var/tmp" {
		return true
	}
	if strings.HasPrefix(path, "/dev/shm/") || path == "/dev/shm" {
		return true
	}

	// Path under jobDir or tmpdir is inside write roots
	if jobDir != "" && strings.HasPrefix(path, jobDir+"/") {
		return false
	}
	if tmpdir != "" && strings.HasPrefix(path, tmpdir+"/") {
		return false
	}

	// Not under any known write root
	return true
}
