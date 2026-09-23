package pulse

// Handoff and takeover are SPEC-PULSE's coordinator verbs: the duty shift ends
// by handoff and begins again by takeover, and the bench ownership between
// them is a lock (queue/OWNER) and a record (queue/HANDOFF), both files in
// the open. The tool makes no model call: it counts files, shells nova-wake
// awake and one nova-bus send, and prints one line per verdict.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// HandoffInput is the handoff verb apart from flag parsing, so a test drives
// it against a fake queue and fake nova-wake and nova-bus on PATH.
type HandoffInput struct {
	Queue  string // the queue directory holding OWNER, pending, launched and the logs
	To     string // the successor the shift goes to
	Bus    string // the nova-bus clone carrying the one note to the successor
	Roots  string // the benches, comma separated, for the in-flight-by-bench count
	As     string // the name the note is sent as
	Work   string // a nova-work checkout whose ownership record moves too; empty skips it
	Max    int
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// TakeoverInput is the takeover verb apart from flag parsing.
type TakeoverInput struct {
	Queue  string // the same queue the handoff left
	As     string // the name taking the shift
	Bus    string // kept for the verbs-block symmetry; the takeover sends no note
	Roots  string // kept for the verbs-block symmetry; the record carries the counts
	Max    int
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// shiftOwner is the coordinator's bench lock: name, host, pid, since, one
// tab-separated line in queue/OWNER.
type shiftOwner struct {
	name, host string
	pid        int
	since      string
}

func readShiftOwner(queue string) (shiftOwner, error) {
	raw, err := os.ReadFile(filepath.Join(queue, "OWNER"))
	if err != nil {
		return shiftOwner{}, fmt.Errorf("cannot read OWNER: %s", oneline.Err(err))
	}
	line := strings.TrimRight(string(raw), "\n")
	fields := strings.Split(strings.TrimSpace(line), "\t")
	if len(fields) != 4 || fields[0] == "" || fields[1] == "" || fields[3] == "" {
		return shiftOwner{}, fmt.Errorf("OWNER is not name, host, pid, since (rewrite it by takeover, then hand off)")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(fields[2]))
	if err != nil || pid <= 0 {
		return shiftOwner{}, fmt.Errorf("OWNER carries no live pid %q (rewrite it by takeover, then hand off)", fields[2])
	}
	return shiftOwner{name: fields[0], host: fields[1], pid: pid, since: fields[3]}, nil
}

// hostReachable places the OWNER host: this bench answers for itself, and a
// host nobody can place is unreachable -- an unreachable host's lock is stale,
// because liveness nobody can check is not liveness.
var hostReachable = func(host string) bool {
	me, err := os.Hostname()
	if err != nil {
		return false
	}
	return host == strings.TrimSpace(me)
}

// shiftChild runs one bounded child and returns its combined output. Nothing
// here runs a shell.
func shiftChild(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// friendAwake reads nova-wake awake's answer for one successor: a FRIEND line
// naming them awake. Any other answer -- asleep, unknown, absent -- is asleep.
func friendAwake(out, name string) bool {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(l))
		if len(f) >= 3 && f[0] == "FRIEND" && f[1] == name && f[2] == "awake" {
			return true
		}
	}
	return false
}

// shiftCards lists the card files in one queue directory, in name order.
func shiftCards(queue, dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(queue, dir, "card-*.md"))
	for i, p := range matches {
		matches[i] = strings.TrimSuffix(filepath.Base(p), ".md")
	}
	return matches
}

// cardBench finds a card's bench the way the manager does: the swarm root
// holding <root>/<slot>/jobs/<label>. It names the root's base, "" when no
// bench holds the card.
func cardBench(roots, label string) string {
	for _, r := range splitList(roots) {
		matches, _ := filepath.Glob(filepath.Join(r, "*", "jobs", label))
		if len(matches) > 0 {
			return filepath.Base(r)
		}
	}
	return ""
}

// lastWidthLine is the shift's last word on width: the last non-empty line of
// pulse.log, else MANAGER.log, else "-" -- a width never measured is unknown,
// never zero.
func lastWidthLine(queue string) string {
	for _, rel := range []string{"pulse.log", "MANAGER.log"} {
		raw, err := os.ReadFile(filepath.Join(queue, rel))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(lines[i]); t != "" {
				return t
			}
		}
	}
	return "-"
}

