package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// THE LANE LOCK (captain, 2026-10-20). A machine whose jobs share one count with work outside
// the sprint -- captain's native lanes and the coordinator's WSL jobs share four slots of the
// Windows bench's winlock -- names the holder command with --lane-lock, a JSON argv. Every
// lane starts that command before native, waits for its first stdout line to begin with HELD,
// runs, and closes the command's stdin when it ends, which is the holder's release; a member
// that dies closes it too. "{card}" in the argv is the card's id, and a card whose timing is
// its gate -- its id names soak, stress, repeat or load, or its GATE-NEEDS names exclusive --
// gets --exclusive appended, so it runs alone on the machine.

// laneLockWait bounds the wait for HELD: past it the lane is not started, the holder is ended,
// and the card goes back (never wait forever).
const laneLockWait = 2 * time.Hour

// laneLockRelease bounds the holder's exit after its stdin closes.
const laneLockRelease = 30 * time.Second

var laneLockTimingRE = regexp.MustCompile(`(?i)(^|[-_.])(soak|stress|repeat|load)([-_.]|$)`)

// laneLockExclusive says whether a card runs alone: its id names a timing gate, or its brief's
// GATE-NEEDS line names exclusive.
func laneLockExclusive(card, brief string) bool {
	if laneLockTimingRE.MatchString(card) {
		return true
	}
	for _, line := range strings.Split(brief, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "GATE-NEEDS:"); ok {
			for _, w := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				if strings.EqualFold(w, "exclusive") {
					return true
				}
			}
		}
	}
	return false
}

// parseLaneLock reads --lane-lock: empty is none; otherwise a JSON array of strings, the
// program first.
func parseLaneLock(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(s), &argv); err != nil {
		return nil, fmt.Errorf("--lane-lock wants a JSON array of strings, the program first: %w", err)
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("--lane-lock names no program")
	}
	return argv, nil
}

// laneLockArgv is the holder's argv for one card.
func laneLockArgv(tmpl []string, p member.Packet) []string {
	out := make([]string, 0, len(tmpl)+1)
	for _, a := range tmpl {
		out = append(out, strings.ReplaceAll(a, "{card}", p.Card))
	}
	if laneLockExclusive(p.Card, p.Brief) {
		out = append(out, "--exclusive")
	}
	return out
}

// awaitHeld reads the holder's first line, bounded by wait: nil when it begins with HELD.
func awaitHeld(stdout io.Reader, wait time.Duration) (string, error) {
	got := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil && line == "" {
			line = "(no line: " + err.Error() + ")"
		}
		got <- strings.TrimSpace(line)
	}()
	select {
	case line := <-got:
		if strings.HasPrefix(line, "HELD") {
			return line, nil
		}
		return line, fmt.Errorf("the lane lock answered %q, not HELD", line)
	case <-time.After(wait):
		return "", fmt.Errorf("the lane lock was not held within %s", wait)
	}
}

// takeLaneLock starts the card's holder and waits for HELD. The release closes its stdin and
// waits for it, bounded; a member with no --lane-lock takes nothing and releases nothing.
func (r *nativeRunner) takeLaneLock(p member.Packet) (func(), error) {
	if len(r.laneLock) == 0 {
		return func() {}, nil
	}
	argv := laneLockArgv(r.laneLock, p)
	ctx, cancel := context.WithCancel(context.Background())
	c := subproc.Long(ctx, argv[0], argv[1:]...)
	stdin, err := c.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	c.Stderr = r.stderr
	if err := c.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("the lane lock %s could not start: %w", argv[0], err)
	}
	wait := r.laneLockWait
	if wait <= 0 {
		wait = laneLockWait
	}
	line, err := awaitHeld(stdout, wait)
	if err != nil {
		cancel()
		// ignored: the holder was ended above; its exit is not the refusal, err is
		_ = c.Wait()
		return nil, err
	}
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "nova-swarm member: LANE LOCK card=%s %s\n", oneline.Field(p.Card), oneline.Escape(line))
	}
	var once sync.Once
	return func() { once.Do(func() { releaseLaneLock(c, stdin, cancel) }) }, nil
}

// releaseLaneLock closes the holder's stdin -- the release -- and waits for it, bounded.
func releaseLaneLock(c interface{ Wait() error }, stdin io.Closer, cancel func()) {
	// ignored: closing the pipe IS the release; an error means the holder already ended
	_ = stdin.Close()
	done := make(chan struct{})
	go func() {
		// ignored: the holder's own status after its release is not the lane's
		_ = c.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(laneLockRelease):
		cancel()
		<-done
	}
	cancel()
}
