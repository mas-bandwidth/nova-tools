package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
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
	line(c, tool.Done().As("STATE").Fact("state", "muse-installed").Fact("plugin", "nova-hook").Fact("capability", museApproveTarget).Note(museWakeNote))
	return nil
}

// pendingBound bounds one peek from the hook: the bundle allows 3000ms, so
// the child is cut short of that and a slow bus can never hold the turn.
const pendingBound = 2500 * time.Millisecond

// pending is the bundle's PostToolUse hook as a Go verb (no shell ships): it
// discards stdin, runs `nova-bus peek` for the name and prints only its
// PEEK MESSAGE lines, one per line, so a HOOK CHALLENGE reaches the model
// inside its turn. It only reads: it never proves, sends, acks or writes,
// and it always exits 0 — a missing or failing bus prints nothing.
func (w world) pending(c *tool.Call) *tool.Out {
	if c.Stdin != nil {
		_, _ = io.Copy(io.Discard, c.Stdin) // ignored: the hook input is discarded unread; a failed drain changes nothing the hook prints
	}
	bus := c.Str("bus")
	if bus == "" {
		found, err := exec.LookPath("nova-bus")
		if err == nil {
			bus = found
		} else if home := w.getenv("HOME"); home != "" {
			candidate := filepath.Join(home, ".local", "bin", "nova-bus")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				bus = candidate
			}
		}
	}
	if bus == "" {
		return tool.Exit(0)
	}
	name := c.Str("as")
	if name == "" {
		name = "aurora"
	}
	redis := c.Str("redis")
	if redis == "" {
		redis = "127.0.0.1:6381"
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd, cancel := subproc.CommandFor(ctx, pendingBound, bus, "peek", "--as", name, "--redis", redis)
	defer cancel()
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return tool.Exit(0)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "PEEK MESSAGE") {
			fmt.Fprintln(c.Stdout, line)
		}
	}
	return tool.Exit(0)
}
