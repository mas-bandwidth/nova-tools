package friend

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// Codex delivers to an open desktop chat through its owning app's IPC socket.
// A confirmed admission is recorded as delivered, not as an answered turn.
// When the app is unavailable the message stays pending (Deferred); it never
// starts a headless copy (SPEC-FRIEND.md, Codex). Without a named session,
// resolve the newest saved thread for Dir before discovering its app owner.
type Codex struct {
	Dir, Session string
	Dial         func(context.Context, string, string) (net.Conn, error) // net.Dialer.DialContext when nil
	Home         string                                                  // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Env          func(string) string                                     // getenv; os.Getenv when nil
	Out          io.Writer                                               // where the admission receipt goes, when set
}

func (c *Codex) home() string {
	if c.Home != "" {
		return c.Home
	}
	getenv := c.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	if h := getenv("CODEX_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

// Deliver follows the measured desktop admission contract in SPEC-FRIEND.md,
// Codex. It does not equate an admission with the model completing an answer.
func (c *Codex) Deliver(ctx context.Context, text string) (int, error) {
	session := c.Session
	if session == "" {
		var err error
		session, err = NewestCodexSession(c.home(), c.Dir)
		if err != nil {
			return 1, err
		}
	}
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	turn, err := codexOpenChat(ctx, c.home(), session, text, dial)
	if err != nil {
		return 0, Deferred{Reason: fmt.Sprintf("thread %s open-chat admission is unconfirmed (%v); keep the message pending and retry with the chat open", session, err)}
	}
	if c.Out != nil {
		fmt.Fprintf(c.Out, "delivered to open chat: thread=%s turn=%s (admitted, not answered)\n", session, turn)
	}
	return 0, nil
}
