package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Adapters is the per-friend wake config: which harness window a friend's
// session lives in. One today.
var Adapters = map[string]func() Waker{
	"stella": func() Waker {
		return &CodexWindow{Bundle: "com.openai.codex", Window: "ChatGPT", run: runOsascript}
	},
}

// CodexWindow wakes a session in a macOS app window by UI scripting over the
// Accessibility API (System Events, run as an osascript subprocess with the
// script embedded below). It brings nothing to the front: the line is set as
// the chat input's value and sent with the window's Send button (AXPress);
// only when the app is already frontmost is return pressed instead.
type CodexWindow struct {
	Bundle, Window string
	run            func(ctx context.Context, args ...string) (stdout, stderr string, err error)
}

func (c *CodexWindow) Via() string { return "codex-window" }

func (c *CodexWindow) Wake(ctx context.Context, line string) error {
	_, stderr, err := c.run(ctx, "-e", wakeScript, line, c.Bundle, c.Window)
	if err == nil {
		return nil
	}
	if g := grantNeeded(stderr); g != nil {
		return g
	}
	if ctx.Err() != nil {
		return fmt.Errorf("timed out: %w", ctx.Err())
	}
	return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr))
}

var assistiveRe = regexp.MustCompile(`([^:]+?) is not allowed assistive access`)

// grantNeeded reads osascript's refusal of UI scripting, nil when it is none.
func grantNeeded(stderr string) *GrantNeeded {
	detail := strings.TrimSpace(stderr)
	if m := assistiveRe.FindStringSubmatch(stderr); m != nil {
		proc := strings.TrimSpace(m[1])
		if i := strings.LastIndex(proc, "error: "); i >= 0 {
			proc = proc[i+len("error: "):]
		}
		proc = strings.TrimPrefix(proc, "System Events got an error: ")
		return &GrantNeeded{Process: grantee(proc), Detail: detail}
	}
	if strings.Contains(stderr, "(-1719)") || strings.Contains(stderr, "(-25211)") {
		return &GrantNeeded{Process: grantee(""), Detail: detail}
	}
	return nil
}

// grantee names who needs the grant: the app macOS named as responsible (the
// app that launched this binary), and this binary, which needs it when it runs
// under launchd.
func grantee(proc string) string {
	self, _ := os.Executable() // ignored: the name is advice; an empty path still names the app
	if proc == "" {
		return fmt.Sprintf("the app that runs %s (osascript's responsible process)", self)
	}
	return fmt.Sprintf("%q (the app that runs %s; under launchd, %s itself)", proc, self, self)
}

func runOsascript(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.String(), errb.String(), ctx.Err()
	}
	return out.String(), errb.String(), err
}

// wakeScript: argv is {line, bundle id, window name}.
const wakeScript = `on run argv
	set theLine to item 1 of argv
	set bundleID to item 2 of argv
	set winName to item 3 of argv
	tell application "System Events"
		set procs to (every process whose bundle identifier is bundleID)
		if procs is {} then error "not running: " & bundleID
		set p to item 1 of procs
		tell p
			set ws to (every window whose name is winName)
			if ws is {} then set ws to every window
			if ws is {} then error "no window"
			set w to item 1 of ws
			set ta to missing value
			set sendBtn to missing value
			repeat with e in (entire contents of w)
				try
					set r to role of e
					if r is "AXTextArea" then set ta to e
					if r is "AXButton" then
						set d to ""
						try
							set d to (description of e) as text
						end try
						if d is "" then
							try
								set d to (name of e) as text
							end try
						end if
						if d starts with "Send" then set sendBtn to e
					end if
				end try
			end repeat
			if ta is missing value then error "no chat input"
			set value of ta to theLine
			if frontmost then
				set focused of ta to true
				key code 36
			else if sendBtn is not missing value then
				perform action "AXPress" of sendBtn
			else
				perform action "AXConfirm" of ta
			end if
		end tell
	end tell
	return "ok"
end run`
