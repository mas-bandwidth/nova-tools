package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
		if err = writeSnapshotWith(o.snapshot, state, env.rename()); err != nil {
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
var busRefusals = []string{"SEND FAIL ", "SEND REFUSED: ", "SEND OK "}

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

// deliveryAllowance is what is left of the budget. A delivery that cannot start
// inside it is refused, not killed.
func deliveryAllowance(ctx context.Context, now time.Time) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return deadline.Sub(now)
}

// noteSubject is the one Subject line the composed note already carries.
func noteSubject(body []byte) string {
	for _, l := range strings.Split(string(body), "\n") {
		if l == "" {
			break
		}
		if s, ok := strings.CutPrefix(l, "Subject: "); ok && s != "" && !strings.ContainsAny(s, "\r\n") {
			return s
		}
	}
	return "-"
}

// redisSend is one nova-bus send on the Redis bus. nova-bus reads the store from
// NOVA_BUS_REDIS; this argv names no checkout, remote or branch.
// SPEC-UPDATE.md rule 24: delivery is nova-bus's, and only a confirmed SEND OK
// records the id the receipt keeps.
func redisSend(ctx context.Context, env Environment, as, to, subject string, body []byte, retry string) (id, line string, args []string, err error) {
	allowance := deliveryAllowance(ctx, env.Now())
	if allowance <= 0 {
		return "", "", nil, fmt.Errorf("delivery budget exhausted (%s)", retry)
	}
	args = []string{"nova-bus", "send", "--as", as, "--to", to, "--subject", subject, "--stdin"}
	child, cancel := context.WithTimeout(ctx, allowance)
	r := env.runProcess(child, args, bytes.NewReader(body), ChildCap)
	cancel()
	id, line = sendOK(r.Stdout)
	if r.Reason != "" || id == "" {
		return "", "", args, fmt.Errorf("send not confirmed: %s; the bus said: %s (%s)", dash(r.Reason), busSaid(r), retry)
	}
	return id, line, args, nil
}

// sendOK reads the id from nova-bus's `SEND OK id=<id>` line.
// SPEC-UPDATE.md rule 24: the id is what the receipt records.
func sendOK(stdout string) (id, line string) {
	for _, l := range strings.Split(stdout, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[0] != "SEND" || f[1] != "OK" {
			continue
		}
		for _, v := range f[2:] {
			if rest, ok := strings.CutPrefix(v, "id="); ok && rest != "" {
				return rest, l
			}
		}
	}
	return "", ""
}

func deliver(ctx context.Context, o options, s *snapshot, seen map[string]observed, body []byte, res *tool.Out, env Environment) (string, error) {
	scope := snapshotScope(o)
	save := func() error {
		if o.snapshot != "" {
			return writeSnapshotWith(o.snapshot, s, env.rename())
		}
		return nil
	}
	if d, ok := s.Delivered[scope]; ok && sameObserved(d.Observed, seen) {
		res.Note(fmt.Sprintf("unchanged since %s to %s; nothing sent", d.ID, o.to))
		return "no", nil
	}
	if ctx.Err() != nil {
		return "no", fmt.Errorf("delivery budget exhausted (retry --send with the same --snapshot)")
	}
	id, line, args, err := redisSend(ctx, env, o.as, o.to, noteSubject(body), body, "retry --send with the same --snapshot")
	if err != nil {
		return "uncertain", err
	}
	s.Delivered[scope] = delivery{cloneObserved(seen), id, env.Now().UTC().Format(time.RFC3339)}
	if err = save(); err != nil {
		return "uncertain", err
	}
	res.Item("sent", "to", o.to, "via", strings.Join(args, " "), "line", line)
	return "yes", nil
}
