package main

import (
	"errors"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const maxCodexEvent = notice.MaxSaved

// codexRoute is the Codex desktop route's flags, or the Antigravity route's
// (antigravity set): the shim's mechanism, and the native startup recovery.
type codexRoute struct {
	socket, thread, directory string
	antigravity               *antigravityRoute
	since                     time.Time
}

func (r *codexRoute) label() string {
	if r != nil && r.antigravity != nil {
		return "Antigravity"
	}
	return "Codex"
}

// codexConfig refuses partial routes before any hook record or lease is written.
func (w world) codexConfig(c *tool.Call) (*codexRoute, error) {
	harness := strings.TrimSpace(c.Str("harness"))
	hasCodex := c.Str("codex-socket") != "" || c.Str("codex-thread") != "" || c.Str("codex-events-dir") != "" || c.Str("codex-since") != ""
	hasAntigravity := c.Str("antigravity-dir") != "" || c.Str("antigravity-session") != "" || c.Str("session") != "" || c.Str("antigravity-server") != "" || c.Str("antigravity-events-dir") != "" || c.Str("antigravity-since") != ""
	if hasCodex && hasAntigravity {
		return nil, errors.New("cannot combine --codex-* and --antigravity-* flags")
	}
	if strings.EqualFold(harness, "codex") && hasAntigravity {
		return nil, errors.New("--harness codex cannot use --antigravity-* flags")
	}
	if strings.EqualFold(harness, "antigravity") && hasCodex {
		return nil, errors.New("--harness antigravity cannot use --codex-* flags")
	}
	if strings.EqualFold(harness, "muse") && hasAntigravity {
		return nil, errors.New("one harness at a time: --harness Muse or the Antigravity desktop route, not both")
	}
	if strings.EqualFold(harness, "antigravity") || hasAntigravity {
		if harness != "" && !strings.EqualFold(harness, "antigravity") {
			return nil, errors.New("--harness wants muse or antigravity (the Codex desktop route uses the --codex-* flags)")
		}
		return w.antigravityConfig(c)
	}
	r := &codexRoute{socket: c.Str("codex-socket"), thread: c.Str("codex-thread"), directory: c.Str("codex-events-dir")}
	rawSince := c.Str("codex-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("codex cutover wants --codex-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if r.socket == "" && r.thread == "" && r.directory == "" && rawSince == "" && !strings.EqualFold(harness, "codex") {
		return nil, nil
	}
	if r.socket == "" || r.thread == "" || r.directory == "" {
		return nil, errors.New("codex delivery wants --codex-socket, --codex-thread and --codex-events-dir together")
	}
	if c.Bool("once") {
		return nil, errors.New("codex delivery wants a running hook client; omit --once")
	}
	if w.codexLock == nil || w.codexValidate == nil || w.files == nil || w.codexDeliver == nil {
		return nil, errors.New("codex delivery wants a configured local native receiver")
	}
	if c.DryRun() {
		if _, err := codexdesktop.SavedPath(r.directory, "route"); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.codexValidate(r.socket, r.thread, r.directory); err != nil {
		return nil, err
	}
	return r, nil
}

// Reject oversized external fields before encoding can allocate another copy.
// The final encoded envelope is checked independently for JSON escaping overhead.
func codexMessageBytes(m bus.Message) int {
	total := len(m.ID) + len(m.From) + len(m.Subject) + len(m.Re) + len(m.Kind) + len(m.Body) + len(m.Token) + len(m.Once)
	for _, name := range m.To {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, name := range m.CC {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, mark := range m.Marks {
		total += len(mark.Key) + len(mark.Field) + len(mark.Value)
		if total > maxCodexEvent {
			return total
		}
	}
	return total
}
