package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// acceptance is the acceptance run verb (docs/SPEC-FRIEND.md, the acceptance run; the wall
// is buds-in-the-wall-r.w5): the fleet play runs it once per friend placed on the host, and
// it runs the friend's own lane program inside the very wall the daemon's lane children run
// inside — the one laneWall assembles for run too — so a profile that will not build (125)
// or a program that cannot execute (126) comes back as the real exit, and the play fails the
// host with the friend, the exit code and the output line. No model is called: the program
// runs its version flag, and no card is taken (docs/SPEC-FRIEND.md, the acceptance run).
func (w world) acceptance(c *tool.Call) *tool.Out {
	name, dir := c.Str("as"), c.Str("dir")
	if name == "" {
		return tool.Refuse("--as wants the friend's name; run: nova-friend acceptance --as <friend> --dir <working_dir> --harness <h>")
	}
	if dir == "" {
		return tool.Refuse("--dir wants the friend's working directory; run: nova-friend acceptance --as <friend> --dir <working_dir> --harness <h>")
	}
	harness := c.Str("harness")
	if harness == "" {
		return tool.Refuse("--harness wants the friend's harness; run: nova-friend acceptance --as <friend> --dir <working_dir> --harness <h>")
	}
	abs, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(abs) {
		return tool.Refuse(fmt.Sprintf("lane path not absolute: %s: %v; the daemon does not start", dir, err))
	}
	dir = abs

	// The acceptance run is a lane's own run: the same wall the daemon's lane children run
	// inside, built by the one function run and acceptance share, from the same flags and
	// the friend's row. A lane child never runs outside it, and there is no wall of its own.
	if w.wall == nil {
		return tool.Refuse("the lane wall is not built; run this on the host, not in a test: a lane child never runs outside its wall")
	}
	wl := laneWall(c, w, dir,
		func() []string { return friend.DefaultDenySelf(w.home, c.Str("coordinator")) },
		func() string { return w.getenv("CLAUDE_CONFIG_DIR") },
		func() (string, bool) { return "", false }) // no row in an acceptance run: --profile stands

	// The lane's own program: the adapter's runner, found on PATH or under the home,
	// run with its no-cost version flag so no model is called.
	program, versionArg := runnerProgram(harness, w.home)
	if w.lookPath != nil {
		if _, err := w.lookPath(program); err != nil {
			// the note is printed here, not carried on tool.Exit: Exit's result is
			// `printed`, so the skeleton renders nothing and the play's fail_msg,
			// which reads stdout/stderr for the output line, would name no line.
			fmt.Fprintf(c.Stdout, "ACCEPTANCE NOTE friend=%s exit=126 the runner %s cannot execute: %v; run: nova-friend help\n", name, program, err)
			return tool.Exit(126)
		}
	}
	probeDir := filepath.Join(dir, ".acceptance")
	if c.DryRun() {
		// The plan from the same path the real run takes: the wall is assembled and the
		// runner found above; nothing is made, run or written.
		return tool.Done().Fact("friend", name).Fact("dir", dir).
			Item("plan", "command", tool.Text(strings.Join(append([]string{program}, versionArg...), " "))).
			Note("nothing was run or written; the run would make " + probeDir + ", run the command inside the lane wall and write RESULT.md there")
	}
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return tool.Refuse(fmt.Sprintf("the probe job directory cannot be made: %s: %v", probeDir, err))
	}
	ctx := friend.LaneContext(context.Background())
	out, code, runErr := w.wall(wl, w.exec)(ctx, dir, program, versionArg, "")
	if runErr != nil {
		return tool.Refuse(fmt.Sprintf("--dir cannot be run inside the wall: %s: %v", dir, runErr))
	}
	resultPath := filepath.Join(probeDir, "RESULT.md")
	if werr := os.WriteFile(resultPath, []byte("acceptance ok\n"), 0o644); werr != nil {
		return tool.Refuse(fmt.Sprintf("the probe wrote no RESULT.md: %s: %v", resultPath, werr))
	}
	if code != 0 {
		// printed here, not on tool.Exit: Exit renders nothing (it is `printed`), and
		// the play reads this line out of stdout/stderr for its ADOPT REFUSED message.
		fmt.Fprintf(c.Stdout, "ACCEPTANCE NOTE friend=%s exit=%d %s\n", name, code, probeLine(out))
		return tool.Exit(code)
	}
	return tool.Done().Note(fmt.Sprintf("friend=%s exit=0 %s", name, resultPath))
}

// runnerProgram is the program each adapter's lanes start, and the no-cost version argument that runs it without a model
// (docs/SPEC-FRIEND.md, the acceptance run: one version flag per adapter). An unknown
// harness is named as its own program, the way the adapter's default does. Antigravity's
// lane starts the harness's own agentapi under home, the path the adapter runs
// (friend.AntigravityRunner), never a bare name on PATH.
func runnerProgram(harness, home string) (string, []string) {
	switch harness {
	case "claude":
		return "claude", []string{"--version"}
	case "codex":
		return "codex", []string{"--version"}
	case "opencode":
		return "opencode", []string{"--version"}
	case "grok":
		return "grok", []string{"--version"}
	case "gemini":
		return "gemini", []string{"--version"}
	case "dsh":
		return friend.DSHProgram, []string{"--version"}
	case "antigravity":
		return friend.AntigravityRunner(home), []string{"--version"}
	}
	return harness, []string{"--version"}
}

// probeLine is the probe's own output as one line, capped: the play's assert prints it
// beside the friend and the exit code (docs/SPEC-FRIEND.md, the acceptance run).
func probeLine(out string) string {
	line := strings.Join(strings.Fields(out), " ")
	if len(line) > 400 {
		line = line[:400] + "..."
	}
	return line
}
