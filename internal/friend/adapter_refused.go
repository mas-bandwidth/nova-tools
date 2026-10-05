package friend

// Refusals are the harnesses of the survey of 2026-10-04 (SPEC-FRIEND.md,
// the harness survey) with no adapter: each the one-line reason, the route
// the vendor documents where there is one, and what would make it real.
// None of them is installed on this machine; one with a route gets its
// adapter the day its binary and login are there.
var Refusals = map[string]string{
	"copilot":  "not installed here; the route is `copilot -p <text> --resume <id>`, after `copilot login`",
	"cursor":   "not installed here; the route is `agent -p --resume <chatId> <text>`, after `agent login`",
	"amp":      "not installed here; the route is `amp -x --thread-id <id> <text>`, with AMP_API_KEY",
	"goose":    "not installed here; the route is `goose run -n <name> -r -t <text>`, with a provider key",
	"kiro":     "not installed here; the route is `kiro-cli chat --resume-id <id> <text>`, after `kiro-cli login`",
	"cline":    "not installed here; the route is `cline --id <session> <text>` to the hub at 127.0.0.1:25463, after `cline auth`",
	"aider":    "no route: aider keeps no session to adopt, only a history file replayed into a new process",
	"roo":      "no route: Roo Code's sendMessage API lives inside VS Code, reachable only from another extension",
	"windsurf": "no route: nothing documented reaches a running Cascade conversation from outside the app",
	"zed":      "no route: Zed is an ACP client; nothing documented reaches a running thread from outside",
	"warp":     "no route: `oz run message send` reaches cloud runs only; `oz agent run` starts a new run",
}

// RefusedHarnesses lists Refusals in a fixed order, for the registry.
var RefusedHarnesses = []string{"copilot", "cursor", "amp", "goose", "kiro", "cline", "aider", "roo", "windsurf", "zed", "warp"}
