package pulse

// The Handoff and Takeover verbs (SPEC-PULSE.md, "Handoff", replay set
// 26-31): the manager's workday ends by handoff and begins again by takeover,
// OWNER is the lock, HANDOFF is the record, and one bus note to the successor
// carries the record.

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// HandoffInput is the verb's input, held apart from flag parsing so a test can
// drive handoff against a fake queue and a fake nova-wake, nova-bus on PATH.
type HandoffInput struct {
	Queue  string // <root>/queue directory: pending, launched, done, OWNER, HANDOFF
	Bus    string // the nova-bus clone handoff posts the record to
	To     string // the successor the bus note is addressed to
	From   string // the current owner writing the record
	Width  string // the last PULSE WIDTH line, "PULSE WIDTH <bench> in-flight=... hours=..."
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time

	// HostLiveness is the host reachability hook: production says yes iff the
	// OWNER's host field equals os.Hostname(). The spec test substitutes a stub.
	HostLiveness func(host string) bool
}

// TakeoverInput is the verb's input, held apart from flag parsing so a test
// can drive takeover against a fake queue and a fake nova-bus on PATH.
type TakeoverInput struct {
	Queue  string // <root>/queue directory
	Bus    string // the nova-bus clone (read on inheritance; never written)
	As     string // the name taking over
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time

	// HostLiveness is the same hook as HandoffInput: production uses os.Hostname().
	HostLiveness func(host string) bool

	// PidLiveness is the liveness hook: production uses swarm.Alive. The test
	// substitutes a stub so the lock can be staged as live or stale at will.
	PidLiveness func(pid int) bool
}

// Handoff ends the manager's shift and writes a HANDOFF record the successor
// will inherit. The verb refuses when a harvest is in progress (it must run
// to its own line first) and when the successor is asleep by `nova-wake awake`.
// On success it prints the SHIFT END line, posts one bus note to the
// successor carrying the record, and prints HANDOFF OK.
//
// Exit codes: 0 the shift ended and the record is on disk; 2 on a refusal.
func Handoff(in HandoffInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "HANDOFF", stderrors.New("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if strings.TrimSpace(in.Bus) == "" {
		return refusal(in.Stderr, "HANDOFF", stderrors.New("missing --bus; refusing to guess (supply the nova-bus clone)"))
	}
	if strings.TrimSpace(in.To) == "" {
		return refusal(in.Stderr, "HANDOFF", stderrors.New("missing --to; refusing to guess (supply the successor's name)"))
	}
	in.HostLiveness = hostLiveness(in.HostLiveness)

	// Mid-harvest is the one state where handoff must not steal the lock: a
	// manager cycle running a harvest owns the in-flight slots and finishing
	// the harvest is the same line as freeing this verb to write HANDOFF.
	// The flag is the queue's HARVEST file the harvest verb takes while it
	// runs; for tests we set it directly to demonstrate the refusal.
	if _, err := os.Stat(filepath.Join(in.Queue, "HARVEST")); err == nil {
		return refusal(in.Stderr, "HANDOFF", stderrors.New("mid-harvest in progress; finish the harvest first, then handoff (run: rm <queue>/HARVEST to clear a stale flag if no harvest is running)"))
	}

	// The successor must be awake. nova-wake awake answers with one FRIEND
	// line per friend and one AWAKE OK verdict; an exit code besides 0 OR
	// a FRIEND <name> asleep line means the handoff cannot succeed now.
	awkStdout, awkStderr, awkErr := runChild("", 30*time.Second, "nova-wake", "awake", "--bus", in.Bus)
	if awkErr != nil {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("nova-wake awake refused (the bus clone is %s): %s (check the bus, wake the successor with nova-wake serve, then handoff again)",
			oneline.Field(in.Bus), oneline.Cap(awkStderr+" "+awkStdout, 120)))
	}
	for _, l := range strings.Split(awkStdout, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "FRIEND "+in.To+" ") {
			continue
		}
		if !strings.Contains(t, " awake") {
			return refusal(in.Stderr, "HANDOFF", fmt.Errorf("successor %s is asleep by nova-wake awake (the caller handoffs only on a present recipient: wake them with nova-wake serve, then handoff again)", oneline.Field(in.To)))
		}
	}

	inflight, pending, escalations, benches := handoffState(in.Queue)

	stamp := in.Now().UTC().Format("20060102T150405Z")
	record := strings.Join([]string{
		"to=" + oneline.Field(in.To),
		"from=" + oneline.Field(in.From),
		"width=" + oneline.Field(in.Width),
		"in-flight=" + strconv.Itoa(inflight),
		"pending=" + strconv.Itoa(pending),
		"escalations=" + strconv.Itoa(escalations),
		"benches=" + benches,
		"state=ended",
	}, "\t")

	if err := os.WriteFile(filepath.Join(in.Queue, "HANDOFF"), []byte(record+"\n"), 0o644); err != nil {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("cannot write %s: %s (the record must land on disk before the bus note goes out)", oneline.Field(filepath.Join(in.Queue, "HANDOFF")), oneline.Err(err)))
	}

	// OWNER is released by removing it: takeover's check reads the file to
	// decide whose exemption is in force, and an absent file is a free lock.
	_ = os.Remove(filepath.Join(in.Queue, "OWNER"))

	// One bus note to the successor, carrying the record verbatim. The
	// subject names the stamp so any follow-up is searchable.
	draft := strings.Join([]string{
		"From: " + in.From,
		"To: " + in.To,
		"Subject: handoff to " + in.To + " " + stamp,
		"Kind: handoff",
		"",
		record,
	}, "\n")
	draftPath := filepath.Join(filepath.Dir(in.Queue), "handoff-draft-"+stamp+".md")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "HANDOFF NOTE cannot stage draft %s: %s\n", oneline.Field(draftPath), oneline.Err(err))
	} else {
		if out, _, err := runChild("", 60*time.Second, "nova-bus", "send", "--bus", in.Bus, "--as", in.From, "--file", draftPath, "--remote", "origin", "--branch", "main"); err != nil {
			fmt.Fprintf(in.Stderr, "HANDOFF NOTE bus send refused: %s (the HANDOFF record is on disk: %s; the bus note can be resent by hand)\n", oneline.Cap(err.Error(), 100), oneline.Cap(out, 100))
		}
		_ = os.Remove(draftPath)
	}

	fmt.Fprintf(in.Stdout, "SHIFT END cycles=0 decisions=0 escalations=0\n")
	fmt.Fprintf(in.Stdout, "HANDOFF OK to=%s inflight=%d pending=%d escalations=%d\n",
		oneline.Field(in.To), inflight, pending, escalations)
	return 0
}

