package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// renewExits is the renew verb's exit table.
const renewExits = "0 renewed (or, with --dry-run, the plan), 1 refused and nothing changed (no daemon state for the friend, or a harness with no new-session route), 2 could not run (a flag, a state file or plist that cannot be read, or the harness or launchctl failed: FAILED, with what was done)."

func renewVerb(w world, redis func(*tool.Flags)) tool.Verb {
	return tool.Verb{
		Name:      "renew",
		Usage:     "renew --as <coordinator> <friend> [--reason <text>] [--state-dir <d>] [--redis <addr>] [--dry-run] [--json]",
		Example:   "", // it needs a friend's daemon state: the runnable example is in its help
		Effect:    tool.Delivery + ": a fresh session in the friend's harness and directory, seeded with one turn; the daemon re-pinned to it and restarted",
		ExitTable: renewExits,
		DryRun:    true,
		Detail: `A broken session gets a fresh one in the same harness and directory (docs/SPEC-FRIEND.md, Renew).
Reads the friend's daemon state: the installed agent's command line (harness, --dir, --session) and
the status file in the state directory (~/.nova-friend/<friend>, or --state-dir: harness, dir, the
pinned session, the broken mark). Starts a new session through the harness's own new-session command
(opencode: ` + friend.RenewRoutes["opencode"] + `; a harness with none is refused, nothing changed), its
first turn the wake brief: the friend's AGENTS.md and memory/ under its directory, named and never
copied; the old session's id; why it was renewed (--reason, else the broken mark's reason); and the
pong line to run when a PING <nonce> arrives. Then re-pins the daemon to the new session (the agent's
--session in its plist when installed, and the status file's pinned session, the broken mark cleared)
and restarts the daemon (launchctl bootout, then bootstrap). The old session is never deleted or
edited. The friend is the last word, or --friend.
Prints, the result first: RENEW OK friend= old= new=, then RENEW NEW friend= harness= old= new=,
RENEW SEEDED friend= bytes=, RENEW PINNED friend= session=, one RENEW RAN command= per launchctl
command, and a RENEW NOTE when no agent is installed (restart the daemon with --session yourself).
--json is the same value: facts friend, old, new; items of kind new, seeded, pinned and ran. A refusal
is RENEW REFUSED: <why>; run: <remedy>. --dry-run reads the state files and runs nothing: RENEW OK
friend= harness= dir= old= bytes= dry_run=true and one RENEW PLAN command= per step it would take.
example: nova-friend renew --as ada bob --reason "the provider refuses every turn" --dry-run`,
		Flags: func(f *tool.Flags) {
			f.Required("as", "your name, the coordinator: the wake brief says who renewed the session")
			f.Required("friend", "the friend whose session to renew, as the last word or --friend")
			f.String("reason", "", "why the session is renewed, said in the wake brief (default: the broken mark's reason; required when there is none)")
			f.String("state-dir", "", "where the friend's daemon state files live (default: ~/.nova-friend/<friend>)")
			redis(f)
		},
		Run: w.renew,
	}
}

