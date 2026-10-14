package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// hookHarnessGrok is this client's one harness row. Other names stay with
// their own adapters. tla/GrokHookNotice.tla is the notice.
const hookHarnessGrok = "grok"

// grokNotice is one pending wake line (tla/GrokHookNotice.tla). waiting counts
// notes not yet written. outstanding is the line already written. sawBusy is
// the turn that line met. seenGen is how many busy marks are already applied;
// the turn file holds the rest (pendingBusy in the module).
type grokNotice struct {
	outstanding bool
	waiting     int
	sawBusy     bool
	seenGen     int
}

func (g *grokNotice) add(n int) {
	if n > 0 {
		g.waiting += n
	}
}

// observe applies one sample. gen is the busy marks recorded so far. A gen
// ahead of seenGen is a busy mark that arrived since the last read, and it
// still counts when the state word is already idle again: the idle write
// replaces the word and leaves the turn file. An idle wake that never records
// a busy mark stays outstanding.
func (g *grokNotice) observe(idle bool, gen int) {
	advanced := gen > g.seenGen
	if advanced {
		g.seenGen = gen
	}
	if !g.outstanding {
		return
	}
	if advanced && idle {
		g.outstanding = false
		g.sawBusy = false
		return
	}
	if !idle || advanced {
		g.sawBusy = true
		return
	}
	if g.sawBusy {
		g.outstanding = false
		g.sawBusy = false
	}
}

// release writes at most one line, and only while none is outstanding.
// kind is wake when the session is idle and turn when a turn is already running.
func (g *grokNotice) release(idle bool) (line, kind string, n int, ok bool) {
	if g.outstanding || g.waiting == 0 {
		return "", "", 0, false
	}
	n = g.waiting
	g.waiting = 0
	g.outstanding = true
	if idle {
		kind = "wake"
		g.sawBusy = false
	} else {
		kind = "turn"
		g.sawBusy = true
	}
	return grokNoticeLine(n), kind, n, true
}

// undo puts a failed write's count back and clears the outstanding bit.
func (g *grokNotice) undo(n int) {
	g.outstanding = false
	g.sawBusy = false
	if n > 0 {
		g.waiting += n
	}
}

func grokNoticeLine(n int) string {
	if n == 1 {
		return "1 more message; read the bus"
	}
	return fmt.Sprintf("%d more messages; read the bus", n)
}

// grokRoute is the installed harness: the home the hooks live in, the wake
// file one monitor tails, and the notice those tails share.
type grokRoute struct {
	home, wake, as string
	role           hook.Role
	note           grokNotice
}

// grokConfig resolves --harness grok before any hook record is written.
// An empty harness, and every harness this row does not own, returns nil.
func (w world) grokConfig(c *tool.Call) (*grokRoute, error) {
	if !strings.EqualFold(c.Str("harness"), hookHarnessGrok) {
		return nil, nil
	}
	if c.Str("codex-socket") != "" || c.Str("codex-thread") != "" || c.Str("codex-events-dir") != "" || c.Str("codex-since") != "" {
		return nil, errors.New("one harness at a time: --harness grok or the Codex desktop route, not both")
	}
	if c.Str("muse-bundle") != "" || c.Str("muse-bin") != "" {
		return nil, errors.New("one harness at a time: --harness grok or --harness muse, not both")
	}
	home, err := grokHome(c.Str("grok-home"), w.getenv)
	if err != nil {
		return nil, err
	}
	return &grokRoute{home: home, as: c.Str("as")}, nil
}