// Handoff ends the duty shift: SHIFT END on stdout, the loop stopped, OWNER
// released, a HANDOFF record written, and one bus note to the successor
// carrying the record. It refuses mid-harvest and when the successor is
// asleep, and then it changes nothing.
func Handoff(in HandoffInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	now := in.Now().UTC()
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if strings.TrimSpace(in.To) == "" {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("missing --to; refusing to guess (name the successor the shift goes to)"))
	}
	if strings.TrimSpace(in.Bus) == "" {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("missing --bus; refusing to guess (supply the nova-bus clone carrying the note)"))
	}
	if st, err := os.Stat(in.Queue); err != nil || !st.IsDir() {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("the queue directory %s is not a directory (make it, then hand off)", oneline.Field(in.Queue)))
	}
	// A harvest in progress is finished before the handoff proceeds: the
	// handoff refuses and the harvest's lock stays for the harvest to lift.
	if _, err := os.Stat(filepath.Join(in.Queue, "HARVEST.LOCK")); err == nil {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("a harvest is in progress (finish the harvest first, then hand off)"))
	}
	// The successor is awake by nova-wake awake, never by a guess.
	out, err := shiftChild(120*time.Second, "nova-wake", "awake", "--bus", in.Bus)
	if err != nil || !friendAwake(out, in.To) {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("successor %s is asleep by nova-wake awake (wake them before the handoff)", oneline.Field(in.To)))
	}
	own, err := readShiftOwner(in.Queue)
	if err != nil {
		return refusal(in.Stderr, "HANDOFF", err)
	}

	pending := shiftCards(in.Queue, "pending")
	launched := shiftCards(in.Queue, "launched")
	byBench := map[string]int{}
	for _, label := range launched {
		if b := cardBench(in.Roots, label); b != "" {
			byBench[b]++
		}
	}
	inflight := strconv.Itoa(len(launched))
	if len(byBench) > 0 {
		names := make([]string, 0, len(byBench))
		for b := range byBench {
			names = append(names, b)
		}
		sort.Strings(names)
		var detail []string
		for _, b := range names {
			detail = append(detail, b+"="+strconv.Itoa(byBench[b]))
		}
		inflight += ":" + strings.Join(detail, ",")
	}
	escalations := strconv.Itoa(len(readLines(filepath.Join(in.Queue, "ESCALATE"))))
	benches := strings.TrimSpace(in.Roots)
	if benches == "" {
		benches = "-"
	}
	// HANDOFF is to, from, width, in-flight, pending, escalations, benches
	// and state, one tab-separated line: the lock the shift held, as a record
	// the successor inherits.
	record := strings.Join([]string{
		in.To, own.name, lastWidthLine(in.Queue), inflight,
		strconv.Itoa(len(pending)), escalations, benches, "ended",
	}, "\t") + "\n"
	if err := os.WriteFile(filepath.Join(in.Queue, "HANDOFF"), []byte(record), 0o644); err != nil {
		return refusal(in.Stderr, "HANDOFF", fmt.Errorf("cannot write the HANDOFF record: %s (check the queue directory)", oneline.Err(err)))
	}
	if in.Work != "" {
		if err := moveWorkOwnership(in.Work, own.name, in.To, now); err != nil {
			_ = os.Remove(filepath.Join(in.Queue, "HANDOFF"))
			return refusal(in.Stderr, "HANDOFF", err)
		}
	}
	// The loop stops and the lock is released: OWNER is removed, never
	// rewritten, so a second handoff finds nothing to hand off.
	_ = os.WriteFile(filepath.Join(in.Queue, "LOOP"),
		[]byte(fmt.Sprintf("stopped handoff to=%s from=%s at=%s\n", oneline.Field(in.To), oneline.Field(own.name), now.Format(time.RFC3339))), 0o644)
	_ = os.Remove(filepath.Join(in.Queue, "OWNER"))

	shiftEnd := fmt.Sprintf("SHIFT END to=%s from=%s inflight=%d pending=%d escalations=%s",
		oneline.Field(in.To), oneline.Field(own.name), len(launched), len(pending), escalations)
	fmt.Fprintln(in.Stdout, shiftEnd)
	note := busNotifier{bus: in.Bus, as: nonEmpty(in.As, own.name)}
	if err := note.Note(fmt.Sprintf("HANDOFF to=%s from=%s", oneline.Field(in.To), oneline.Field(own.name)), record); err != nil {
		fmt.Fprintf(in.Stderr, "HANDOFF NOTE send failed to=%s: %s (the record is in %s; send it by hand)\n",
			oneline.Field(in.To), oneline.Err(err), oneline.Field(filepath.Join(in.Queue, "HANDOFF")))
	}
	fmt.Fprintf(in.Stdout, "HANDOFF OK to=%s inflight=%d pending=%d escalations=%s\n",
		oneline.Field(in.To), len(launched), len(pending), escalations)
	return 0
}

