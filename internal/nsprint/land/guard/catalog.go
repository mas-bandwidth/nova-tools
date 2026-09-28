package guard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/docs"
)

// catalog holds the committed AGENTS.md pages against a fresh render of the
// tree (internal/docs, `make map`): a merged tree where one member added a
// directory and another edited the catalog is stale as a whole, though each
// member's page matched its own tree.
//
// The fast path renders with this binary's catalog. When that reports an
// issue, the tree's own catalog is the truth (a member may have added a row
// this binary does not carry), so the guard confirms by running the tree's
// own map test once, `go test ./internal/docs -run TestCommittedMapMatchesTree`,
// and reports what that says.
func catalog(ctx context.Context, root string) Result {
	if _, err := os.Stat(filepath.Join(root, docs.RootAgents)); err != nil {
		return Result{OK: false, File: docs.RootAgents, Why: "missing; run: make map"}
	}
	issues := docs.Check(root, docs.DefaultCatalog)
	if len(issues) == 0 {
		return Result{OK: true, Why: "committed map matches the tree"}
	}
	first := issues[0]
	file := first
	if i := strings.Index(first, " "); i > 0 {
		file = first[:i]
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return Result{OK: false, File: file, Why: first + " (no go on PATH to confirm with the tree's own catalog)"}
	}
	tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(tctx, goBin, "test", "-count=1", "-run", "^TestCommittedMapMatchesTree$", "./internal/docs/")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(out.String())
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return Result{OK: false, File: file, Why: fmt.Sprintf("%s (the tree's own map test agrees: %s)", first, strings.ReplaceAll(tail, "\n", " | "))}
	}
	return Result{OK: true, Why: "committed map matches the tree by its own catalog (this binary's catalog is older)"}
}
