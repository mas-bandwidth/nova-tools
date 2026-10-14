package main

import (
	"errors"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)


type dshRoute struct {
	route dshdesktop.Route
	since time.Time
}

// dshConfig refuses partial routes before any hook record or lease is written.
// --harness deepseek (or its alias dsh) selects the route; the --dsh-* flags
// carry the native include path, private event directory and exact live session.
// No gateway and no cookie: delivery uses the harness agent registry.
func (w world) dshConfig(c *tool.Call) (*dshRoute, error) {
	harness := c.Str("harness")
	r := &dshRoute{
		route: dshdesktop.Route{
			EventsDir: c.Str("dsh-events-dir"),
			HooksFile: c.Str("dsh-hooks-file"),
			SessionID: c.Str("dsh-session"),
		},
	}
	rawSince := c.Str("dsh-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("dsh cutover wants --dsh-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if harness == "" && r.route.EventsDir == "" && r.route.HooksFile == "" && r.route.SessionID == "" && rawSince == "" {
		return nil, nil
	}
	if harness != "" && harness != "deepseek" && harness != "dsh" {
		return nil, errors.New("--harness wants deepseek (or dsh); other harnesses keep their own route")
	}
	if r.route.EventsDir == "" || r.route.HooksFile == "" || r.route.SessionID == "" {
		return nil, errors.New("dsh delivery wants --dsh-events-dir and --dsh-hooks-file with a pinned --dsh-session")
	}
	if c.Bool("once") {
		return nil, errors.New("dsh delivery wants a running hook client; omit --once")
	}
	if w.dshValidate == nil || w.dshDeliver == nil {
		return nil, errors.New("dsh delivery wants a configured local native receiver")
	}
	if c.DryRun() {
		if err := dshdesktop.ValidateRoute(r.route); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.dshValidate(r.route); err != nil {
		return nil, err
	}
	return r, nil
}
