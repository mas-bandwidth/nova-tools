package notice

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// Muse is the Muse shim. Waking an idle session is the Core's one PUSH line
// on the hook's stdout, which the Monitor (wake_delay_ms=0) injects. Into a
// running turn is the bundle's PostToolUse hook, nova-hook pending, which
// prints the one line Push leaves in Notices/pending. Muse reports no turn
// boundary, so consumption is the model's own act on the bus; Clear then
// removes the line so the hook prints nothing.
type Muse struct {
	Notices string
}

// Name is the harness word.
func (m *Muse) Name() string { return "muse" }

// Dir is the private notice directory.
func (m *Muse) Dir() string { return m.Notices }

// Push leaves the one line the PostToolUse hook prints.
func (m *Muse) Push(_ context.Context, n Notice) (string, error) {
	if err := atomicfile.Write(filepath.Join(m.Notices, "pending"), []byte(n.Text()+"\n"), 0o600); err != nil {
		return "", err
	}
	return "monitor", nil
}

// Consumed is false: Muse has no boundary the client can read.
func (m *Muse) Consumed(context.Context, Notice) (bool, error) { return false, nil }

// Clear removes the consumed notice's line.
func (m *Muse) Clear(context.Context, Notice) error {
	if err := os.Remove(filepath.Join(m.Notices, "pending")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// MusePending is the line the PostToolUse hook prints, or "".
func MusePending(notices string) string {
	b, err := os.ReadFile(filepath.Join(notices, "pending"))
	if err != nil {
		return ""
	}
	return string(b)
}
