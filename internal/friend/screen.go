package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// DefaultScreenLines is the default number of lines screen returns.
const DefaultScreenLines = 40

// Screen sources.
const (
	SourceTmux   = "tmux"
	SourceWindow = "window"
)

// ErrAccessibilityPermission is returned when the binary lacks macOS accessibility permission.
var ErrAccessibilityPermission = errors.New("accessibility permission not granted")

// ScreenRefused is returned when a screen cannot be captured and the verb should refuse (exit 1).
type ScreenRefused struct {
	Why    string
	Remedy string
}

func (s ScreenRefused) Error() string {
	if s.Remedy != "" {
		return s.Why + "; run: " + s.Remedy
	}
	return s.Why
}

// ScreenResult is the captured screen text of an open friend session.
type ScreenResult struct {
	Friend string    `json:"friend"`
	Source string    `json:"source"`
	Lines  []string  `json:"lines"`
	At     time.Time `json:"at"`
}

// MarshalJSON renders the JSON shape: {"friend":..,"source":..,"at":..,"lines":[..]}.
func (s ScreenResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Friend string   `json:"friend"`
		Source string   `json:"source"`
		At     string   `json:"at"`
		Lines  []string `json:"lines"`
	}{
		Friend: s.Friend,
		Source: s.Source,
		At:     s.At.UTC().Format(time.RFC3339),
		Lines:  s.Lines,
	})
}

// FormatText formats the output as text:
// SCREEN friend=<f> source=<tmux|window> lines=<n> at=<RFC3339>
//
// <text>
func (s ScreenResult) FormatText() string {
	head := fmt.Sprintf("SCREEN friend=%s source=%s lines=%d at=%s\n", s.Friend, s.Source, len(s.Lines), s.At.UTC().Format(time.RFC3339))
	return head + "\n" + strings.Join(s.Lines, "\n") + "\n"
}

// ExtractLastLines returns at most n lines from the end of text.
func ExtractLastLines(text string, n int) []string {
	if n <= 0 {
		return nil
	}
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return nil
	}
	all := strings.Split(text, "\n")
	for i := range all {
		all[i] = strings.TrimRight(all[i], "\r")
	}
	if len(all) > n {
		return all[len(all)-n:]
	}
	return all
}

// WindowReader reads the text of a GUI harness window through the accessibility API.
type WindowReader func(ctx context.Context, bundle, target string) (string, error)

// DefaultWindowReader runs osascript via the Exec seam.
func DefaultWindowReader(run Exec) WindowReader {
	return func(ctx context.Context, bundle, target string) (string, error) {
		if run == nil {
			return "", errors.New("no exec seam")
		}
		appName := strings.TrimSuffix(filepath.Base(bundle), ".app")
		script := fmt.Sprintf(`
tell application "System Events"
	set appName to "%s"
	if not (exists process appName) then
		error "process " & appName & " not running"
	end if
	tell process appName
		set targetWin to ""
		repeat with w in windows
			if name of w contains "%s" then
				set targetWin to w
				exit repeat
			end if
		end repeat
		if targetWin is "" then error "no matching window found"
		tell targetWin
			try
				set out to value of text area 1 of scroll area 1
				return out
			on error
				try
					set out to description of targetWin
					return out
				on error
					return name of targetWin
				end try
			end try
		end tell
	end tell
end tell`, appName, target)
		out, exit, err := run(ctx, "", "osascript", []string{"-e", script}, "")
		if err != nil || exit != 0 {
			combined := strings.ToLower(out)
			if err != nil {
				combined += " " + strings.ToLower(err.Error())
			}
			if strings.Contains(combined, "not allowed assistive access") ||
				strings.Contains(combined, "-1728") ||
				strings.Contains(combined, "-1719") ||
				strings.Contains(combined, "accessibility") {
				return "", ErrAccessibilityPermission
			}
			return "", fmt.Errorf("accessibility window read: %s", strings.TrimSpace(out))
		}
		return out, nil
	}
}

// ScreenOpts specifies the options for reading a friend's screen.
type ScreenOpts struct {
	Friend       string
	Lines        int
	StateDir     string
	Dir          string
	Home         string
	Binary       string
	Now          func() time.Time
	Exec         Exec
	WindowReader WindowReader
}

