package friend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// CommandConfig names a native adapter bridge directly. Message content is
// JSON on stdin, never interpolated into argv or passed through a shell.
type CommandConfig struct {
	Argv      []string `json:"argv"`
	Dir       string   `json:"dir,omitempty"`
	Env       []string `json:"env,omitempty"`
	Adapter   string   `json:"adapter,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
}

// NewAdapter selects the native identity guard for an explicit bridge. Codex
// registration pins the existing session from its trusted startup hook; no
// default endpoint or separate app-server is manufactured here.
func NewAdapter(config CommandConfig, timeout time.Duration) (Adapter, error) {
	bridge, err := NewCommandAdapter(config, timeout)
	if err != nil {
		return nil, err
	}
	switch config.Adapter {
	case "":
		return bridge, nil
	case "codex":
		return CodexAdapter{Bridge: bridge, SessionID: config.SessionID}, nil
	default:
		return nil, fmt.Errorf("native adapter %q is unsupported; use an explicitly registered bridge", config.Adapter)
	}
}

// CommandAdapter speaks the native bridge's registration/receipt protocol.
// The configured bridge must actually restore/inject into the harness and
// persist delivery-key deduplication; successful exit alone acknowledges nothing.
type CommandAdapter struct {
	Config  CommandConfig
	Timeout time.Duration
}

func NewCommandAdapter(config CommandConfig, timeout time.Duration) (*CommandAdapter, error) {
	if len(config.Argv) == 0 || strings.TrimSpace(config.Argv[0]) == "" {
		return nil, errors.New("the native bridge needs a direct argv command")
	}
	if timeout <= 0 {
		return nil, errors.New("the native bridge timeout must be positive")
	}
	return &CommandAdapter{Config: config, Timeout: timeout}, nil
}

// CommandRequest is the stdin request: previous session for registration,
// current session and the stable delivery key for a native injection.
type CommandRequest struct {
	Operation string              `json:"operation"`
	Recipient string              `json:"recipient"`
	Previous  *friendbus.Session  `json:"previous,omitempty"`
	Session   *friendbus.Session  `json:"session,omitempty"`
	Delivery  *friendbus.Delivery `json:"delivery,omitempty"`
}

// CommandResponse contains real native registration or acceptance evidence.
// DeliveryKey must echo the injected stable key. Error refuses the request.
type CommandResponse struct {
	Session     friendbus.Session    `json:"session"`
	Acceptance  friendbus.Acceptance `json:"acceptance"`
	DeliveryKey string               `json:"delivery_key"`
	Error       string               `json:"error,omitempty"`
}

func (a *CommandAdapter) Register(ctx context.Context, recipient string, previous friendbus.Session) (friendbus.Session, error) {
	response, err := a.call(ctx, CommandRequest{Operation: "register", Recipient: recipient, Previous: &previous})
	if err != nil {
		return friendbus.Session{}, err
	}
	if err := validateSession(response.Session); err != nil {
		return friendbus.Session{}, err
	}
	return response.Session, nil
}

func (a *CommandAdapter) Wake(ctx context.Context, d friendbus.Delivery, session friendbus.Session) (friendbus.Acceptance, error) {
	response, err := a.call(ctx, CommandRequest{Operation: "wake", Recipient: d.Recipient, Delivery: &d, Session: &session})
	if err != nil {
		return friendbus.Acceptance{}, err
	}
	if response.DeliveryKey != d.Key {
		return friendbus.Acceptance{}, errors.New("the native bridge receipt does not name the injected delivery key")
	}
	return response.Acceptance, nil
}

const commandOutputLimit = 64 << 10

// boundedOutput accepts a child's output while retaining only a bounded prefix.
// An oversized receipt is refused; stderr is bounded independently.
type boundedOutput struct {
	buffer   bytes.Buffer
	TooLarge bool
}

func (b *boundedOutput) String() string { return b.buffer.String() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := commandOutputLimit - b.buffer.Len()
	if n > room {
		b.TooLarge = true
		p = p[:room]
	}
	_, err := b.buffer.Write(p)
	return n, err
}

func (a *CommandAdapter) call(ctx context.Context, request CommandRequest) (CommandResponse, error) {
	if a == nil || len(a.Config.Argv) == 0 || a.Timeout <= 0 {
		return CommandResponse{}, errors.New("the native bridge is not configured")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return CommandResponse{}, err
	}
	cmd, cancel := subproc.CommandFor(ctx, a.Timeout, a.Config.Argv[0], a.Config.Argv[1:]...)
	defer cancel()
	cmd.Dir = a.Config.Dir
	cmd.Env = append(os.Environ(), a.Config.Env...)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return CommandResponse{}, fmt.Errorf("native bridge failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.TooLarge {
		return CommandResponse{}, errors.New("the native bridge receipt exceeds 64 KiB")
	}
	var response CommandResponse
	dec := json.NewDecoder(bytes.NewReader(stdout.buffer.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&response); err != nil {
		return CommandResponse{}, fmt.Errorf("read the native bridge receipt: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return CommandResponse{}, errors.New("the native bridge must return exactly one receipt")
	}
	if response.Error != "" {
		return CommandResponse{}, fmt.Errorf("the native bridge refused: %s", response.Error)
	}
	return response, nil
}
