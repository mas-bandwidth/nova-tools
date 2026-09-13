package update

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

const ChildCap = 64 * 1024

// killGrace is the drain allowance a healthy child gets after it exits: its
// output copy may still be finishing, and under load a ten millisecond grace
// lost that race and reported a healthy tool as UNKNOWN. It is a cap, not a
// promise: the actual allowance is what is left of the budget, so a child whose
// pipe an escaped grandchild keeps open cannot extend the run past its deadline.
const killGrace = 2 * time.Second

// drainFloor is the smallest a drain may shrink to. It is reached only when the
// budget is already spent at the moment the child is gone, so a held pipe is
// still closed without ever growing into a second timeout.
const drainFloor = 50 * time.Millisecond

// leakRemedy names the one thing a person can do about a held pipe: the version
// command, not this tool, decides whether its children keep stdout open.
const leakRemedy = "make the version command wait for its own children, or send their output elsewhere"

var dotted = regexp.MustCompile(`[0-9]\.[0-9]`)
var digit = regexp.MustCompile(`[0-9]`)
var bareCommit = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
var release = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)
var digest = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

type Read struct{ Raw, Version, Path, Reason, Remedy, Source string }

func (r Read) Known() bool      { return r.Reason == "" }
func firstLine(s string) string { s, _, _ = strings.Cut(s, "\n"); return strings.TrimSuffix(s, "\r") }
func opaque(line string) bool {
	f := strings.Fields(line)
	return len(f) > 1 && (f[1] == "devel" || bareCommit.MatchString(f[1]))
}
func versionKey(line string) (string, error) {
	if opaque(line) {
		return "", fmt.Errorf("no_release_identity")
	}
	for _, t := range strings.Fields(line) {
		if dotted.MatchString(t) {
			return t[digit.FindStringIndex(t)[0]:], nil
		}
	}
	return "", fmt.Errorf("no_version")
}
func identity(e Entry, raw string, report bool) Read {
	r := Read{Raw: firstLine(raw), Remedy: "wrap it in a script that prints the version alone"}
	if e.Kind == "pin" {
		f := strings.Fields(r.Raw)
		if len(f) < 2 {
			r.Reason = "version line has fewer than two tokens"
		} else {
			r.Version = f[1]
		}
		return r
	}
	if e.Kind == "model" {
		for _, line := range strings.Split(raw, "\n") {
			f := strings.Fields(line)
			if len(f) > 1 && f[0] == e.Name && digest.MatchString(strings.ToLower(f[1])) {
				r.Raw = strings.TrimSuffix(line, "\r")
				r.Version = strings.ToLower(f[1])[:12]
				return r
			}
		}
		r.Reason = "model_not_found"
		r.Remedy = "owner: ollama pull " + e.Name + "; nova-local status --list"
		return r
	}
	if report && opaque(r.Raw) {
		return r
	}
	v, err := versionKey(r.Raw)
	if err != nil {
		r.Reason = err.Error()
		if r.Reason == "no_release_identity" {
			r.Remedy = "install a stamped build, or read it with report"
		}
	} else {
		r.Version = v
	}
	return r
}

type ProcessResult struct{ Stdout, Stderr, Path, Reason string }

