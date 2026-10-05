package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoAccessibility is the window step's refusal when this binary may not
// drive the GUI. The tool never asks for the permission; a person grants it.
var ErrNoAccessibility = errors.New("accessibility permission is not granted to this binary")

// WindowSkip is a window step that cannot be taken. The ladder records the
// reason and climbs. It is not a refusal.
type WindowSkip struct{ Reason string }

func (s WindowSkip) Error() string { return "window skipped: " + s.Reason }

// GUIBundles is the harness whose window is a GUI app, and that app's bundle.
// antigravity is the public Antigravity IDE cask. A harness not listed is a
// TUI and is reached through tmux. A bundle that is not published is not invented.
var GUIBundles = map[string]string{
	"antigravity": "com.google.antigravity-ide",
}

// axTrusted reports whether this process may use the accessibility API.
// The darwin build asks the system and never prompts. The other build
// refuses, so a test never types into a real window.
var axTrusted = defaultAXTrusted

// WindowTarget is one friend's window: a tmux pane, or a GUI app by bundle.
type WindowTarget struct {
	Kind   string // "tmux" or "gui"
	Pane   string
	Bundle string
}

// FindWindow locates the friend's window. A GUI harness is refused, with no
// command run, when the accessibility permission is absent. A TUI is the tmux
// pane whose session or command is the friend or the harness, and only when
// that pane is idle (pane_in_mode is 0). A pane in a mode is not typed into.
func FindWindow(ctx context.Context, run Exec, harness, name string) (WindowTarget, error) {
	if bundle, ok := GUIBundles[harness]; ok {
		if !axTrusted() {
			return WindowTarget{}, ErrNoAccessibility
		}
		return WindowTarget{Kind: "gui", Bundle: bundle}, nil
	}
	if run == nil {
		return WindowTarget{}, WindowSkip{Reason: "no-tmux-pane"}
	}
	out, code, err := run(ctx, "", "tmux", []string{"list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{pane_current_command}"}, "")
	if err != nil || code != 0 {
		return WindowTarget{}, WindowSkip{Reason: "no-tmux-pane"}
	}
	pane := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		if f[1] == name || f[2] == name || f[2] == harness {
			pane = f[0]
			break
		}
	}
	if pane == "" {
		return WindowTarget{}, WindowSkip{Reason: "no-tmux-pane"}
	}
	mode, code, err := run(ctx, "", "tmux", []string{"display-message", "-p", "-t", pane, "#{pane_in_mode}"}, "")
	if err != nil || code != 0 || strings.TrimSpace(mode) != "0" {
		return WindowTarget{}, WindowSkip{Reason: "pane-not-idle"}
	}
	return WindowTarget{Kind: "tmux", Pane: pane}, nil
}

// SubmitWindow delivers text into the target. A tmux pane gets send-keys -l
// and then Enter. A GUI app gets the text in its composer and a Return.
func SubmitWindow(ctx context.Context, run Exec, target WindowTarget, text string) error {
	switch target.Kind {
	case "tmux":
		if run == nil {
			return WindowSkip{Reason: "no-tmux-pane"}
		}
		if _, code, err := run(ctx, "", "tmux", []string{"send-keys", "-t", target.Pane, "-l", "--", text}, ""); err != nil || code != 0 {
			return fmt.Errorf("tmux send-keys exited %d: %v", code, err)
		}
		if _, code, err := run(ctx, "", "tmux", []string{"send-keys", "-t", target.Pane, "Enter"}, ""); err != nil || code != 0 {
			return fmt.Errorf("tmux send-keys Enter exited %d: %v", code, err)
		}
		return nil
	case "gui":
		if !axTrusted() {
			return ErrNoAccessibility
		}
		return typeAndSubmit(target.Bundle, text)
	default:
		return WindowSkip{Reason: "no-gui-window"}
	}
}
