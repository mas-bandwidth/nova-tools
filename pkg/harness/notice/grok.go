package notice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// Grok is the grok shim (docs/SPEC-GROK-HOOK.md, tla/GrokHookNotice.tla).
// Into a running turn and waking an idle session are one mechanism: a line
// appended to the wake file one Monitor tails. Consumption is a completed
// turn: the installed hooks publish "<gen> busy" and "<gen> idle" as one
// snapshot, and a generation is complete only when a sample sees it idle.
type Grok struct {
	Home, Wake, Notices string
	seenGen             int
	sawBusy             bool
}

// Name is the harness word.
func (g *Grok) Name() string { return "grok" }

// Dir is the private notice directory under the grok home.
func (g *Grok) Dir() string { return g.Notices }

// Push appends one line. The receipt is wake when the session was idle and
// turn when a turn was running.
func (g *Grok) Push(_ context.Context, n Notice) (string, error) {
	idle, gen, err := readGrokSample(g.Home)
	if err != nil {
		return "", err
	}
	if err := deliverGrokWake(g.Wake, n.Text()); err != nil {
		return "", err
	}
	if gen > g.seenGen {
		g.seenGen = gen
	}
	g.sawBusy = !idle
	if idle {
		return "wake", nil
	}
	return "turn", nil
}

// Consumed applies one snapshot (GrokHookNotice, Observe). A new generation
// sampled idle is a finished turn; a busy sample records the turn; an idle
// sample after a recorded busy one finishes it.
func (g *Grok) Consumed(context.Context, Notice) (bool, error) {
	idle, gen, err := readGrokSample(g.Home)
	if err != nil {
		return false, err
	}
	advanced := gen > g.seenGen
	if advanced {
		g.seenGen = gen
	}
	switch {
	case advanced && idle:
		g.sawBusy = false
		return true, nil
	case !idle || advanced:
		g.sawBusy = true
		return false, nil
	case g.sawBusy:
		g.sawBusy = false
		return true, nil
	}
	return false, nil
}

// GrokHome is --grok-home, else GROK_HOME, else the grok directory under the
// user home. The path is absolute and clean. A line break is refused because
// the installed command is one line; other characters stay, and grokCommand
// quotes each argument.
func GrokHome(flag string, getenv func(string) string) (string, error) {
	home := flag
	if home == "" && getenv != nil {
		home = getenv("GROK_HOME")
	}
	if home == "" {
		u, err := os.UserHomeDir()
		if err != nil || u == "" {
			return "", errors.New("grok install wants an absolute home: pass --grok-home")
		}
		home = filepath.Join(u, ".grok")
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", errors.New("grok home wants a clean absolute path")
	}
	if strings.ContainsAny(home, "\r\n") || strings.ContainsRune(home, 0) {
		return "", errors.New("grok home wants a path without a line break")
	}
	return home, nil
}

// shQuote quotes one argument for sh. A single quote inside the
// argument ends the quote, adds a quoted quote, and opens it again.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// grokHookScript replaces state with one snapshot, "<gen> busy" or "<gen> idle".
// Busy increments gen. Idle keeps the gen it read. One rename publishes both,
// so a reader never pairs a new generation with the previous word. It always
// exits 0, and it never sends or acks.
const grokHookScript = "#!/bin/sh\n" +
	"# Replaces state with one snapshot, \"<gen> busy\" or \"<gen> idle\".\n" +
	"# Busy increments gen. Idle keeps the gen it read. One rename publishes\n" +
	"# both, so a reader never pairs a new generation with the previous word.\n" +
	"# It never sends or acks.\n" +
	"word=$1\n" +
	"dir=$2\n" +
	"case $word in\n" +
	"busy|idle) ;;\n" +
	"*) exit 0 ;;\n" +
	"esac\n" +
	"mkdir -p \"$dir\" || exit 0\n" +
	"gen=0\n" +
	"if [ -f \"$dir/state\" ]; then\n" +
	"  read -r prev _ < \"$dir/state\" || prev=0\n" +
	"  case \"$prev\" in\n" +
	"  ''|*[!0-9]*) gen=0 ;;\n" +
	"  *) gen=$prev ;;\n" +
	"  esac\n" +
	"fi\n" +
	"if [ \"$word\" = busy ]; then\n" +
	"  gen=$((gen + 1))\n" +
	"fi\n" +
	"tmp=$dir/.state.$$\n" +
	"printf '%s %s\\n' \"$gen\" \"$word\" > \"$tmp\" || exit 0\n" +
	"mv \"$tmp\" \"$dir/state\" || exit 0\n" +
	"exit 0\n"

type grokHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type grokHookGroup struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []grokHookCommand `json:"hooks"`
}

type grokHooks struct {
	UserPromptSubmit []grokHookGroup `json:"UserPromptSubmit"`
	Stop             []grokHookGroup `json:"Stop"`
	StopFailure      []grokHookGroup `json:"StopFailure"`
	StopCancelled    []grokHookGroup `json:"StopCancelled"`
	Notification     []grokHookGroup `json:"Notification"`
}

type grokHookFile struct {
	Hooks grokHooks `json:"hooks"`
}

func grokCommand(script, word, stateDir string) grokHookCommand {
	cmd := shQuote(script) + " " + shQuote(word) + " " + shQuote(stateDir)
	return grokHookCommand{Type: "command", Command: cmd, Timeout: 5}
}

func grokHookDoc(script, stateDir string) grokHookFile {
	idle := []grokHookGroup{{Hooks: []grokHookCommand{grokCommand(script, "idle", stateDir)}}}
	return grokHookFile{Hooks: grokHooks{
		UserPromptSubmit: []grokHookGroup{{Hooks: []grokHookCommand{grokCommand(script, "busy", stateDir)}}},
		Stop:             idle,
		StopFailure:      idle,
		StopCancelled:    idle,
		Notification:     []grokHookGroup{{Matcher: "idle_prompt", Hooks: []grokHookCommand{grokCommand(script, "idle", stateDir)}}},
	}}
}

// InstallGrok writes the hook files and creates the wake file when it
// is absent. An existing wake file is left byte for byte.
func InstallGrok(home string) (string, error) {
	hooks := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return "", err
	}
	script := filepath.Join(hooks, "nova-hook-grok.sh")
	if err := atomicfile.Write(script, []byte(grokHookScript), 0o755, atomicfile.ExactMode()); err != nil {
		return "", err
	}
	stateDir := filepath.Join(home, "nova-hook")
	raw, err := json.MarshalIndent(grokHookDoc(script, stateDir), "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := atomicfile.Write(filepath.Join(hooks, "nova-hook.json"), raw, 0o644, atomicfile.ExactMode()); err != nil {
		return "", err
	}
	wake := filepath.Join(home, "nova-hook.wake")
	if _, err := os.Stat(wake); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		f, err := os.OpenFile(wake, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return "", err
		}
		if cerr := f.Chmod(0o600); cerr != nil {
			if closeErr := f.Close(); closeErr != nil {
				return "", errors.Join(cerr, closeErr)
			}
			return "", cerr
		}
		if err := f.Close(); err != nil {
			return "", err
		}
	}
	return wake, nil
}

// readGrokSample reads one state snapshot. The line is "<gen> busy",
// "<gen> idle", or a legacy single word whose generation is 0. A missing
// file is idle at generation 0. A generation is a completed turn only when
// this snapshot is idle at that generation (tla/GrokHookNotice.tla, Observe).
func readGrokSample(home string) (idle bool, gen int, err error) {
	b, err := os.ReadFile(filepath.Join(home, "nova-hook", "state"))
	if os.IsNotExist(err) {
		return true, 0, nil
	}
	if err != nil {
		return true, 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return true, 0, nil
	}
	word := fields[0]
	if len(fields) >= 2 {
		n, nerr := strconv.Atoi(fields[0])
		if nerr == nil && n >= 0 {
			return fields[1] != "busy", n, nil
		}
		word = fields[len(fields)-1]
	}
	return word != "busy", 0, nil
}

// deliverGrokWake appends one notice line. It never truncates the file and
// never puts more than one line in a single write.
func deliverGrokWake(wake, text string) error {
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return errors.New("grok notice wants one line")
	}
	f, err := os.OpenFile(wake, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text + "\n")
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

