package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// acceptance is the acceptance run verb (docs/SPEC-FRIEND.md, buds-in-the-wall-r.w5;
// docs/SPEC-CARD-CONTRACT.md, the friend-daemon step): the fleet play runs it once per
// friend placed on the host, and it runs one probe command inside the same lane wall a
// lane's child runs inside — so a profile that will not build (125) or a command that
// cannot execute (126) comes back as the real exit, and the play fails the host with the
// friend, the exit code and the log line.
func (w world) acceptance(c *tool.Call) *tool.Out {
	name, dir := c.Str("as"), c.Str("dir")
	if name == "" {
		return tool.Refuse("--as wants the friend's name; run: nova-friend acceptance --as <friend> --dir <working_dir>")
	}
	if dir == "" {
		return tool.Refuse("--dir wants the friend's working directory; run: nova-friend acceptance --as <friend> --dir <working_dir>")
	}
	abs, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(abs) {
		return tool.Refuse(fmt.Sprintf("lane path not absolute: %s: %v; the daemon does not start", dir, err))
	}
	dir = abs

	// The acceptance run is a lane's own run: the same wall every lane child runs
	// inside (friend.Wall), over the friend's working directory. No wall is built by
	// a world that is not the real one, and a lane child never runs outside it.
	if w.wall == nil {
		return tool.Refuse("the lane wall is not built; run this on the host, not in a test: a lane child never runs outside its wall")
	}
	wl := friend.Wall{Dir: dir, Profile: sandbox.ProfileFriend, ConfigDir: w.getenv("CLAUDE_CONFIG_DIR")}
	if bin, err := w.binary(); err == nil {
		wl.Self = []string{bin}
	}
	// The wall denies the coordinator's self, at least one path (sandbox.LaneProfile):
	// her name comes from the server, else the probe dir names a stand-in so the wall
	// still denies a write outside the lane's own directory.
	if wl.Deny = friend.DefaultDenySelf(w.home, ""); len(wl.Deny) == 0 {
		wl.Deny = []string{filepath.Join(dir, ".acceptance-self")}
	}

	// The probe: one command inside the wall that writes RESULT.md under the friend's
	// working directory — the lane's own write — and exits with the wall's real code.
	resultPath := filepath.Join(dir, "RESULT.md")
	probe := fmt.Sprintf("printf 'ACCEPTANCE OK\\n' > %s", shellQuote(resultPath))
	ctx := friend.LaneContext(context.Background())
	out, code, runErr := w.wall(wl, w.exec)(ctx, dir, "/bin/sh", []string{"-c", probe}, "")
	if runErr != nil {
		return tool.Refuse(fmt.Sprintf("--dir cannot be run inside the wall: %s: %v", dir, runErr))
	}
	if code != 0 {
		return tool.Exit(code).Note(fmt.Sprintf("friend=%s exit=%d %s", name, code, probeLine(out)))
	}
	if _, err := os.Stat(resultPath); err != nil {
		return tool.Refuse(fmt.Sprintf("the probe wrote no RESULT.md: %s: %v", resultPath, err))
	}
	return tool.Done().Note(fmt.Sprintf("friend=%s exit=0 %s", name, resultPath))
}

// shellQuote wraps s in single quotes so a path a friend names reaches the probe's
// shell as one literal argument and never as a command (docs/SPEC-FRIEND.md, queue
// acceptance: the text is a literal argument, never a shell command).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// probeLine is the probe's own output as one line, capped: the play's assert prints it
// beside the friend and the exit code (docs/SPEC-FRIEND.md, the friend-daemon step).
func probeLine(out string) string {
	line := strings.Join(strings.Fields(out), " ")
	if len(line) > 400 {
		line = line[:400] + "..."
	}
	return line
}
