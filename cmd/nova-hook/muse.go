package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// museApproveTarget is the one hook capability the bundle declares, in the
// Muse CLI's approve spelling (plugin-id:kind:capability-id).
const museApproveTarget = "nova-hook:hook:pending-challenges"

// museWakeNote is what --harness Muse can say about waking: wake_delay is a
// per-Monitor setting with no persistent store, so nothing else is applied.
const museWakeNote = "wake_delay has no persistent store: pass wake_delay_ms=0 on each Monitor call for this friend"

type museRoute struct {
	bundle, bin string
}

// museConfig resolves the Muse install before any hook record or lease is
// written, mirroring codexConfig: an unknown harness, a missing bundle or a
// Codex route at the same time is refused up front.
func (w world) museConfig(c *tool.Call) (*museRoute, error) {
	harness := c.Str("harness")
	if harness == "" {
		return nil, nil
	}
	if !strings.EqualFold(harness, "Muse") {
		return nil, errors.New("--harness wants muse (the Codex desktop route uses the --codex-* flags)")
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
		return nil, errors.New("Muse install wants the plugin bundle: pass --muse-bundle <dir> or set NOVA_HOOK_MUSE_BUNDLE")
	}
	if _, err := os.Stat(filepath.Join(bundle, ".muse-plugin", "plugin.json")); err != nil {
		return nil, errors.New("Muse bundle " + bundle + " has no .muse-plugin/plugin.json")
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
		cmd := exec.CommandContext(ctx, r.bin, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			o := tool.Refuse("Muse " + args[0] + " " + args[1] + " failed: " + err.Error())
			if len(out) > 0 {
				o.Note(string(out))
			}
			o.Remedy = "run muse plugins validate " + r.bundle + " --json, fix the bundle, and hook in again"
			return o
		}
	}
	line(c, tool.Done().As("STATE").Fact("state", "muse-installed").Fact("plugin", "nova-hook").Fact("capability", museApproveTarget).Note(museWakeNote))
	return nil
}
