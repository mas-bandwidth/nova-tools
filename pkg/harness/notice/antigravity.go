package notice

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
)

// Antigravity is the Antigravity shim. Into a running turn and waking an
// idle session are one mechanism: agentapi send-message into the
// conversation, whose fixed text names the saved file. Delivery is confirmed
// only by a mailbox file carrying this notice's own id. Antigravity reports
// no turn boundary, so consumption is the model's own act on the bus.
type Antigravity struct {
	Directory string
	// Deliver is the agentapi call; a test passes its fake harness.
	Deliver func(ctx context.Context, id, directory string) (string, error)
	// Token is the language server's CSRF token, which may pass only through
	// the agentapi environment: it is redacted from every error.
	Token func() string
}

// Name is the harness word.
func (a *Antigravity) Name() string { return "antigravity" }

// Dir is the private events directory.
func (a *Antigravity) Dir() string { return a.Directory }

// Push is one send; it is never retried.
func (a *Antigravity) Push(ctx context.Context, n Notice) (string, error) {
	turn, err := a.Deliver(ctx, n.ID, a.Directory)
	if err != nil {
		if a.Token != nil {
			err = Redact(err, a.Token())
		}
		return turn, err
	}
	if a.Directory != "" {
		if _, statErr := os.Stat(a.Directory); statErr == nil {
			if err := dshdesktop.MarkPending(a.Directory, n.ID); err != nil {
				return turn, err
			}
			if _, err := dshdesktop.PruneResolvedNotices(a.Directory, n.ID); err != nil {
				return turn, err
			}
		}
	}
	return turn, nil
}

// Consumed is false: the conversation has no boundary the client can read.
func (a *Antigravity) Consumed(context.Context, Notice) (bool, error) { return false, nil }

// Clear marks a consumed notice resolved and prunes resolved notice-*.json
// files in Directory beyond dshdesktop.MaxResolvedNotices.
func (a *Antigravity) Clear(_ context.Context, n Notice) error {
	if a.Directory == "" {
		return nil
	}
	if _, err := os.Stat(a.Directory); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, err := dshdesktop.ResolveNotice(a.Directory, n.ID)
	return err
}

// Redact keeps a secret out of an error's text and keeps what it wraps.
func Redact(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return redacted{err: err, text: strings.ReplaceAll(err.Error(), secret, "<redacted>")}
}

type redacted struct {
	err  error
	text string
}

func (r redacted) Error() string { return r.text }
func (r redacted) Unwrap() error { return r.err }

// MailboxSnapshot is the names in a conversation mailbox before a send.
func MailboxSnapshot(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// MailboxConfirm is the newest file written since before that carries id,
// or "". A file another sender wrote never confirms this notice.
func MailboxConfirm(dir string, before map[string]bool, id string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var added []string
	for _, e := range entries {
		if before[e.Name()] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && bytes.Contains(b, []byte(id)) {
			added = append(added, e.Name())
		}
	}
	if len(added) == 0 {
		return ""
	}
	sort.Strings(added)
	return added[len(added)-1]
}
