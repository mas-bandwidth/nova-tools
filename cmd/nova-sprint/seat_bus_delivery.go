package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"io"
	"os"
)

// cmdSeatDeliver is the receiver's concrete --exec command (SPEC-SPRINT 8).
// It reads the current seat target through the API and delivers on the caller;
// a server never waits inside its control line for the harness to finish.
func (a *app) cmdSeatDeliver(args []string, stdout, stderr io.Writer) int {
	const name = "seat deliver"
	fs, c := a.verbSetup(name)
	text := fs.String("text", "", "message to deliver; without this flag read the bus receiver's standard input (at most 1 MiB)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if c.actor == "" {
		return refuse(stderr, name, "--actor <seat> is required: whose proven target receives the message")
	}
	body := []byte(*text)
	if !(verbArgs{fs: fs}).given("text") {
		body, err = io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
	}
	if len(body) > 1<<20 {
		return refuse(stderr, name, "message is over 1 MiB; split the message before sending it")
	}
	o, err := a.readPushObservation(context.Background(), *c, "bus", a.server(fs))
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if o.Now.IsZero() || o.Set.Proven.After(o.Now) || !sprint.PushLive(o.Set.PushRecord, true, o.Now) {
		fmt.Fprintf(stderr, "SEAT DELIVER DOWN: the judgments target is unproven; run: %s\n", sprint.PushSetup(c.actor, o.Set.PushRecord, true))
		return 1
	}
	if o.Set.Session == "" && o.Set.Adapter != sprint.AdapterFolder {
		return refuse(stderr, name, "the resident harness session id is unknown; run: nova-sprint seat push --actor "+oneline.ShellWord(c.actor)+" --harness "+oneline.ShellWord(o.Set.Harness)+" --target "+oneline.ShellWord(o.Set.Target)+" --session <live-session-id>, then prove its nonce")
	}
	if why := a.deliverPush(context.Background(), o.Set.PushRecord, string(body)); why != "" {
		return refuse(stderr, name, "bus delivery failed: "+why)
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("SEAT DELIVER OK name=%s", oneline.Field(c.actor)), map[string]any{"name": c.actor, "session": o.Set.Session})
	return 0
}
