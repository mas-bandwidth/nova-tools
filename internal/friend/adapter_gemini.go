package friend

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Gemini delivers through `gemini --skip-trust --resume <id> --prompt=<text>`
// in Dir, which resumes the persisted session (the same session id and the
// same chat file under ~/.gemini/tmp/<project>/chats/, read from the CLI's
// ChatRecordingService.initialize on 2026-10-04, v0.46.0) and blocks for the
// whole turn. Without a session named, "latest": the CLI's own newest session
// of the project that Dir is, so a friend who starts a fresh session is still
// reached. --skip-trust trusts Dir for this process only; the friend's own
// session already trusts it. The text goes as --prompt=<text>, one argument,
// so a message beginning with a dash is never read as a flag. Exit codes are
// the CLI's: 42 when the session is not found, 55 untrusted, 1 an error.
type Gemini struct {
	Dir, Session string
	Run          Exec
	Program      string    // "gemini" when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record

	turns SessionTurns // the session's last turns, its liveness (alive.go)
}

// GeminiArgs is the argument list of one delivery: the frame, apart from the
// transport.
func GeminiArgs(session, text string) []string {
	if session == "" {
		session = "latest"
	}
	return []string{"--skip-trust", "--resume", session, "--prompt=" + text}
}

func (g *Gemini) Deliver(ctx context.Context, text string) (int, error) {
	g.turns.begin()
	defer g.turns.end()
	program := g.Program
	if program == "" {
		program = "gemini"
	}
	out, exit, err := g.Run(ctx, g.Dir, program, GeminiArgs(g.Session, text), "")
	if g.Out != nil && out != "" {
		fmt.Fprintln(g.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	session := GeminiArgs(g.Session, "")[2]
	exit, err = refused(session, out, exit, err)
	g.turns.saw(session, exit, err)
	return exit, err
}
