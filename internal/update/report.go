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
func deliver(ctx context.Context, o options, s *snapshot, seen map[string]observed, body []byte, out io.Writer, env Environment) (string, error) {
	scope := snapshotScope(o)
	save := func() error {
		if o.snapshot != "" {
			return writeSnapshot(o.snapshot, s)
		}
		return nil
	}
	send := func(p pending) (string, error) {
		args := []string{"nova-bus", "send", "--prepared-stdin", "--bus", o.bus, "--remote", o.remote, "--branch", o.branch, "--as", o.as}
		child, cancel := context.WithTimeout(ctx, o.timeout)
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
			return "uncertain", fmt.Errorf("pending %s not confirmed: %s (retry this --send with the same --snapshot; do not prepare again)", p.ID, dash(r.Reason))
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
	child, cancel := context.WithTimeout(ctx, o.timeout)
	prepared := captureRun(child, []string{"nova-bus", "prepare", "--bus", o.bus, "--as", o.as, "--stdin"}, body, ChildCap)
	cancel()
	if prepared.Reason != "" {
		return sent, fmt.Errorf("prepare refused: %s (check nova-bus and the named bus; retry --send)", prepared.Reason)
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