// grokHome is --grok-home, else GROK_HOME, else the grok directory under the
// user home. The path is absolute and clean. A line break is refused because
// the installed command is one line; other characters stay, and grokCommand
// quotes each argument.
func grokHome(flag string, getenv func(string) string) (string, error) {
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

// grokShellQuote quotes one argument for sh. A single quote inside the
// argument ends the quote, adds a quoted quote, and opens it again.
func grokShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// grokHookScript records the latest word in state and appends one turn mark
// on busy. An idle write replaces state and leaves the turn mark. It always
// exits 0, and it never sends or acks.
const grokHookScript = "#!/bin/sh\n" +
	"# Records the latest word in state. On busy it appends one turn mark first,\n" +
	"# so a later idle write of state leaves the witness in place.\n" +
	"# It never sends or acks.\n" +
	"word=$1\n" +
	"dir=$2\n" +
	"case $word in\n" +
	"busy|idle) ;;\n" +
	"*) exit 0 ;;\n" +
	"esac\n" +
	"mkdir -p \"$dir\" || exit 0\n" +
	"if [ \"$word\" = busy ]; then\n" +
	"  printf 'x\\n' >> \"$dir/turn\" || exit 0\n" +
	"fi\n" +
	"tmp=$dir/.state.$$\n" +
	"printf '%s\\n' \"$word\" > \"$tmp\" || exit 0\n" +
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
	cmd := grokShellQuote(script) + " " + grokShellQuote(word) + " " + grokShellQuote(stateDir)
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

// installGrokHarness writes the hook files and creates the wake file when it
// is absent. An existing wake file is left byte for byte.
func installGrokHarness(home string) (string, error) {
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

func (w world) installGrok(c *tool.Call, g *grokRoute) *tool.Out {
	wake, err := installGrokHarness(g.home)
	if err != nil {
		o := tool.Refuse("grok harness could not be installed: " + err.Error())
		o.Remedy = grokRemedy(g)
		return o
	}
	g.wake = wake
	line(c, tool.Done().As("STATE").Fact("state", "grok-installed").Fact("wake", wake).Note("monitor `tail -n 0 -F "+wake+"`"))
	return nil
}

// readGrokSample is the latest state word and the count of busy marks in the
// turn file. A missing state file is idle. The turn file survives an idle
// write that replaces the state word (tla/GrokHookNotice.tla, MarkIdle).
func readGrokSample(home string) (idle bool, gen int, err error) {
	dir := filepath.Join(home, "nova-hook")
	b, err := os.ReadFile(filepath.Join(dir, "state"))
	idle = true
	if err == nil {
		idle = strings.TrimSpace(string(b)) != "busy"
	} else if !os.IsNotExist(err) {
		return true, 0, err
	}
	tb, err := os.ReadFile(filepath.Join(dir, "turn"))
	if os.IsNotExist(err) {
		return idle, 0, nil
	}
	if err != nil {
		return true, 0, err
	}
	return idle, bytes.Count(tb, []byte("\n")), nil
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

// grokFlush applies the turn witness and the latest word, then releases at
// most one line. A failed write puts the count back and stops the loop. It
// does not drop the hook record.
func (w world) grokFlush(c *tool.Call) *tool.Out {
	g := w.grok
	idle, gen, err := readGrokSample(g.home)
	if err != nil {
		o := tool.Fail("grok harness state could not be read: " + err.Error())
		o.Remedy = grokRemedy(g)
		return o
	}
	g.note.observe(idle, gen)
	text, kind, n, ok := g.note.release(idle)
	if !ok {
		return nil
	}
	if err := deliverGrokWake(g.wake, text); err != nil {
		g.note.undo(n)
		o := tool.Fail("grok wake file could not be written: " + err.Error())
		o.Remedy = grokRemedy(g)
		return o
	}
	line(c, tool.Done().As("PUSH").Fact("push", "grok").Fact("kind", kind).Fact("notice", text).Note("one notice; the model reads the bus"))
	return nil
}

func grokRemedy(g *grokRoute) string {
	role := string(g.role)
	if role == "" {
		role = "<role>"
	}
	as := g.as
	if as == "" {
		as = "<me>"
	}
	return "nova-hook hook --as " + as + " --role " + role + " --harness grok --grok-home " + g.home
}
