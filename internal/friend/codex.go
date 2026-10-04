package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
)

// CodexSessionStart is the native SessionStart input, not proof of an external
// control endpoint. See https://learn.chatgpt.com/docs/hooks, SessionStart.
// Hook definitions require the user's trust; this decoder never installs or
// trusts a hook and never derives authority from a transcript or process ID.
type CodexSessionStart struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Model     string `json:"model"`
	Source    string `json:"source"`
	Event     string `json:"hook_event_name"`
}

// ReadCodexSessionStart decodes one bounded native hook event. Unknown fields
// are allowed for forward compatibility, but a different event or a second
// JSON value is refused rather than registered as a session.
func ReadCodexSessionStart(r io.Reader) (CodexSessionStart, error) {
	const maxInput = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return CodexSessionStart{}, fmt.Errorf("read Codex SessionStart: %w", err)
	}
	if len(raw) > maxInput {
		return CodexSessionStart{}, fmt.Errorf("Codex SessionStart exceeds %d bytes", maxInput)
	}
	var event CodexSessionStart
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, fmt.Errorf("decode Codex SessionStart: %w", err)
	}
	if event.Event != "SessionStart" {
		return event, fmt.Errorf("Codex hook event is %q, wants SessionStart", event.Event)
	}
	if event.SessionID == "" || strings.ContainsAny(event.SessionID, " \t\r\n") {
		return event, fmt.Errorf("Codex SessionStart wants its native session_id")
	}
	if !slices.Contains([]string{"startup", "resume", "clear", "compact"}, event.Source) {
		return event, fmt.Errorf("Codex SessionStart source is %q, wants startup, resume, clear or compact", event.Source)
	}
	if event.CWD == "" {
		return event, fmt.Errorf("Codex SessionStart wants its cwd")
	}
	return event, nil
}

// CodexAdapter uses an explicitly supplied native bridge. A SessionStart ID
// alone cannot wake a stdio-only desktop server. The bridge must attest its
// durable delivery and receipt capabilities through the runtime handshake,
// and bind every receipt to the delivery key (Adapter's contract).
// See https://learn.chatgpt.com/docs/app-server, transports and turn/start.
// This adapter never starts another app-server or resumes a separate copy.
type CodexAdapter struct {
	Bridge Adapter
	// SessionID is supplied by the trusted SessionStart hook for initial registration.
	// Without it registration restores the previously registered native session.
	SessionID string
}

func (a CodexAdapter) Register(ctx context.Context, recipient string, previous friendbus.Session) (friendbus.Session, error) {
	if a.Bridge == nil {
		return friendbus.Session{}, codexBridgeMissing()
	}
	if a.SessionID != "" {
		previous.ID = a.SessionID
	}
	if previous.ID == "" {
		return friendbus.Session{}, fmt.Errorf("Codex registration wants the existing native session ID from SessionStart")
	}
	session, err := a.Bridge.Register(ctx, recipient, previous)
	if err != nil {
		return friendbus.Session{}, err
	}
	if session.ID != previous.ID {
		return friendbus.Session{}, fmt.Errorf("Codex bridge registered another session: wants %s", previous.ID)
	}
	if err := codexSession(session); err != nil {
		return friendbus.Session{}, err
	}
	return session, nil
}

func (a CodexAdapter) Wake(ctx context.Context, d friendbus.Delivery, session friendbus.Session) (friendbus.Acceptance, error) {
	if a.Bridge == nil {
		return friendbus.Acceptance{}, codexBridgeMissing()
	}
	if err := codexSession(session); err != nil {
		return friendbus.Acceptance{}, err
	}
	receipt, err := a.Bridge.Wake(ctx, d, session)
	if err != nil {
		return friendbus.Acceptance{}, err
	}
	if receipt.Session != session.ID || receipt.Adapter != session.Adapter || receipt.Revision != session.Revision || receipt.ReceiptID == "" || !receipt.Durable {
		return friendbus.Acceptance{}, fmt.Errorf("Codex bridge did not return a durable receipt for the registered session")
	}
	return receipt, nil
}

func codexSession(session friendbus.Session) error {
	if session.Adapter != "codex" {
		return fmt.Errorf("Codex bridge wants the codex adapter identity")
	}
	return validateSession(session)
}

func codexBridgeMissing() error {
	return fmt.Errorf("Codex external wake has no registered native bridge; expose the existing app-server control endpoint or configure an app-owned bridge, then register again; a SessionStart hook or running process alone cannot accept a delivery")
}
