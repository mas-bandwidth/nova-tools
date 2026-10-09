package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The active root tool cell is the only receiver; the server never runs this
// local filesystem command (SPEC-SPRINT.md, Active Codex tool-result binding).
func init() {
	notServed = append(notServed, "seat tool-result")
	verbClasses["seat tool-result"] = classRead
}
func (a *app) cmdSeatToolResult(args []string, stdout, stderr io.Writer) int {
	const name = "seat tool-result"
	mode := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	fs, c := a.verbSetup(name)
	dir := fs.String("directory", "", "absolute private transport directory; wait creates it, ack/release preserve its events")
	session := fs.String("session", "", "exact UUID of the actual Codex root receiving tool output, required")
	namespace := fs.String("namespace", "", "private route label, required; must match the seat push record")
	server := fs.String("reply-server", "", "credential-free private sprint reply host:port, required; must match the seat push record")
	owner := fs.String("receiver", "", "owner token of this bounded root tool cell, required; another live owner is refused")
	timeout := fs.Duration("timeout", 20*time.Second, "wait duration from 0s to 20s; renews a 30-second lease once, never a permanent watcher")
	id := fs.String("event", "", "with ack, the immutable event id emitted through buffered tool output; this never pongs")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes wait, ack or release and flags only ", err, pos...))
	}
	route := friend.ToolResult{Dir: *dir, Scope: friend.ToolResultScope{Session: *session, Namespace: *namespace, Server: *server}, Owner: *owner}
	var event *friend.ToolResultEvent
	switch mode {
	case "wait":
		if *id != "" {
			return refuse(stderr, name, "--event goes with ack; nothing changed")
		}
		event, err = route.Wait(context.Background(), *timeout)
	case "ack":
		err = route.Ack(*id)
	case "release":
		err = route.Release()
	default:
		return refuse(stderr, name, "wants wait, ack or release; run: nova-sprint seat tool-result -h")
	}
	if err != nil {
		return refuse(stderr, name, err.Error()+"; run: nova-sprint seat tool-result -h")
	}
	state := mode
	if mode == "wait" {
		state = "none"
		if event != nil {
			state = "event"
		}
	}
	result := map[string]any{"status": "ok", "action": state, "scope": route.Scope, "event": event, "lease_seconds": int(friend.ToolResultLease / time.Second), "proof": "transport emission only; actual root receipt and seat pong remain separate"}
	line := fmt.Sprintf("TOOL-RESULT OK action=%s session=%s namespace=%s server=%s; transport only, no pong", state, oneline.Field(*session), oneline.Field(*namespace), oneline.Field(*server))
	if event != nil {
		line += fmt.Sprintf("; event=%s untrusted-payload=%s", event.ID, oneline.Quote(event.Text))
	}
	sayOK(stdout, c.json, name, line, result)
	return 0
}

// toolResultDeliverer is selected only by explicit --adapter tool-result on
// the actual Codex harness; arbitrary adapters cannot bypass native delivery.
func toolResultDeliverer(rec sprint.PushRecord) friend.ToolResult {
	return friend.ToolResult{Dir: rec.Target, Scope: friend.ToolResultScope{Session: rec.Session, Namespace: rec.ToolResultNamespace, Server: rec.ToolResultServer}}
}
