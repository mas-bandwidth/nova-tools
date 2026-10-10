package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// runNotifications is the run verb's delivery-only route (SPEC-FRIEND.md,
// notifications). It constructs none of the ordinary run's sprint, lane, presence or
// job machinery, including during startup, retry and restart.
func (w world) runNotifications(c *tool.Call) *tool.Out {
	addr := c.Want("redis", "the bus store's address, host:port")
	if o := c.Refused(); o != nil {
		return o
	}
	name, dir := c.Str("as"), c.Str("dir")
	state := filepath.Join(w.stateDir(c, dir), "notifications")
	deliver, err := friend.NewDeliverer(c.Str("harness"), dir, c.Str("session"), w.exec, c.Stdout)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	cx, ok := deliver.(*friend.Codex)
	if !ok {
		return tool.Refuse("--notifications-only wants the Codex queue adapter")
	}
	cx.QueueOnly, cx.Now = true, w.now
	if c.DryRun() {
		fmt.Fprintf(c.Stdout, "RUN DRY-RUN notifications-only=true as=%s state=%s kinds=%s; no store opened, proof, beat or jobs changed\n", name, state, c.Str("notify-kinds"))
		return tool.Exit(0)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	record := func(line string) {
		if err := friend.Record(state, line); err != nil {
			fmt.Fprintln(c.Stderr, "RUN NOTE notification audit: "+err.Error())
		}
	}
	st, closeStore := w.openUntil(ctx, addr, record)
	if st == nil {
		return tool.Exit(0)
	}
	defer closeStore()
	d := &friend.Daemon{Friend: name, Harness: c.Str("harness"), Dir: dir, Store: st, Deliver: cx, Now: w.now, Pause: w.sleep, Record: record,
		Coordinator: c.Str("coordinator"), NotificationOnly: true, NotificationStateDir: state,
		Notifications: &friend.NotificationPolicy{Kinds: commaList(c.Str("notify-kinds")), Window: c.Dur("notify-window")},
		PongCommand: func(nonce string) string {
			bin, err := w.binary()
			if err != nil {
				bin = "nova-friend"
			}
			return fmt.Sprintf("%s pong --as %s --nonce %s --state-dir %s --redis %s", bin, name, nonce, w.stateDir(c, dir), addr)
		},
	}
	if err := d.Run(ctx); err != nil {
		return tool.Fail("notification delivery stopped with pending input preserved: " + err.Error())
	}
	return tool.Exit(0)
}

// installNotifications never plans/writes harness settings or performs the native
// proof check (SPEC-FRIEND.md, notifications). Deployment remains an explicit install.
func (w world) installNotifications(c *tool.Call, a friend.Agent) *tool.Out {
	src := a.Binary
	placed, copy, err := a.BinaryPlan()
	if err != nil {
		return tool.Refuse("notification binary plan: " + err.Error())
	}
	if c.DryRun() {
		a.Binary = placed
		out := tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Fact("binary", placed)
		if copy {
			out.Item("plan", "command", tool.Text("copy "+src+" "+placed))
		}
		return out.Item("plan", "command", tool.Text("write "+a.PlistPath())).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootstrap gui/%d %s", w.uid, a.PlistPath()))).
			Note("the agent runs: " + a.Said()).Note("no native harness settings or proof are changed")
	}
	a.Sleep = func(d time.Duration) { w.sleep(context.Background(), d) }
	path, ran, err := friend.Install(context.Background(), a, w.uid, w.launchctl, func(path string, raw []byte) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, raw, 0o644)
	}, func() { w.sleep(context.Background(), time.Second) })
	if err != nil {
		return tool.Fail("notification installation: " + err.Error())
	}
	out := tool.Done().Fact("label", a.Label()).Fact("plist", path).Fact("binary", placed)
	for _, line := range ran {
		out.Item("ran", "command", tool.Text(line))
	}
	return out.Note("queue acceptance is not session proof; native proof and job state are unchanged")
}
