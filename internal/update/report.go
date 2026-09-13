package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func shaText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func report(ctx context.Context, all, entries []Entry, o options, kinds string, started time.Time, out, errs io.Writer, env Environment) int {
	state := emptySnapshot()
	var err error
	if o.snapshot != "" {
		release, lockErr := lockSnapshot(ctx, o.snapshot)
		if lockErr != nil {
			return refusal(errs, "REPORT", lockErr)
		}
		defer release()
		state, err = readSnapshot(o.snapshot)
		if err != nil {
			return refusal(errs, "REPORT", err)
		}
	}
	rs := readEntries(ctx, entries, o, env, true)
	seen := map[string]observed{}
	known := 0
	at := started.UTC().Format(time.RFC3339)
	var inventory bytes.Buffer
	fmt.Fprintf(&inventory, "REPORT at=%s file=%s host=%s as=%s entries=%d kinds=%s timeout=%s budget=%s max=%d snapshot=%s\n", field(at), field(o.file), field(o.host), field(o.as), len(all), field(kinds), o.timeout, o.budget, o.max, field(o.snapshot))
	group := bounded.Grouped(&inventory, o.max, "REPORT", "use --max 0 to show all")
	for _, x := range rs {
		r := x.Installed
		status := "unknown"
		if r.Known() {
			status = "known"
			known++
			group.Line("tool", fmt.Sprintf("REPORT TOOL name=%s kind=%s version=%s raw=%s path=%s", field(x.Entry.Name), field(x.Entry.Kind), field(r.Version), field(r.Raw), field(r.Path)))
		} else {
			group.Line("unknown", fmt.Sprintf("REPORT UNKNOWN name=%s kind=%s path=%s raw=%s: %s (%s)", field(x.Entry.Name), field(x.Entry.Kind), field(r.Path), field(r.Raw), oneline.Escape(r.Reason), oneline.Escape(r.Remedy)))
		}
		seen[x.Entry.Name] = observed{r.Raw, status, at}
	}
	changed := "-"
	if o.snapshot != "" {
		changed = "no"
		if !sameObserved(state.Observed, seen) {
			changed = "yes"
		}
		keys := map[string]bool{}
		for k := range state.Observed {
			keys[k] = true
		}
		for k := range seen {
			keys[k] = true
		}
		order := []string{}
		for k := range keys {
			order = append(order, k)
		}
		sort.Strings(order)
		for _, k := range order {
			a, b := state.Observed[k], seen[k]
			if a.Raw != b.Raw || a.Status != b.Status {
				group.Line("changed", fmt.Sprintf("REPORT CHANGED name=%s was=%s now=%s", field(k), field(a.Raw), field(b.Raw)))
			}
		}
		state.Observed = seen
		if err = writeSnapshot(o.snapshot, state); err != nil {
			return refusal(errs, "REPORT", err)
		}
	}
	group.More()
	sent := "-"
	code := 0
	if known != len(entries) {
		code = 1
	}
	count := func(w io.Writer, delivery string, code int) {
		result := "OK"
		if code != 0 {
			result = "FAIL"
		}
		fmt.Fprintf(w, "REPORT %s checked=%d known=%d unknown=%d changed=%s sent=%s took=%s file=%s\n", result, len(entries), known, len(entries)-known, changed, delivery, time.Since(started).Round(time.Millisecond), field(o.file))
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\nTo: %s\nSubject: versions on %s at %s\n\n", o.as, o.to, dash(o.host), at)
	body.Write(inventory.Bytes())
	count(&body, "-", code)
	if o.draft {
		if _, err = out.Write(body.Bytes()); err != nil {
			return 1
		}
		return code
	}
	if _, err = out.Write(inventory.Bytes()); err != nil {
		return 1
	}
	if o.send {
		sent, err = deliver(ctx, o, state, seen, body.Bytes(), out, env)
		if err != nil {
			fmt.Fprintf(errs, "REPORT NOTE %s\n", oneline.Err(err))
			code = 1
		}
	}
	w := out
	if code != 0 {
		w = errs
	}
	count(w, sent, code)
	return code
}
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// busRefusals are the openings of nova-bus's own refusal grammar. A line is
// relayed only if it begins with one of them.
var busRefusals = []string{"SEND FAIL ", "SEND REFUSED: ", "SEND OK ", "PREPARE FAIL ", "PREPARE REFUSED: "}

// busSaid carries the bus's OWN first line into the caller's diagnostic. Without
// it an operator reading a failed delivery is told "exit 1" and nothing else,
// and the one thing that would tell them what to repair -- a stale index lock, a
// roster that no longer resolves the speaker, a same-id note with other bytes --
// stays in a pipe nobody reads.
//
// What is relayed is bounded twice over. It must be ONE line that opens with the
// bus's own refusal grammar, so an arbitrary binary that happens to be called
// nova-bus on somebody's PATH cannot use this tool's event line to say anything
// it likes; and it is clipped. A line that is not in that grammar is named as
// unrelayed rather than repeated, and the caller still has its own reason for
// the failure beside it -- that reason (not_found, timeout, exit <n>,
// output_not_closed, the operating system's own words) is about this tool's argv
// and is never the bus's text.
//
// What the bus's own reasons may contain, read at #138 aeb45c9: a note header
// VALUE (a From or a Subject, truncated), a roster name, an id, a lane, a path,
// one remote INDEX line. They do not contain a note body and they cannot contain
// a version command's output, which reaches the bus only inside the note.
func busSaid(r ProcessResult) string {
	said := firstLine(strings.TrimSpace(r.Stderr))
	if said == "" {
		said = firstLine(strings.TrimSpace(r.Stdout))
	}
	if said == "" {
		return "nothing"
	}
	for _, opening := range busRefusals {
		if strings.HasPrefix(said, opening) {
			return clip(said, 200)
		}
	}
	return "a line outside the bus's refusal grammar, not relayed"
}

// busBounds are the finite retry controls the reporter hands the bus, computed
// from what is LEFT of the reporter's own budget.
//
// Rule 23's --timeout bounds one version probe; it was also bounding the bus
// child, which made a default run kill its own delivery at five seconds. The
// delivery allowance is the remaining budget instead (rule 25 and
// SPEC-BUS-DELIVERY both name the budget as the resolution horizon), and these
// two flags are what let the bus stop on its own inside that horizon rather than
// be killed at the end of it: one git operation gets a third of what remains,
// capped at a minute and never more than remains, and the attempt count is how
// many such operations fit, never more than the bus's own 25.
func busBounds(remaining time.Duration) (attempts, gitSeconds int) {
	git := remaining / 3
	if git > time.Minute {
		git = time.Minute
	}
	if git > remaining {
		git = remaining
	}
	gitSeconds = int(git / time.Second)
	if gitSeconds < 1 {
		// The bus counts this flag in whole seconds, so below a second there is
		// no number to name but one. The caller's own deadline is then the
		// tighter of the two bounds, which is the safe way round.
		gitSeconds = 1
	}
	attempts = int(remaining / (time.Duration(gitSeconds) * time.Second))
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 25 {
		attempts = 25
	}
	return attempts, gitSeconds
}

// deliveryAllowance is what is left of the budget. A delivery that cannot start
// inside it is a pending gate, not a kill.
func deliveryAllowance(ctx context.Context, now time.Time) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return deadline.Sub(now)
}
func deliver(ctx context.Context, o options, s *snapshot, seen map[string]observed, body []byte, out io.Writer, env Environment) (string, error) {
	scope := snapshotScope(o)
	save := func() error {
		if o.snapshot != "" {
			return writeSnapshot(o.snapshot, s)
		}
		return nil
	}
	send := func(p pending) (string, error) {
		allowance := deliveryAllowance(ctx, env.Now())
		if allowance <= 0 {
			return "uncertain", fmt.Errorf("pending %s not sent: the budget is spent (retry this --send with the same --snapshot; do not prepare again)", p.ID)
		}
		attempts, gitSeconds := busBounds(allowance)
		args := []string{"nova-bus", "send", "--prepared-stdin", "--bus", o.bus, "--remote", o.remote, "--branch", o.branch, "--as", o.as,
			"--attempts", strconv.Itoa(attempts), "--git-timeout", strconv.Itoa(gitSeconds)}
		child, cancel := context.WithTimeout(ctx, allowance)
		r := captureRun(child, args, p.Artifact, ChildCap)
		cancel()
		line := ""
		for _, l := range strings.Split(r.Stdout, "\n") {
			if confirmed(l, p.ID) {
				line = l
				break
			}
		}
		if r.Reason != "" || line == "" {
			return "uncertain", fmt.Errorf("pending %s not confirmed: %s; the bus said: %s (retry this --send with the same --snapshot; do not prepare again)", p.ID, dash(r.Reason), busSaid(r))
		}
		s.Delivered[scope] = delivery{cloneObserved(p.Observed), p.ID, env.Now().UTC().Format(time.RFC3339)}
		delete(s.Pending, scope)
		if err := save(); err != nil {
			return "uncertain", err
		}
		fmt.Fprintf(out, "REPORT SENT to=%s via=%s line=%s\n", field(o.to), field(strings.Join(args, " ")), field(line))
		return "yes", nil
	}
	sent := "no"
	if p, ok := s.Pending[scope]; ok {
		id, err := validatePrepared(p.Artifact)
		if err != nil || id != p.ID {
			return "uncertain", fmt.Errorf("invalid pending artifact (preserve the snapshot and repair it before retry)")
		}
		var err2 error
		sent, err2 = send(p)
		if err2 != nil {
			return sent, err2
		}
	}
	if d, ok := s.Delivered[scope]; ok && sameObserved(d.Observed, seen) {
		if sent == "no" {
			fmt.Fprintf(out, "REPORT NOTE unchanged since %s to %s; nothing sent\n", field(d.ID), field(o.to))
		}
		return sent, nil
	}
	if ctx.Err() != nil {
		return sent, fmt.Errorf("delivery budget exhausted (retry --send with the same --snapshot)")
	}
	// prepare runs no Git and touches no network, so it takes no attempt or
	// git-timeout flag; what it must not take is the version probe's timeout,
	// which is a bound on reading a tool's version and not on a delivery.
	allowance := deliveryAllowance(ctx, env.Now())
	if allowance <= 0 {
		return sent, fmt.Errorf("delivery budget exhausted (retry --send with the same --snapshot)")
	}
	child, cancel := context.WithTimeout(ctx, allowance)
	prepared := captureRun(child, []string{"nova-bus", "prepare", "--bus", o.bus, "--as", o.as, "--stdin"}, body, ChildCap)
	cancel()
	if prepared.Reason != "" {
		return sent, fmt.Errorf("prepare refused: %s; the bus said: %s (check nova-bus and the named bus; retry --send)", prepared.Reason, busSaid(prepared))
	}
	id, err := validatePrepared([]byte(prepared.Stdout))
	if err != nil {
		return sent, fmt.Errorf("%s (use a compatible nova-bus)", err)
	}
	p := pending{json.RawMessage(prepared.Stdout), cloneObserved(seen), id}
	s.Pending[scope] = p
	if err = save(); err != nil {
		return sent, err
	}
	return send(p)
}
