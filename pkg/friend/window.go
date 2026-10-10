package friend

import (
	"context"
	"fmt"
	"strings"
)

// AccessibilityRemedy is what a person does when the window step cannot type.
// The permission is granted to this binary. The tool never asks for it.
const AccessibilityRemedy = "grant Accessibility to this binary in System Settings, Privacy and Security, Accessibility; nova-friend does not ask"

// AccessibilityCheckScript asks whether this process is trusted. It calls
// AXIsProcessTrusted and never AXIsProcessTrustedWithOptions, so it cannot prompt.
const AccessibilityCheckScript = "use framework \"ApplicationServices\"\nreturn (current application's AXIsProcessTrusted() as boolean) as text\n"

// WindowRefused is the window step's answer when the accessibility permission
// is absent (docs/SPEC-FRIEND.md, Reach). Remedy is what a person grants.
type WindowRefused struct{ Remedy string }

func (w WindowRefused) Error() string {
	if w.Remedy == "" {
		return "accessibility permission absent"
	}
	return "accessibility permission absent: " + w.Remedy
}

// AppBundles is the bundle identifier the window step looks up for a GUI
// harness. These are lookup ids, not a measured survey of installed apps.
// A harness with no entry, and not tmux, has no window.
var AppBundles = map[string]string{
	"claude":      "com.anthropic.claudefordesktop",
	"codex":       "com.openai.codex",
	"cursor":      "com.todesktop.230313mzl4w4u92",
	"antigravity": "com.google.antigravity",
	"windsurf":    "com.exafunction.windsurf",
	"zed":         "dev.zed.Zed",
	"warp":        "dev.warp.Warp-Stable",
}

// GUIWindow types one message into a GUI harness's composer and submits it.
// Permitted, when set, answers whether the accessibility permission is held.
// Nil asks the platform (PlatformPermitted), which never prompts.
type GUIWindow struct {
	Bundle    string
	Run       Exec
	Permitted func(ctx context.Context) (bool, error)
}

// Deliver types text once the permission is held (docs/SPEC-FRIEND.md, Reach;
// tla/Reach.tla, the window step). Absent permission is WindowRefused. The
// tool does not ask.
func (g GUIWindow) Deliver(ctx context.Context, text string) error {
	ok, err := g.trusted(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return WindowRefused{Remedy: AccessibilityRemedy}
	}
	if g.Bundle == "" {
		return fmt.Errorf("has no window")
	}
	script := TypeScript(g.Bundle, text)
	if accessibilityAsks(script) {
		return fmt.Errorf("the type script asks for accessibility permission")
	}
	if g.Run == nil {
		return fmt.Errorf("no command runner")
	}
	out, exit, err := g.Run(ctx, "", "osascript", []string{"-e", script}, "")
	if err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("osascript exited %d: %s", exit, strings.TrimSpace(out))
	}
	return nil
}

func (g GUIWindow) trusted(ctx context.Context) (bool, error) {
	if g.Permitted != nil {
		return g.Permitted(ctx)
	}
	return PlatformPermitted(ctx, g.Run)
}

// TypeScript is the osascript that brings bundle frontmost, types text and
// submits it with Return (key code 36). It never asks for permission.
func TypeScript(bundle, text string) string {
	return "tell application \"System Events\"\n" +
		"set frontmost of first process whose bundle identifier is \"" + appleString(bundle) + "\" to true\n" +
		"keystroke \"" + appleString(TypedLine(text)) + "\"\n" +
		"key code 36\n" +
		"end tell\n"
}

func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// accessibilityAsks reports a script that would prompt for the permission.
func accessibilityAsks(script string) bool {
	if strings.Contains(script, "AXIsProcessTrustedWithOptions") {
		return true
	}
	return strings.Contains(strings.ToLower(script), "with prompt")
}
