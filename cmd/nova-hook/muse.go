package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// museApproveTarget is the one hook capability the bundle declares, in the
// Muse CLI's approve spelling (plugin-id:kind:capability-id).
const museApproveTarget = "nova-hook:hook:pending-challenges"

// museWakeNote is what --harness Muse must say about waking. Muse has
// no persistent wake_delay store (evidence in the bundle README), so the
// adapter installs nothing for wake and instead spells out both Monitor
// calls the friend's session must start with wake_delay_ms=0.
func museWakeNote(as, role string) string {
	if as == "" {
		as = "<name>"
	}
	if role == "" {
		role = "<role>"
	}
	return "wake_delay_ms has no persistent store in Muse, so nothing is stored: start both Monitors with wake_delay_ms=0: " +
		"Monitor(command=\"nova-hook hook --as " + as + " --role " + role + " --harness Muse\", wake_delay_ms=0) and " +
		"Monitor(command=\"nova-bus recv --as " + as + " --forever --exec '<deliver-into-session>'\", wake_delay_ms=0)"
}

type museRoute struct {
	bundle, bin, notices string
}

// museNotices is the directory a Muse friend's one pending notice lives in:
// the hook writes it, and the PostToolUse hook reads it.
func museNotices(home, as string) string {
	return filepath.Join(home, ".muse", "nova-hook", as)
}

// museConfig resolves the Muse install before any hook record or lease is
// written, mirroring codexConfig: an unknown harness, a missing bundle or a
// Codex route at the same time is refused up front.
func (w world) museConfig(c *tool.Call) (*museRoute, error) {
	harness := strings.TrimSpace(c.Str("harness"))
	if harness == "" || strings.EqualFold(harness, "antigravity") || strings.EqualFold(harness, "codex") {
		return nil, nil
	}
	if !strings.EqualFold(harness, "Muse") {
		return nil, errors.New("--harness wants muse or antigravity (the Codex desktop route uses the --codex-* flags)")
	}
	bundle := c.Str("muse-bundle")
	if bundle == "" {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "muse-bundle")
			if _, err := os.Stat(filepath.Join(candidate, ".muse-plugin", "plugin.json")); err == nil {
				bundle = candidate
			}
		}
	}
	if bundle == "" {
		return nil, errors.New("muse install wants the plugin bundle: pass --muse-bundle <dir> or set NOVA_HOOK_MUSE_BUNDLE")
	}
	if _, err := os.Stat(filepath.Join(bundle, ".muse-plugin", "plugin.json")); err != nil {
		return nil, errors.New("muse bundle " + bundle + " has no .muse-plugin/plugin.json")
	}
	bin := c.Str("muse-bin")
	if bin == "" {
		bin = "muse"
	}
	return &museRoute{bundle: bundle, bin: bin}, nil
}

// museInstall runs the Muse CLI's install and approve for the bundle's one
// hook capability, then states the wake setting. A failure is a refusal
// before arming: nothing is hooked in on a half-installed route.
func (w world) museInstall(c *tool.Call, r *museRoute) *tool.Out {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, args := range [][]string{
		{"plugins", "install", r.bundle},
		{"plugins", "approve", museApproveTarget},
	} {
		cmd := subproc.Context(ctx, r.bin, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			o := tool.Refuse("Muse " + args[0] + " " + args[1] + " failed: " + err.Error())
			if len(out) > 0 {
				o.Note(string(out))
			}
			o.Remedy = "run muse plugins validate " + r.bundle + " --json, fix the bundle, and hook in again"
			return o
		}
	}
	home := w.getenv("HOME")
	if home == "" {
		return tool.Refuse("Muse delivery wants HOME for its notice directory")
	}
	notices, err := notice.MakeDir(museNotices(home, c.Str("as")))
	if err != nil {
		return tool.Refuse("Muse notice directory could not be made: " + err.Error())
	}
	r.notices = notices
	line(c, tool.Done().As("STATE").Fact("state", "muse-installed").Fact("plugin", "nova-hook").Fact("capability", museApproveTarget).Note(museWakeNote(c.Str("as"), c.Str("role"))))
	return nil
}

// pending is the bundle's PostToolUse hook as a Go verb: it discards stdin
// and prints the one pending notice line the hook left for this name, if
// any, so the newest challenge reaches the model inside its turn. It only
// reads: it never proves, sends, acks or writes, and it always exits 0.
func (w world) pending(c *tool.Call) *tool.Out {
	if c.Stdin != nil {
		_, _ = io.Copy(io.Discard, c.Stdin) // ignored: the hook input is discarded unread; a failed drain changes nothing the hook prints
	}
	name := c.Str("as")
	if name == "" {
		name = "aurora"
	}
	if home := w.getenv("HOME"); home != "" {
		fmt.Fprint(c.Stdout, notice.MusePending(museNotices(home, name)))
	}
	return tool.Exit(0)
}
