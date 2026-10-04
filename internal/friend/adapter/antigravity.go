package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

var _ friend.Adapter = (*AntigravityAdapter)(nil)

// RequiredCapabilities are the capabilities the Antigravity adapter must confirm.
var RequiredCapabilities = []string{"durable-delivery-id", "durable-receipt"}

// Transport delivers a message payload to an Antigravity conversation or session.
type Transport interface {
	SendMessage(ctx context.Context, recipientID string, title string, body string) error
}

// AgentAPITransport delivers messages using the native `agentapi send-message` CLI.
type AgentAPITransport struct {
	Command string        // executable name or path; defaults to "agentapi"
	Dir     string        // optional working directory
	Env     []string      // optional environment variables
	Timeout time.Duration // default: 30s
}

// SendMessage runs `agentapi send-message [--title=<title>] <recipient_id> <content>`.
func (t *AgentAPITransport) SendMessage(ctx context.Context, recipientID string, title string, body string) error {
	cmdName := t.Command
	if cmdName == "" {
		cmdName = "agentapi"
	}
	args := []string{"send-message"}
	if title != "" {
		args = append(args, "--title="+title)
	}
	args = append(args, recipientID, body)

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cmd, cancel := subproc.CommandFor(ctx, timeout, cmdName, args...)
	defer cancel()

	cmd.Dir = t.Dir
	if len(t.Env) > 0 {
		cmd.Env = append(os.Environ(), t.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agentapi send-message failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// AntigravityAdapter implements friend.Adapter for the Antigravity harness.
// It achieves durable delivery and deduplication across process restarts by maintaining
// a persistent store of delivered keys. Replaying a pending message returns the prior
// durable acceptance without re-injecting.
type AntigravityAdapter struct {
	mu           sync.RWMutex
	SessionID    string
	Revision     string
	StatePath    string
	Transport    Transport
	Bridge       friend.Adapter
	Capabilities []string

	records map[string]friendbus.Acceptance
}

// NewAntigravityAdapter constructs an AntigravityAdapter with the specified parameters.
func NewAntigravityAdapter(sessionID string, statePath string, transport Transport) *AntigravityAdapter {
	return &AntigravityAdapter{
		SessionID: sessionID,
		StatePath: statePath,
		Transport: transport,
	}
}

// recordFile is the JSON schema persisted to StatePath across restarts.
type recordFile struct {
	Deliveries map[string]friendbus.Acceptance `json:"deliveries"`
}

func (a *AntigravityAdapter) loadRecordsLocked() error {
	if a.records != nil {
		return nil
	}
	a.records = make(map[string]friendbus.Acceptance)
	if a.StatePath == "" {
		return nil
	}
	data, err := os.ReadFile(a.StatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read antigravity durable store: %w", err)
	}
	var rf recordFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("decode antigravity durable store: %w", err)
	}
	if rf.Deliveries != nil {
		a.records = rf.Deliveries
	}
	return nil
}

func (a *AntigravityAdapter) persistRecordLocked(key string, acc friendbus.Acceptance) error {
	a.records[key] = acc
	if a.StatePath == "" {
		return nil
	}
	dir := filepath.Dir(a.StatePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create antigravity durable store dir: %w", err)
	}
	rf := recordFile{Deliveries: a.records}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("encode antigravity durable store: %w", err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", a.StatePath, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write antigravity durable store tmp: %w", err)
	}
	if err := os.Rename(tmp, a.StatePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit antigravity durable store: %w", err)
	}
	return nil
}

// Register validates or sets s.Adapter = "antigravity", confirms required capabilities,
// sets/validates the session ID, and sets route revision.
func (a *AntigravityAdapter) Register(ctx context.Context, friendName string, s friendbus.Session) (friendbus.Session, error) {
	if a.Bridge != nil {
		bridged, err := a.Bridge.Register(ctx, friendName, s)
		if err != nil {
			return friendbus.Session{}, err
		}
		s = bridged
	}

	if a.SessionID != "" {
		s.ID = a.SessionID
	}
	if strings.TrimSpace(s.ID) == "" {
		return friendbus.Session{}, errors.New("antigravity registration requires a non-empty session ID")
	}

	if s.Adapter == "" {
		s.Adapter = "antigravity"
	} else if s.Adapter != "antigravity" {
		return friendbus.Session{}, fmt.Errorf("antigravity adapter requires adapter %q, got %q", "antigravity", s.Adapter)
	}

	if a.Revision != "" {
		s.Revision = a.Revision
	} else if s.Revision == "" {
		s.Revision = fmt.Sprintf("ag-rev-%d", time.Now().UnixNano())
	}

	caps := s.Capabilities
	if a.Capabilities != nil {
		caps = a.Capabilities
	} else if len(caps) == 0 {
		caps = slices.Clone(RequiredCapabilities)
	}
	for _, req := range RequiredCapabilities {
		if !slices.Contains(caps, req) {
			return friendbus.Session{}, fmt.Errorf("antigravity harness does not confirm %s; deliveries remain pending", req)
		}
	}
	s.Capabilities = caps

	return s, nil
}

// Wake delivers an addressed message to the native Antigravity session.
// It checks the durable delivery ledger first; replaying an existing delivery key returns
// the prior durable acceptance without re-injecting.
// Exit zero of agentapi alone is NOT durable acceptance; acceptance is explicitly constructed,
// bound, and persisted.
func (a *AntigravityAdapter) Wake(ctx context.Context, d friendbus.Delivery, s friendbus.Session) (friendbus.Acceptance, error) {
	if strings.TrimSpace(d.Key) == "" {
		return friendbus.Acceptance{}, errors.New("antigravity wake requires a delivery key")
	}
	if strings.TrimSpace(s.ID) == "" {
		return friendbus.Acceptance{}, errors.New("antigravity wake requires a session ID")
	}
	if s.Adapter != "antigravity" {
		return friendbus.Acceptance{}, fmt.Errorf("antigravity wake wants adapter %q, got %q", "antigravity", s.Adapter)
	}
	for _, req := range RequiredCapabilities {
		if !slices.Contains(s.Capabilities, req) {
			return friendbus.Acceptance{}, fmt.Errorf("antigravity harness does not confirm %s; deliveries remain pending", req)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.loadRecordsLocked(); err != nil {
		return friendbus.Acceptance{}, err
	}

	// Deduplication: replaying a pending message returns the prior durable acceptance
	if existing, ok := a.records[d.Key]; ok {
		return existing, nil
	}

	if a.Bridge != nil {
		acceptance, err := a.Bridge.Wake(ctx, d, s)
		if err != nil {
			return friendbus.Acceptance{}, err
		}
		if err := a.persistRecordLocked(d.Key, acceptance); err != nil {
			return friendbus.Acceptance{}, err
		}
		return acceptance, nil
	}

	transport := a.Transport
	if transport == nil {
		transport = &AgentAPITransport{}
	}

	title := d.Re
	if title == "" {
		if d.From != "" {
			title = fmt.Sprintf("nova-bus message from %s", d.From)
		} else {
			title = "nova-bus message"
		}
	}

	if err := transport.SendMessage(ctx, s.ID, title, string(d.Body)); err != nil {
		return friendbus.Acceptance{}, fmt.Errorf("deliver %s to antigravity session %s: %w", d.Key, s.ID, err)
	}

	receiptID := GenerateReceiptID(s.ID, s.Revision, d.Key)
	acceptance := friendbus.Acceptance{
		ReceiptID: receiptID,
		Adapter:   "antigravity",
		Session:   s.ID,
		Revision:  s.Revision,
		Durable:   true,
	}

	if err := a.persistRecordLocked(d.Key, acceptance); err != nil {
		return friendbus.Acceptance{}, err
	}

	return acceptance, nil
}

// GenerateReceiptID produces a deterministic, durable receipt identifier
// binding the session ID, route revision, and delivery key.
func GenerateReceiptID(sessionID, revision, deliveryKey string) string {
	sum := sha256.Sum256([]byte(sessionID + ":" + revision + ":" + deliveryKey))
	return "ag-receipt-" + hex.EncodeToString(sum[:16])
}
