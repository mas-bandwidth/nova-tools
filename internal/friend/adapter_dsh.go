package friend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DSHProgram is where the DeepSeek Harness desktop app ships its CLI on this
// platform; the app installs no link on PATH.
const DSHProgram = "/Applications/DeepSeek Harness.app/Contents/Resources/runtime/cli/bin/dsh"

// DSH delivers through `dsh headless --session-id <id> -` run in Dir, the
// text on stdin: the headless profile adopts the persisted session (the same
// session directory under ~/.dsh/sessions/<key>/<id>, its record grows by one
// turn; measured 2026-10-04, v0.2.0-rc.2) and exits when the turn ends. An
// unknown id, a session recorded in another directory, and a missing
// provider key each exit 1. Without a session named, the newest session of
// Dir from the store (DSH_HOME, else ~/.dsh). The desktop app the friend
// sits in shares the store. Measured 2026-10-04 and 2026-10-05 (docs/SPEC-FRIEND.md,
// the dsh row): a session under an agent preset is refused by the one-shot
// runner whatever the text, exit 1 before any write, its transcript hash
// unchanged (the runner adopts only a session with no preset, and a session
// never returns to none): that delivery is Deferred, so the message stays
// pending instead of being given up after three refusals. On the survey machine,
// deliver.log records 1339+ deferred deliveries against Zhi's real open session,
// and the desktop app exposes no local listener or IPC socket. No route into
// the open desktop session exists, so Route answers defer; the session reads the
// bus itself with nova-bus wait or nova-bus recv.
type DSH struct {
	Dir, Session string
	Run          Exec
	Program      string    // DSHProgram when empty
	Sessions     string    // the sessions root; DSH_HOME/sessions, else ~/.dsh/sessions, when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record
}

// DSHArgs is the argument list of one delivery; the text travels on stdin
// ("-"), so it is never parsed as an argument.
func DSHArgs(session string) []string {
	return []string{"headless", "--session-id", session, "-"}
}

// DSHSessionKey is the store's directory for the sessions of dir: the path
// with every separator a dash, one more dash in front and two behind
// (measured 2026-10-04 on the store: /Volumes/nova/ai/zhi is
// --Volumes-nova-ai-zhi--).
func DSHSessionKey(dir string) string {
	return "-" + strings.ReplaceAll(dir, "/", "-") + "--"
}

// NewestDSHSession is the most recently modified session of dir under the
// sessions root: the directory names are the session ids.
func NewestDSHSession(sessions, dir string) (string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	entries, err := os.ReadDir(filepath.Join(sessions, DSHSessionKey(realDir)))
	if err != nil {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	var best string
	var bestAt time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || !strings.HasPrefix(e.Name(), "session-") {
			continue
		}
		if best == "" || info.ModTime().After(bestAt) {
			best, bestAt = e.Name(), info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	return best, nil
}

func (d *DSH) Deliver(ctx context.Context, text string) (int, error) {
	id := d.Session
	if id == "" {
		root := d.Sessions
		if root == "" {
			root = dshHome()
		}
		var err error
		if id, err = NewestDSHSession(root, d.Dir); err != nil {
			return 0, err
		}
	}
	program := d.Program
	if program == "" {
		program = DSHProgram
	}
	out, exit, err := d.Run(ctx, d.Dir, program, DSHArgs(id), text)
	if m := dshPresetRefusal.FindStringSubmatch(out); exit != 0 && err == nil && m != nil {
		return 0, Deferred{Reason: fmt.Sprintf("session %s runs under agent preset %q, which dsh's headless runner does not compose (it adopts only a session with no agent preset); the message stays pending: start a session in %s without an agent preset and name it with --session, or read the bus with nova-bus recv", id, m[1], d.Dir)}
	}
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	return refused(id, out, exit, err)
}

// Route is what status says: always defer. No live push into the open
// desktop session was found (docs/SPEC-FRIEND.md, the dsh row), and a
// headless turn is a separate process, not the friend's open chat. line is
// what the session runs itself: a blocking read of the bus.
func (d *DSH) Route(ctx context.Context) (route, line string, err error) {
	return "defer", "nova-bus wait --as <friend>", nil
}

// dshPresetRefusal is the one-shot runner's refusal of a session under an
// agent preset, the preset its group.
var dshPresetRefusal = regexp.MustCompile(`runs under agent preset "([^"]*)", which the one-shot runner does not compose`)

func dshHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return filepath.Join(h, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh", "sessions")
}

// dshPresetNone is the agent preset install writes. dsh headless refuses a
// session under any preset, and "minimal" in particular, before any write
// (docs/SPEC-FRIEND.md, the dsh row). An empty default is no preset, so a
// new session is one the runner can adopt.
const dshPresetNone = ""

// planDSH sets agent-presets.default in <DSH_HOME|home/.dsh>/settings.yaml
// to no preset. Other keys are kept. A symlinked .dsh directory is refused.
func planDSH(p *Prepared) error {
	home := p.settings.DSHHome
	if home == "" {
		home = filepath.Join(p.settings.Home, ".dsh")
	}
	if err := stageDir(p, home); err != nil {
		return fmt.Errorf("dsh: %w", err)
	}
	path := filepath.Join(home, "settings.yaml")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next, installed, present, err := dshPresetText(string(raw))
	if err != nil {
		return fmt.Errorf("dsh: %s: %w", path, err)
	}
	if !present || installed != dshPresetNone {
		shown := "missing"
		if present {
			shown = strconv.Quote(installed)
		}
		p.drifts = append(p.drifts, fmt.Sprintf("dsh agent_preset: installed %s, want %s", shown, strconv.Quote(dshPresetNone)))
	}
	p.Files[path] = next
	return nil
}

// dshPresetText is settings.yaml with agent-presets.default set to no preset.
// The current default is installed; present is false when the key is absent.
func dshPresetText(current string) (next, installed string, present bool, err error) {
	if strings.TrimSpace(current) == "" {
		return "agent-presets:\n  default: \"\"\n", "", false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(current), &doc); err != nil {
		return "", "", false, fmt.Errorf("settings.yaml is not YAML: %w", err)
	}
	root := yamlMapping(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return "", "", false, fmt.Errorf("settings.yaml is not a YAML map")
	}
	presets := yamlMapGet(root, "agent-presets")
	if presets == nil {
		presets = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "agent-presets"},
			presets)
	}
	if presets.Kind != yaml.MappingNode {
		return "", "", false, fmt.Errorf("settings.yaml agent-presets is not a map")
	}
	def := yamlMapGet(presets, "default")
	if def != nil {
		present = true
		if def.Tag == "!!null" {
			installed = ""
		} else {
			installed = def.Value
		}
	}
	if present && installed == dshPresetNone && def.Tag != "!!null" {
		return current, installed, true, nil
	}
	if def == nil {
		presets.Content = append(presets.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "default"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: dshPresetNone, Style: yaml.DoubleQuotedStyle})
	} else {
		def.Kind = yaml.ScalarNode
		def.Tag = "!!str"
		def.Value = dshPresetNone
		def.Style = yaml.DoubleQuotedStyle
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", "", false, err
	}
	if err := enc.Close(); err != nil {
		return "", "", false, err
	}
	return buf.String(), installed, present, nil
}

func yamlMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

func yamlMapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
