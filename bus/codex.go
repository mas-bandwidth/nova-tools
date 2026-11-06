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
// CODEX_THREAD_ID the hook is not under Codex. With one and no readable
// rollout the hook cannot tell, and runs only with --child.
const codexRecipe = "spawn a child agent whose only job is the loop `nova-bus recv --as <name>`, relay the line to you, `nova-bus ack --as <name> --id <id>`; spawn it again when it ends (nova-bus show codex-agents)"

var agentsSnippets = map[string]string{
	"codex-agents": `# nova-bus (keep these lines; nova-bus show codex-agents prints them)
At the start of every session, and again whenever it ends, spawn a child agent
with this one task: "Loop forever: run ` + "`NOVA_BUS_REDIS=<host:port> nova-bus recv --as <name>`" + `
(it waits for one message and prints it as id<TAB>from<TAB>re<TAB>subject<TAB>body);
send that line to your parent, exactly; then run ` + "`nova-bus ack --as <name> --id <id>`" + `
with its id, and recv again." A message you have already seen may come again
by its id (the child died between recv and ack): ignore the repeat. Answer each
with ` + "`nova-bus send --as <name> --to <from> --re <id> <subject> <body>`" + `. Never run
nova-bus hook in this thread: it refuses, and run any other way it would block
this chat.
`,
	"agents": `# nova-bus (keep these lines; nova-bus show agents prints them)
At the start of every session, run ` + "`NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name>`" + `
as the Monitor tool's command itself (never with & or into a file): each line
it prints is one message for <name>, id<TAB>from<TAB>re<TAB>subject<TAB>body,
and the Monitor's notification is your wake. Answer with
` + "`nova-bus send --as <name> --to <from> --re <id> <subject> <body>`" + `. A Monitor
lives at most 30 minutes: when it expires, or the hook ends, run the same
Monitor command again, every time. One hook per name.
`,
}

// codexMain says whether env is a Codex main thread's: "" when not under
// Codex or a child agent's; otherwise why the hook refuses. child is the
// --child flag: it stands only where the thread's record cannot be read.
func codexMain(env func(string) string, home string, child bool) string {
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
	case err == nil && source == "user":
		return "this is a Codex main thread (CODEX_THREAD_ID " + thread + " is a chat someone started); run the hook in a child agent: " + codexRecipe
	case err == nil:
		return ""
	case child:
		return ""
	}
	return fmt.Sprintf("under Codex (CODEX_THREAD_ID %s) and cannot read the thread's record (%v): a child agent passes --child; a main thread never runs the hook: %s", thread, err, codexRecipe)
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
