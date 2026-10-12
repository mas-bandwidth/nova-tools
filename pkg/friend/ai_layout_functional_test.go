//go:build functional

package friend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAILayoutBuildsNothingOutsideAiName runs friend sync on a temp ai/ root and
// asserts every path nova tools build lands under <root>/ai/<name>/ — the
// working directory, its inbox and the seat's deny-self read — and that nothing
// is created at the old top-level <name>-working sibling (docs/FRIENDS.md, the
// ai/ layout).
func TestAILayoutBuildsNothingOutsideAiName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	name := "ada"

	dir := AIPath(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	job := workCard("held.w1", "working")
	inboxJob(t, dir, job.Job, job.Brief)
	asked := time.Now()

	_, err := SyncInbox(dir, Row{Reads: true, Cards: []HeldCard{
		{Card: job.Card, Job: job.Job, Col: "working", Brief: job.Brief},
	}}, nil, asked, asked, func(string) {})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "inbox", job.Job, "BRIEF.md"))

	// the seat's deny-self resolves inside ai/<seat>/ under the same root.
	assert.Equal(t, []string{filepath.Join(root, "ai", "seat", "working")}, DefaultDenySelf(root, "seat"))

	// nothing is created at the old top-level siblings.
	assert.NoDirExists(t, filepath.Join(root, name+"-working"))
	assert.NoDirExists(t, filepath.Join(root, "seat-working"))
}
