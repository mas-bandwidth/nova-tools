package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func shaText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// report is the report verb's one value: the run's facts on its first line, an
// item per tool read (TOOL, or UNKNOWN with its reason), per tool changed since
// --snapshot (CHANGED) and per delivery (SENT), and a note for what is true and
// not a finding. Under --draft the value is the note itself, its payload: the
// headers and this same value's lines, which is also what --send delivers.
func report(ctx context.Context, verb string, all, entries []Entry, o options, kinds, help string, started time.Time, env Environment) *tool.Out {
	state := emptySnapshot()
	var err error
	if o.snapshot != "" {
		release, lockErr := lockSnapshot(ctx, o.snapshot)
		if lockErr != nil {
			return refused(verb, help, lockErr.Error())
		}
		defer release()
		state, err = readSnapshot(o.snapshot)
		if err != nil {
			return refused(verb, help, err.Error())
		}
	}
	rs := readEntries(ctx, entries, o, env, true)
	res := &tool.Out{Verb: verb, Status: tool.OK}
	seen := map[string]observed{}
	known := 0
	at := started.UTC().Format(time.RFC3339)
	for _, x := range rs {
		r := x.Installed
		status := "unknown"
		if r.Known() {
			status = "known"
			known++
			res.Item("tool", "name", x.Entry.Name, "kind", x.Entry.Kind, "version", r.Version, "raw", r.Raw, "path", r.Path)
		} else {
			res.Item("unknown", "name", x.Entry.Name, "kind", x.Entry.Kind, "path", r.Path, "raw", r.Raw, "reason", r.Reason, "remedy", r.Remedy)
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
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			a, b := state.Observed[k], seen[k]
			if a.Raw != b.Raw || a.Status != b.Status {
				res.Item("changed", "name", k, "was", a.Raw, "now", b.Raw)
			}
		}
		state.Observed = seen
		if err = writeSnapshot(o.snapshot, state); err != nil {
			return refused(verb, help, err.Error())
		}
	}
	if known != len(entries) {
		res.Status, res.Exit = tool.Failed, 1
	}
	facts := func(sent string) tool.Fields {
		return tool.Fields{{K: "checked", V: len(entries)}, {K: "known", V: known}, {K: "unknown", V: len(entries) - known},
			{K: "changed", V: changed}, {K: "sent", V: sent}, {K: "took", V: env.Now().Sub(started).Round(time.Millisecond).String()},
			{K: "file", V: o.file}, {K: "host", V: o.host}, {K: "as", V: o.as}, {K: "entries", V: len(all)}, {K: "kinds", V: kinds},
			{K: "at", V: at}, {K: "timeout", V: o.timeout.String()}, {K: "budget", V: o.budget.String()}, {K: "max", V: o.max},
			{K: "snapshot", V: o.snapshot}}
	}
	res.Facts = facts("-")
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\nTo: %s\nSubject: versions on %s at %s\n\n", o.as, o.to, dash(o.host), at)
	text(&body, capped(res, o.max))
	if o.draft {
		return &tool.Out{Verb: verb, Status: res.Status, Exit: res.Exit, Payload: body.String()}
	}
	if o.send {
		sent, err := deliver(ctx, o, state, seen, body.Bytes(), res, env)
		if err != nil {
			res.Note(err.Error())
			res.Status, res.Exit = tool.Failed, 1
		}
		res.Facts = facts(sent)
	}
	return res
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
// What the bus's own reasons may contain: a note header
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
// Rule 23's --timeout bounds one version probe, so it does not bound the bus
// child: a default run would otherwise kill its own delivery at five seconds.
// The delivery allowance is the remaining budget instead (rule 25 and
// SPEC-BUS-DELIVERY both name the budget as the resolution horizon), and these
// two flags are what let the bus stop on its own inside that horizon rather than
// be killed at the end of it: one git operation gets a third of what remains,
// capped at a minute and never more than remains, and the attempt count is how
// many such operations fit, never more than the bus's own 25.
func busBounds(remaining time.Duration) (attempts, gitSeconds int) {
	git := min(remaining/3, time.Minute, remaining)
	// The bus counts this flag in whole seconds, so below a second there is no number to
	// name but one. The caller's own deadline is then the tighter of the two bounds, which
	// is the safe way round.
	gitSeconds = max(int(git/time.Second), 1)
	attempts = min(max(int(remaining/(time.Duration(gitSeconds)*time.Second)), 1), 25)
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
func deliver(ctx context.Context, o options, s *snapshot, seen map[string]observed, body []byte, res *tool.Out, env Environment) (string, error) {
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
		res.Item("sent", "to", o.to, "via", strings.Join(args, " "), "line", line)
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
			res.Note(fmt.Sprintf("unchanged since %s to %s; nothing sent", d.ID, o.to))
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
