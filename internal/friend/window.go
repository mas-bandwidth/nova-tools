package friend

import (
	"context"
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
type WindowRefused struct{ Reason, Remedy string }

func (w WindowRefused) Error() string {
	reason := w.Reason
	if reason == "" {
		reason = "accessibility permission absent"
	}
	if w.Remedy == "" {
		return reason
	}
	return reason + ": " + w.Remedy
}

// ComposerRemedy names the supported delivery route when a GUI target cannot
// be established. Accessibility trust alone never establishes the composer.
const ComposerRemedy = "use a verified session delivery route or --harness tmux; this GUI adapter cannot verify the friend's composer"

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

// GUIWindow checks permission and refuses delivery without a verified composer.
// No harness-specific composer targeting contract is available here.
type GUIWindow struct {
	Bundle    string
	Run       Exec
	Permitted func(ctx context.Context) (bool, error)
}

// Deliver fails closed (docs/SPEC-FRIEND.md, Reach). Accessibility permission
// is necessary but cannot identify a session or prove its composer has focus.
func (g GUIWindow) Deliver(ctx context.Context, _ string) error {
	ok, err := g.trusted(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return WindowRefused{Remedy: AccessibilityRemedy}
	}
	return WindowRefused{Reason: "GUI composer is not verified", Remedy: ComposerRemedy}
}

func (g GUIWindow) trusted(ctx context.Context) (bool, error) {
	if g.Permitted != nil {
		return g.Permitted(ctx)
	}
	return PlatformPermitted(ctx, g.Run)
}

// accessibilityAsks reports a script that would prompt for the permission.
func accessibilityAsks(script string) bool {
	if strings.Contains(script, "AXIsProcessTrustedWithOptions") {
		return true
	}
	return strings.Contains(strings.ToLower(script), "with prompt")
}