func process(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
	r := ProcessResult{}
	if ctx.Err() != nil {
		r.Reason = "budget"
		return r
	}
	if len(args) == 0 {
		r.Reason = "empty argv"
		return r
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		r.Reason = "not_found"
		return r
	}
	r.Path = path
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	out, errs := bounded.NewCapture(cap, cancel), bounded.NewCapture(cap, cancel)
	cmd := exec.CommandContext(child, path, args[1:]...)
	cmd.Stdin = input
	// The pipes are created here rather than handed to os/exec as plain writers,
	// so this process can close the read ends itself when the deadline passes and
	// an escaped grandchild is still holding the write ends open.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		r.Reason = "execution failed: " + clip(err.Error(), 160)
		return r
	}
	defer stdoutRead.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutWrite.Close()
		r.Reason = "execution failed: " + clip(err.Error(), 160)
		return r
	}
	defer stderrRead.Close()
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	configureProcess(cmd)

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go func() { defer copyWG.Done(); _, _ = io.Copy(out, stdoutRead) }()
	go func() { defer copyWG.Done(); _, _ = io.Copy(errs, stderrRead) }()

	if err := cmd.Start(); err != nil {
		stdoutWrite.Close()
		stderrWrite.Close()
		stdoutRead.Close()
		stderrRead.Close()
		copyWG.Wait()
		r.Reason = "execution failed: " + clip(err.Error(), 160)
		return r
	}
	// The parent's write ends must close so a read sees EOF once the child and
	// its descendants have all closed theirs.
	stdoutWrite.Close()
	stderrWrite.Close()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	// Reap the child as soon as it exits or the deadline kills it, whichever
	// comes first, then drain its output within what the budget leaves.
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-child.Done():
		waitErr = <-waitCh
	}

	done := make(chan struct{})
	go func() { copyWG.Wait(); close(done) }()
	held := false
	if drain := drainAllowance(ctx); drain > 0 {
		t := time.NewTimer(drain)
		select {
		case <-done:
			t.Stop()
		case <-t.C:
			held = true
			stdoutRead.Close()
			stderrRead.Close()
			<-done
		}
	} else {
		<-done
	}

	r.Stdout = string(out.Bytes())
	r.Stderr = string(errs.Bytes())
	switch {
	case out.Hit() || errs.Hit():
		r.Reason = "output"
	case ctx.Err() != nil:
		r.Reason = "timeout"
	case waitErr != nil:
		if e, ok := waitErr.(*exec.ExitError); ok {
			r.Reason = fmt.Sprintf("exit %d", e.ExitCode())
		} else {
			r.Reason = "execution failed: " + clip(waitErr.Error(), 160)
		}
	case held:
		// The process itself is gone and its status was a clean exit, but the
		// output we did capture is not proof of a version because a grandchild
		// kept the pipe open past the drain. Name the leaked pipe rather than
		// blaming the version command, which ran.
		r.Reason = "output_not_closed"
	}
	return r
}

// drainAllowance is how long process lets a child's output copy finish after the
// child is gone or the deadline kills it. A healthy child that printed and
// exited gets the full grace; a child gone at the deadline gets only what the
// budget leaves, floored, so a held pipe is closed promptly rather than kept
// open by a fixed grace begun at cancellation.
func drainAllowance(ctx context.Context) time.Duration {
	drain := killGrace
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < drain {
			drain = remaining
		}
	}
	if drain < drainFloor {
		drain = drainFloor
	}
	return drain
}

// clip bounds a diagnostic clause. A reason a person cannot read is not a
// record, so the cut is marked rather than silent.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
func Installed(ctx context.Context, e Entry, timeout time.Duration, report bool) Read {
	if ctx.Err() != nil {
		return Read{Reason: "budget", Remedy: "increase --budget"}
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p := process(child, e.Installed, nil, ChildCap)
	raw := p.Stdout
	if raw == "" {
		raw = p.Stderr
	}
	r := identity(e, raw, report)
	r.Path = p.Path
	if p.Reason != "" {
		r.Reason = p.Reason
		r.Remedy = "wrap it in a script that prints the version alone"
		if p.Reason == "not_found" {
			r.Remedy = "install " + e.Installed[0] + " or supply its executable path; searched PATH=" + os.Getenv("PATH")
		}
		if p.Reason == "timeout" {
			r.Remedy = "increase --timeout or repair the version command"
		}
		if p.Reason == "output_not_closed" {
			r.Remedy = leakRemedy
		}
	}
	if ctx.Err() != nil {
		r.Reason = "budget"
		r.Remedy = "increase --budget"
	}
	return r
}

// Compare orders only equally long, plain numeric release tags. Arbitrary-size
// components avoid both floating-point loss and machine integer overflow.
func Compare(a, b string) string {
	if a == b {
		return "EQUAL"
	}
	if !release.MatchString(a) || !release.MatchString(b) {
		return "DIFFERENT"
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	if len(aa) != len(bb) {
		return "DIFFERENT"
	}
	for i := range aa {
		x, _ := new(big.Int).SetString(aa[i], 10)
		y, _ := new(big.Int).SetString(bb[i], 10)
		switch x.Cmp(y) {
		case -1:
			return "OLDER"
		case 1:
			return "NEWER"
		}
	}
	return "DIFFERENT"
}
