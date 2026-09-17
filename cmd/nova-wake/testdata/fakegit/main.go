// A fake git for the two bounds of the lane read that cannot be proved against
// a real one: the wall clock on a single git process, and the one-item buffer.
//
// It is a SHIM and not a replacement. Everything it is not asked to fake is
// handed to the real git named in NOVA_WAKE_REAL_GIT, so the lane under the
// test is a real repository and what is under test is this tool's own handling
// of a process that misbehaves.
//
//	NOVA_WAKE_FAKE_GIT_WEDGE=<arg>    wedge (sleep, forever) when this exact
//	                                  argument is present, after writing this
//	                                  process's pid to the pidfile
//	NOVA_WAKE_FAKE_GIT_PIDFILE=<path> where the wedged pid is written
//	NOVA_WAKE_FAKE_GIT_FAIL=<arg>     exit 1 when this exact argument is
//	                                  present, so a listing that fails can be
//	                                  told from a lane that is empty
//	NOVA_WAKE_FAKE_GIT_DIFF=<n>       answer `git diff` with n synthetic
//	                                  appended RECEIPTS lines instead of
//	                                  running it, and record in <pidfile>.bytes
//	                                  how many bytes it managed to WRITE before
//	                                  the reader went away
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	// The reader closes the pipe the moment its byte budget is spent; that is the
	// whole point of the fixture. Without ignoring SIGPIPE, writing to a closed
	// stdout would kill this process on the spot -- before its final byte count
	// is recorded -- and leave the count at whatever intermediate value the last
	// periodic record wrote. Ignoring it turns the broken pipe into an ordinary
	// EPIPE, which emit already treats as "the reader went away", so the final
	// count is written deterministically and the reader's Wait is a real sync
	// point for it (#370).
	signal.Ignore(syscall.SIGPIPE)
	args := os.Args[1:]
	has := func(want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	pidfile := os.Getenv("NOVA_WAKE_FAKE_GIT_PIDFILE")
	if wedge := os.Getenv("NOVA_WAKE_FAKE_GIT_WEDGE"); wedge != "" && has(wedge) {
		if pidfile != "" {
			_ = os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o644)
		}
		// Forever, as far as any bound this tool has is concerned. A process
		// that outlives the test binary is the failure the test is looking for,
		// so the sleep is long rather than endless.
		time.Sleep(10 * time.Minute)
		return
	}
	if fail := os.Getenv("NOVA_WAKE_FAKE_GIT_FAIL"); fail != "" && has(fail) {
		fmt.Fprintf(os.Stderr, "fatal: the fake git was told to fail %s\n", fail)
		os.Exit(1)
	}
	if n := os.Getenv("NOVA_WAKE_FAKE_GIT_DIFF"); n != "" && has("diff") {
		lines, _ := strconv.Atoi(n)
		written := 0
		out := bufio.NewWriterSize(os.Stdout, 4096)
		emit := func(s string) bool {
			k, err := out.WriteString(s)
			written += k
			if err != nil || out.Flush() != nil {
				return false
			}
			return true
		}
		record := func() {
			if pidfile == "" {
				return
			}
			// The reader reads .bytes the moment the read returns, which can be
			// while this process is still streaming and rewriting the count. A
			// plain os.WriteFile truncates before it writes, so a concurrent
			// reader can see a zero-length file; write to a temp file and rename
			// so the file always holds a complete previous or new count.
			tmp := pidfile + ".bytes.tmp"
			if err := os.WriteFile(tmp, []byte(strconv.Itoa(written)), 0o644); err == nil {
				_ = os.Rename(tmp, pidfile+".bytes")
			}
		}
		// The count is written AS IT GOES, because the reader closing the pipe
		// is the whole point of the test: a total written only at the end is a
		// total nobody ever sees.
		record()
		ok := emit("diff --git a/from-peer/RECEIPTS b/from-peer/RECEIPTS\n") &&
			emit("--- a/from-peer/RECEIPTS\n") && emit("+++ b/from-peer/RECEIPTS\n") &&
			emit("@@ -0,0 +1,"+n+" @@\n")
		for i := 0; ok && i < lines; i++ {
			ok = emit(fmt.Sprintf("+2026-09-13T10:00:00Z peer-%012d %s\n", i, strings.Repeat("x", 900)))
			if i%8 == 0 {
				record()
			}
		}
		record()
		return
	}
	real := os.Getenv("NOVA_WAKE_REAL_GIT")
	if real == "" {
		real = "/usr/bin/git"
	}
	cmd := exec.Command(real, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