// renewArgs moves renew's last bare word, the friend, to --friend, so the
// skeleton's one flag parse reads it; the rest of the line is kept as it is.
func renewArgs(args []string) []string {
	if len(args) == 0 || args[0] != "renew" {
		return args
	}
	bools := map[string]bool{"dry-run": true, "json": true, "h": true, "help": true}
	out := []string{"renew"}
	var friendName []string
	for i := 1; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		switch {
		case a == "--":
			out = append(out, args[i:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && !bools[name] && i+1 < len(args):
			out = append(out, a, args[i+1])
			i++
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
		default:
			friendName = append(friendName, "--friend", a)
		}
	}
	return append(out, friendName...)
}

// renew is the verb: the daemon state read, the route checked, the plan, and
// for real the new session, the re-pin and the restart (docs/SPEC-FRIEND.md, Renew).
func (w world) renew(c *tool.Call) *tool.Out {
	coordinator, name, dry := c.Str("as"), c.Str("friend"), c.DryRun()
	state := cmp.Or(c.Str("state-dir"), friend.DefaultStateDir(w.home, name))
	s, found, err := friend.ReadStatus(state)
	if err != nil {
		return tool.Refuse("the status file cannot be read: " + err.Error())
	}
	agent := friend.Agent{Friend: name, Home: w.home}
	plist, perr := os.ReadFile(agent.PlistPath())
	if perr != nil && !os.IsNotExist(perr) {
		return tool.Refuse("the agent plist cannot be read: " + perr.Error())
	}
	installed := perr == nil
	var args []string
	if installed {
		if args, err = friend.AgentArgs(string(plist)); err != nil {
			return tool.Refuse(agent.PlistPath() + ": " + err.Error())
		}
	}
	install := "nova-friend install --as " + name + " --harness <h> --dir <d>"
	if !found && !installed {
		return saidNo("no daemon state for "+name+": no status file in "+state+" and no agent installed", install)
	}
	harness := cmp.Or(friend.ArgValue(args, "--harness"), s.Harness)
	dir := cmp.Or(friend.ArgValue(args, "--dir"), s.Dir)
	old := dash(cmp.Or(friend.ArgValue(args, "--session"), s.Pinned, s.SessionID))
	reason := c.Str("reason")
	if reason == "" && s.Session == friend.SessionBroken {
		reason = s.SessionReason
	}
	if dir == "" {
		c.Problem("the daemon state names no directory (a daemon from before renew, and no agent installed); it wants the agent: " + install)
	}
	if reason == "" {
		c.Problem("--reason is required: the session is not marked broken; it wants why the session is renewed, said in the wake brief")
	}
	if o := c.Refused(); o != nil {
		return o
	}
	route, routed := friend.RenewRoutes[harness]
	d, err := friend.NewDeliverer(harness, dir, "", w.exec, io.Discard)
	lh, lane := d.(friend.LaneHarness)
	if err != nil || !routed || !lane {
		return saidNo("harness "+harness+" has no new-session route this tool drives; nothing was changed",
			"start a fresh session in "+dir+" by hand, then nova-friend install --as "+name+" --harness "+harness+" --dir "+dir+" --session <its id>")
	}
	agents, memory := friend.Identity(dir, name)
	pong := fmt.Sprintf("%s pong --as %s --nonce <nonce> --to %s --state-dir %s", w.bin(), name, coordinator, state)
	if r := c.Str("redis"); r != "" {
		pong += " --redis " + r
	}
	seed := friend.RenewSeed(name, coordinator, agents, memory, old, reason, pong)
	domain := fmt.Sprintf("gui/%d", w.uid)
	if dry {
		o := tool.Done().Fact("friend", name).Fact("harness", harness).Fact("dir", dir).Fact("old", old).Fact("bytes", len(seed)).
			Item("plan", "command", tool.Text(strings.ReplaceAll(route, "<dir>", dir)))
		if installed {
			o.Item("plan", "command", tool.Text("write "+agent.PlistPath()+" with --session <the new session>")).
				Item("plan", "command", tool.Text("launchctl bootout "+domain+"/"+agent.Label()))
		}
		o.Item("plan", "command", tool.Text("write "+state+"/"+friend.StatusFile+" pinned=<the new session> session=ok"))
		if installed {
			o.Item("plan", "command", tool.Text("launchctl bootstrap "+domain+" "+agent.PlistPath()))
		}
		return o
	}
	fresh, err := lh.OpenSession(c.Ctx, seed)
	if err != nil {
		o := tool.Refuse("the harness started no new session: " + err.Error() + "; nothing was re-pinned")
		o.Remedy = strings.ReplaceAll(route, "<dir>", dir)
		return o
	}
	o := tool.Done().Fact("friend", name).Fact("old", old).Fact("new", fresh).
		Item("new", "friend", name, "harness", harness, "old", old, "new", fresh).
		Item("seeded", "friend", name, "bytes", len(seed))
	failed := func(why string) *tool.Out {
		f := tool.Fail(why+"; the new session is "+fresh+", re-pin it: nova-friend install --as "+name+" --harness "+harness+" --dir "+dir+" --session "+fresh).
			Fact("friend", name).Fact("old", old).Fact("new", fresh)
		f.Items, f.Exit = o.Items, 2
		return f
	}
	if installed {
		repinned, err := friend.RepinPlist(string(plist), fresh)
		if err != nil {
			return failed(agent.PlistPath() + ": " + err.Error())
		}
		if err := os.WriteFile(agent.PlistPath(), []byte(repinned), 0o644); err != nil {
			return failed(err.Error())
		}
		o.Item("ran", "command", tool.Text(friend.Bootout(c.Ctx, agent, w.uid, w.launchctl)))
	}
	s.Friend, s.Harness, s.Dir, s.Pinned = name, harness, dir, fresh
	if s.Session == friend.SessionBroken {
		s.Session, s.SessionID, s.SessionReason, s.BrokenAt = friend.SessionOK, "", "", time.Time{}
	}
	if err := friend.WriteStatus(state, s); err != nil {
		return failed("the status file: " + err.Error())
	}
	if installed {
		ran, err := friend.Bootstrap(c.Ctx, agent.PlistPath(), w.uid, w.launchctl, func() { w.sleep(c.Ctx, time.Second) })
		for _, r := range ran {
			o.Item("ran", "command", tool.Text(r))
		}
		if err != nil {
			return failed(err.Error())
		}
	}
	o.Item("pinned", "friend", name, "session", fresh)
	if !installed {
		o.Note("no agent is installed for " + name + ": restart its daemon with --session " + fresh)
	}
	return o
}

// saidNo is a refusal of a verb that ran and changed nothing: exit 1, with
// the remedy that gets past it.
func saidNo(why, remedy string) *tool.Out {
	o := tool.Refuse(why)
	o.Exit, o.Remedy = 1, remedy
	return o
}

// bin is this binary by path, else its name on PATH.
func (w world) bin() string {
	if w.binary == nil {
		return "nova-friend"
	}
	b, err := w.binary()
	if err != nil {
		return "nova-friend" // ignored: the name on PATH stands in when this binary's path is unknown
	}
	return b
}