// moveWorkOwnership moves the coordinator ownership record in a nova-work
// tree: the generation bumps, a fresh token is drawn, and the :handoff event
// SPEC-WORK names is appended. The old owner fenced itself by handing off, so
// the successor takes the next generation at once.
func moveWorkOwnership(work, from, to string, now time.Time) error {
	st, err := os.Stat(work)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("the nova-work tree %s is not a directory (open it, then hand off)", oneline.Field(work))
	}
	generation := 0
	if raw, err := os.ReadFile(filepath.Join(work, "OWNER")); err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			for _, f := range strings.Fields(strings.TrimSpace(l)) {
				if v, ok := strings.CutPrefix(f, "generation="); ok {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						generation = n
					}
				}
			}
		}
	}
	generation++
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("cannot draw the ownership token: %s (hand off again)", oneline.Err(err))
	}
	owner := fmt.Sprintf("name=%s generation=%d token=%s fencing=%s\n",
		oneline.Field(to), generation, hex.EncodeToString(token[:]), now.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(work, "OWNER"), []byte(owner), 0o644); err != nil {
		return fmt.Errorf("cannot move the ownership record: %s (check the tree)", oneline.Err(err))
	}
	event := fmt.Sprintf("(:handoff :to %s :from %s :generation %d :at %s)\n",
		oneline.Field(to), oneline.Field(from), generation, now.Format(time.RFC3339))
	f, err := os.OpenFile(filepath.Join(work, "EVENTS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot append the :handoff event: %s (check the tree)", oneline.Err(err))
	}
	defer f.Close()
	if _, err := f.WriteString(event); err != nil {
		return fmt.Errorf("cannot append the :handoff event: %s (check the tree)", oneline.Err(err))
	}
	return nil
}

// Takeover begins the next shift on the same queue: it refuses when OWNER
// names a live process on a reachable host, takes a stale lock with one NOTE
// line, and inherits the HANDOFF record's counts.
func Takeover(in TakeoverInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	now := in.Now().UTC()
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "TAKEOVER", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if strings.TrimSpace(in.As) == "" {
		return refusal(in.Stderr, "TAKEOVER", fmt.Errorf("missing --as; refusing to guess (name the shift taken as)"))
	}
	if st, err := os.Stat(in.Queue); err != nil || !st.IsDir() {
		return refusal(in.Stderr, "TAKEOVER", fmt.Errorf("the queue directory %s is not a directory (make it, then take over)", oneline.Field(in.Queue)))
	}

	from, inherited := "-", "0/0/0"
	if raw, err := os.ReadFile(filepath.Join(in.Queue, "HANDOFF")); err == nil {
		fields := strings.Split(strings.TrimRight(string(raw), "\n"), "\t")
		if len(fields) == 8 {
			from = nonEmpty(fields[1], "-")
			inflight, pending, escalations := headInt(fields[3]), atoiOr(fields[4], 0), atoiOr(fields[5], 0)
			inherited = fmt.Sprintf("%d/%d/%d", inflight, pending, escalations)
		}
	}
	if _, err := os.ReadFile(filepath.Join(in.Queue, "OWNER")); err == nil {
		if own, oerr := readShiftOwner(in.Queue); oerr == nil {
			if hostReachable(own.host) && swarm.Alive(own.pid, "") {
				fmt.Fprintf(in.Stderr, "TAKEOVER REFUSED owner=%s pid=%d host=%s (wait, or clear the stale lock)\n",
					oneline.Field(own.name), own.pid, oneline.Field(own.host))
				return 2
			}
			reason := "dead process"
			if !hostReachable(own.host) {
				reason = "unreachable host"
			}
			fmt.Fprintf(in.Stdout, "TAKEOVER NOTE owner=%s pid=%d host=%s stale: %s (took the lock)\n",
				oneline.Field(own.name), own.pid, oneline.Field(own.host), reason)
			if from == "-" {
				from = own.name
			}
		} else {
			fmt.Fprintf(in.Stdout, "TAKEOVER NOTE owner=- pid=- host=- stale: unreadable lock (took the lock)\n")
		}
	}

	host, _ := os.Hostname()
	next := strings.Join([]string{
		in.As, strings.TrimSpace(host), strconv.Itoa(os.Getpid()), now.Format(time.RFC3339),
	}, "\t") + "\n"
	if err := os.WriteFile(filepath.Join(in.Queue, "OWNER"), []byte(next), 0o644); err != nil {
		return refusal(in.Stderr, "TAKEOVER", fmt.Errorf("cannot take the OWNER lock: %s (check the queue directory)", oneline.Err(err)))
	}
	// The loop and the shift start on the same queue the handoff left.
	_ = os.WriteFile(filepath.Join(in.Queue, "LOOP"),
		[]byte(fmt.Sprintf("running takeover as=%s at=%s\n", oneline.Field(in.As), now.Format(time.RFC3339))), 0o644)
	fmt.Fprintf(in.Stdout, "TAKEOVER OK from=%s inherited=%s\n", oneline.Field(from), inherited)
	return 0
}

// headInt reads the total off an in-flight field: "<total>" or
// "<total>:<bench>=<n>,...".
func headInt(field string) int {
	head, _, _ := strings.Cut(field, ":")
	return atoiOr(head, 0)
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}