// Screen reads the last n lines of a friend's open session as text: from a
// hosted tmux pane or a GUI harness window through the accessibility seam.
func Screen(ctx context.Context, opts ScreenOpts) (ScreenResult, error) {
	if opts.Lines <= 0 {
		opts.Lines = DefaultScreenLines
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Exec == nil {
		opts.Exec = RealExec
	}
	if opts.StateDir == "" {
		opts.StateDir = FindStateDir(opts.Home, opts.Dir, opts.Friend)
	}

	h, foundHost, _ := ReadHost(opts.StateDir)
	s, foundStatus, _ := ReadStatus(opts.StateDir)

	tmuxSession := ""
	isTmux := false
	if foundHost && h.Session != "" {
		tmuxSession = h.Session
		isTmux = true
	} else if foundStatus && s.Harness == "tmux" {
		tmuxSession = TmuxSession(opts.Friend)
		isTmux = true
	} else if !foundHost && !foundStatus {
		if _, exit, err := opts.Exec(ctx, opts.Dir, "tmux", []string{"has-session", "-t", TmuxSession(opts.Friend)}, ""); err == nil && exit == 0 {
			tmuxSession = TmuxSession(opts.Friend)
			isTmux = true
		}
	}

	if isTmux {
		out, exit, err := opts.Exec(ctx, opts.Dir, "tmux", []string{"capture-pane", "-p", "-t", tmuxSession}, "")
		if err != nil || exit != 0 {
			low := strings.ToLower(out)
			if strings.Contains(low, "can't find") || strings.Contains(low, "no server") || strings.Contains(low, "no sessions") || strings.Contains(low, "error connecting") || (err != nil && strings.Contains(strings.ToLower(err.Error()), "no tmux session")) {
				return ScreenResult{}, ScreenRefused{
					Why:    fmt.Sprintf("the tmux session %s is not running", tmuxSession),
					Remedy: fmt.Sprintf("nova-friend host --as %s --harness <h> --dir <d> -- <launch command...>", opts.Friend),
				}
			}
			return ScreenResult{}, fmt.Errorf("tmux capture-pane exited %d: %s", exit, strings.TrimSpace(Head(out, 200)))
		}
		lines := ExtractLastLines(out, opts.Lines)
		return ScreenResult{
			Friend: opts.Friend,
			Source: SourceTmux,
			Lines:  lines,
			At:     opts.Now(),
		}, nil
	}

	harness := ""
	if foundStatus && s.Harness != "" {
		harness = s.Harness
	} else if foundHost && h.Harness != "" {
		harness = h.Harness
	}

	isGUI := IsGUI(harness)
	if !isGUI {
		if harness != "" {
			return ScreenResult{}, ScreenRefused{Why: fmt.Sprintf("%s has no screen: it is neither hosted in tmux nor a GUI harness", harness)}
		}
		return ScreenResult{}, ScreenRefused{Why: fmt.Sprintf("%s has no screen: neither hosted in tmux nor a GUI harness", opts.Friend)}
	}

	reader := opts.WindowReader
	if reader == nil {
		reader = DefaultWindowReader(opts.Exec)
	}

	target := opts.Dir
	if target == "" && foundStatus {
		target = s.SessionLive
	}
	if target == "" {
		return ScreenResult{}, ScreenRefused{Why: fmt.Sprintf("%s has no directory or session title to identify its window", opts.Friend)}
	}

	bundle := AntigravityApp.Bundle
	text, err := reader(ctx, bundle, target)
	if err != nil {
		if errors.Is(err, ErrAccessibilityPermission) || strings.Contains(strings.ToLower(err.Error()), "accessibility") || strings.Contains(strings.ToLower(err.Error()), "assistive access") {
			remedy := "grant accessibility permission in System Settings > Privacy & Security > Accessibility"
			if opts.Binary != "" {
				remedy = fmt.Sprintf("grant accessibility permission to %s in System Settings > Privacy & Security > Accessibility", opts.Binary)
			}
			return ScreenResult{}, ScreenRefused{
				Why:    "accessibility permission not granted",
				Remedy: remedy,
			}
		}
		return ScreenResult{}, ScreenRefused{Why: err.Error()}
	}
	if strings.TrimSpace(text) == "" {
		return ScreenResult{}, ScreenRefused{Why: fmt.Sprintf("the matching %s window has no readable text", opts.Friend)}
	}

	lines := ExtractLastLines(text, opts.Lines)
	return ScreenResult{
		Friend: opts.Friend,
		Source: SourceWindow,
		Lines:  lines,
		At:     opts.Now(),
	}, nil
}
