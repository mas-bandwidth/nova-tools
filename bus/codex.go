package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Codex runs the hook in a child agent, never in the main chat. Anything
// that injects into the main chat from outside (v1.6.29's follower
// start-turn into the thread, PR #6185 until 68ef37195) can leave it hung:
// Stella's chat stuck on "Thinking" twice on 2026-11-06, every later
// message refused ("Cannot steer conversation ... without an active turn
// id"). A hook run in the main chat blocks it instead. So the hook refuses a
// Codex main thread (exit 2), and `nova-bus show codex-agents` prints the
// AGENTS.md lines that make every fresh session spawn the child unasked.
//
// How a Codex main thread is told from a child agent's: the Codex app-server
// sets CODEX_THREAD_ID in the environment of every command it runs (the
// codex binary in ChatGPT.app 26.1029.61003 sets it beside PATH in
// exec_command, with CODEX_SANDBOX, CODEX_TOOL_CALL_ID, CODEX_VERSION), and
// each thread's rollout, <CODEX_HOME or ~/.codex>/sessions/<y>/<m>/<d>/
// rollout-<start>-<thread>.jsonl, opens with a session_meta line whose
// thread_source is "user" for a chat a person started and "subagent" for a
// child agent (source {"subagent":{"thread_spawn":{"parent_thread_id":..}}};
// 151 child and 8 user rollouts on the studio, 2026-11-06). With no
// CODEX_THREAD_ID the command is not under Codex. With one and no readable
// rollout, recv cannot tell and refuses too.
const codexRecipe = "spawn a child agent whose only job is the loop `NOVA_BUS_REDIS=<host:port> nova-bus recv --as <name>`, send the line to you, `NOVA_BUS_REDIS=<host:port> nova-bus ack --as <name> --id <id>`, again; spawn it again when it ends (nova-bus show codex-agents)"

var agentsSnippets = map[string]string{
	"codex-agents": `# nova-bus (keep these lines; nova-bus show codex-agents prints them)
At the start of every session, and whenever it ends, spawn one child agent
with this one task, which it keeps running: "Loop: run
` + "`NOVA_BUS_REDIS=<host:port> nova-bus recv --as <name>`" + ` (it waits for one message and
prints it as id<TAB>from<TAB>re<TAB>subject<TAB>body; RECV BAD or RECV NONE means recv again);
send that line to your parent as a message, exactly; run
` + "`NOVA_BUS_REDIS=<host:port> nova-bus ack --as <name> --id <id>`" + ` (ACK FAILED: handled already, go on);
then recv again." A message may reach you twice, by its id: ignore the repeat.
Answer each with ` + "`NOVA_BUS_REDIS=<host:port> nova-bus send --as <name> --to <from> --re <id> <subject> <body>`" + `.
Never run nova-bus hook or recv in this thread: they refuse here.
`,
	"agents": `# nova-bus (keep these lines; nova-bus show agents prints them)
At the start of every session, run ` + "`NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name>`" + `
as the Monitor tool's command itself (never with & or into a file): each line
it prints is one message for <name>, id<TAB>from<TAB>re<TAB>subject<TAB>body,
except a line starting "HOOK " (status: HOOK RECONNECTED, HOOK BAD), and the
Monitor's notification is your wake. Answer with
` + "`NOVA_BUS_REDIS=<host:port> nova-bus send --as <name> --to <from> --re <id> <subject> <body>`" + `.
A Monitor lives at most 30 minutes: when it expires, or the hook ends, run
the same Monitor command again, every time. One hook per name.
`,
}

// codexHook says why the hook refuses under Codex ("" when not under it):
// any Codex thread, main or child, because a child whose record cannot be
// read is not told from a main thread, and a hook in a main thread blocks
// the chat.
func codexHook(env func(string) string) string {
	thread := env("CODEX_THREAD_ID")
	if thread == "" {
		return ""
	}
	return "this runs under Codex (CODEX_THREAD_ID " + thread + "), where the hook never runs: receive with recv in a child agent: " + codexRecipe
}