// handoffState reads the three counts the record carries: in-flight (cards in
// launched), pending (cards in pending), escalations (open ESCALATE rows).
// Benches is the count of root directories the queue was running against.
func handoffState(queue string) (inflight, pending, escalations int, benches string) {
	for _, dir := range []string{"launched", "pending"} {
		matches, _ := filepath.Glob(filepath.Join(queue, dir, "card-*.md"))
		switch dir {
		case "launched":
			inflight = len(matches)
		case "pending":
			pending = len(matches)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(queue, "ESCALATE")); err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(l) != "" {
				escalations++
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(queue, "ROOTS")); err == nil {
		benches = strings.TrimSpace(string(raw))
	}
	if benches == "" {
		benches = "-"
	}
	return inflight, pending, escalations, benches
}

// Takeover begins a new shift on the same queue. The verb refuses when OWNER
// names a live process on a reachable host (`TAKEOVER REFUSED owner=<name>
// pid=<n> host=<h>`). When the lock is stale, takeover writes the new
// OWNER, prints one NOTE line saying so, and prints TAKEOVER OK with the
// HANDOFF record's inflight / pending / escalations inherited.
//
// Exit codes: 0 the lock was taken and the loop is signalled; 2 on a refusal.
func Takeover(in TakeoverInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "TAKEOVER", stderrors.New("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if strings.TrimSpace(in.As) == "" {
		return refusal(in.Stderr, "TAKEOVER", stderrors.New("missing --as; refusing to guess (supply the new owner's name)"))
	}
	in.HostLiveness = hostLiveness(in.HostLiveness)
	pidLive := in.PidLiveness
	if pidLive == nil {
		pidLive = func(pid int) bool { return swarm.Alive(pid, "-") }
	}

	oldOwner, oldHost, oldPID, hadLock := readOWNER(in.Queue)
	previous := "-"
	if hadLock {
		pid, _ := strconv.Atoi(oldPID)
		if in.HostLiveness(oldHost) && pidLive(pid) {
			return takeoverRefused(in.Stderr, oldOwner, oldHost, oldPID)
		}
		previous = oldOwner
		fmt.Fprintf(in.Stdout, "TAKEOVER NOTE stale owner=%s host=%s pid=%s cleared (a new owner %s takes the lock)\n",
			oneline.Field(oldOwner), oneline.Field(oldHost), oneline.Field(oldPID), oneline.Field(in.As))
	}

	if err := writeOWNER(in.Queue, OWNERRow{
		Name:  in.As,
		Host:  hostOf(),
		PID:   strconv.Itoa(os.Getpid()),
		Since: in.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return refusal(in.Stderr, "TAKEOVER", fmt.Errorf("cannot write OWNER: %s", oneline.Err(err)))
	}

	inflight, pending, escalations := readInheritedHANDOFF(in.Queue)
	fmt.Fprintf(in.Stdout, "TAKEOVER OK from=%s inherited=%d/%d/%d\n",
		oneline.Field(previous), inflight, pending, escalations)
	return 0
}

// hostLiveness is the production predicate, and the test stub defaults to
// accepting only the local machine's name so the live-owner scenario is
// reproducible without DNS gymnastics.
func hostLiveness(stub func(string) bool) func(string) bool {
	if stub != nil {
		return stub
	}
	me, _ := os.Hostname()
	return func(host string) bool { return host != "" && host == me }
}

// readOWNER returns the OWNER file's four fields. hadLock false means no file.
func readOWNER(queue string) (name, host, pid string, hadLock bool) {
	raw, err := os.ReadFile(filepath.Join(queue, "OWNER"))
	if err != nil {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\n"), "\t")
	if len(parts) < 4 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func hostOf() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "-"
}

// OWNERRow is the four fields OWNER holds; the spec names them as a tabs line.
type OWNERRow struct {
	Name, Host, PID, Since string
}

func writeOWNER(queue string, r OWNERRow) error {
	line := strings.Join([]string{
		oneline.Field(r.Name),
		oneline.Field(r.Host),
		oneline.Field(r.PID),
		oneline.Field(r.Since),
	}, "\t") + "\n"
	return os.WriteFile(filepath.Join(queue, "OWNER"), []byte(line), 0o644)
}

// readInheritedHANDOFF pulls the inflight/pending/escalations triplet the
// previous handoff wrote, so the new owner prints them on TAKEOVER OK.
// A missing or unreadable HANDOFF file is zeroes, never a guess.
func readInheritedHANDOFF(queue string) (inflight, pending, escalations int) {
	raw, err := os.ReadFile(filepath.Join(queue, "HANDOFF"))
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Split(strings.TrimRight(string(raw), "\n"), "\t")
	for _, f := range fields {
		switch {
		case strings.HasPrefix(f, "in-flight="):
			inflight = atoiOrZero(strings.TrimPrefix(f, "in-flight="))
		case strings.HasPrefix(f, "pending="):
			pending = atoiOrZero(strings.TrimPrefix(f, "pending="))
		case strings.HasPrefix(f, "escalations="):
			escalations = atoiOrZero(strings.TrimPrefix(f, "escalations="))
		}
	}
	return inflight, pending, escalations
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// takeoverRefused is one line to stderr: who is in the way of the new owner.
func takeoverRefused(w io.Writer, name, host, pid string) int {
	fmt.Fprintf(w, "TAKEOVER REFUSED owner=%s pid=%s host=%s (wait, or clear the stale lock before taking over)\n",
		oneline.Field(name), oneline.Field(pid), oneline.Field(host))
	return 2
}

// runChild spawns a child and returns its two streams and exit error. We keep
// it in-process so every verb uses the same child pattern: no shell, bounded
// timeout, no inherited PATH tricks the test cannot see.
func runChild(dir string, timeout time.Duration, name string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()
	return out.String(), errb.String(), runErr
}

// ensure imports are not pruned when this file is edited.
var _ = stderrors.New