// codexRecv says why recv refuses under Codex ("" when not under it, or in
// a child agent): a recv in a main thread blocks the chat, and so does one
// whose thread cannot be told from a main thread's.
func codexRecv(env func(string) string, home string) string {
	thread := env("CODEX_THREAD_ID")
	if thread == "" {
		return ""
	}
	if h := env("CODEX_HOME"); h != "" {
		home = h
	} else {
		home = filepath.Join(home, ".codex")
	}
	source, err := threadSource(home, thread)
	switch {
	case err == nil && source == "subagent":
		return ""
	case err == nil:
		return "this is a Codex main thread (CODEX_THREAD_ID " + thread + " is a chat someone started), where recv would block the chat: receive with recv in a child agent: " + codexRecipe
	}
	return fmt.Sprintf("under Codex (CODEX_THREAD_ID %s) and cannot read the thread's record (%v), so this may be the main chat, which recv would block: receive with recv in a child agent: %s", thread, err, codexRecipe)
}

// threadSource is the thread_source of thread's rollout under home: "user"
// for a chat a person started, "subagent" for a child agent.
func threadSource(home, thread string) (string, error) {
	if !safeID(thread) {
		return "", errors.New("the thread id is not a Codex id")
	}
	suffix := "-" + thread + ".jsonl"
	var found string
	err := filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), suffix) {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("%s/sessions: %w", home, err)
	}
	if found == "" {
		return "", fmt.Errorf("no rollout for thread %s under %s/sessions", thread, home)
	}
	f, err := os.Open(found)
	if err != nil {
		return "", err
	}
	defer f.Close() // read only
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ThreadSource string `json:"thread_source"`
		} `json:"payload"`
	}
	if !scan.Scan() || json.Unmarshal(scan.Bytes(), &row) != nil || row.Type != "session_meta" || row.Payload.ThreadSource == "" {
		return "", fmt.Errorf("%s does not open with a session_meta line", found)
	}
	return row.Payload.ThreadSource, nil
}

// harnessOf is the harness a command runs under, read from its whole
// environment and decided in a fixed order, whatever the variables' order.
// Codex sets CODEX_THREAD_ID in every command it runs, and wins: a Codex
// session started inside a Claude Code session inherits CLAUDECODE and has
// no Monitor. Claude Code sets CLAUDECODE=1, next (so a Claude Code session
// that happens to hold a GEMINI_API_KEY is not taken for Antigravity).
// Antigravity's agent runs its commands with ANTIGRAVITY_AGENT_PORT,
// ANTIGRAVITY_LS_ADDRESS, ANTIGRAVITY_CSRF_TOKEN and GEMINI_CLI_CSRF_TOKEN
// (the variables v1.6.29 nova-hook read from its shell and passed on to
// agentapi), and only those count. Grok sets GROK_HOME. Anything else is
// taken for Claude Code or Muse Code, whose recipe is the Monitor.
func harnessOf(environ []string) string {
	set := map[string]bool{}
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	switch {
	case set["CODEX_THREAD_ID"]:
		return "codex"
	case set["CLAUDECODE"]:
		return "claude"
	case set["ANTIGRAVITY_AGENT_PORT"] || set["ANTIGRAVITY_LS_ADDRESS"] || set["ANTIGRAVITY_CSRF_TOKEN"] || set["GEMINI_CLI_CSRF_TOKEN"]:
		return "antigravity"
	case set["GROK_HOME"]:
		return "grok"
	}
	return "claude"
}

// hookInRecipe is the one or two lines that hook name in on harness: what a
// verb that acts as name says when name is not hooked in (Glenn,
// 2026-11-06: "you have to hook in before you can send messages on nova-bus
// ... if you try to send without hooking in, it tells you how to hook in").
func hookInRecipe(harness, name, addr string) string {
	cmd := "NOVA_BUS_REDIS=" + addr + " nova-bus"
	switch harness {
	case "codex":
		return "spawn a child agent that loops `" + cmd + " recv --as " + name + "`, a message to you, `" + cmd + " ack --as " + name + " --id <id>`; run: nova-bus show codex-agents"
	case "grok":
		return "Monitor: tail -n 0 -F $GROK_HOME/nova-hook.wake, then `" + cmd + " hook --as " + name + " --harness grok --grok-home $GROK_HOME --service`; run: nova-bus hook -h"
	case "antigravity":
		return "`" + cmd + " hook --as " + name + " --harness antigravity --antigravity-dir <workspace> --antigravity-session <conversation id> --antigravity-events-dir ~/.gemini/antigravity/nova-bus-" + name + " --service`; run: nova-bus hook -h"
	}
	return "run `" + cmd + " hook --as " + name + "` as the Monitor tool's command; run: nova-bus show agents"
}
