//go:build functional

package release

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"time"
)

func TestExecGitReadsLargeRangeWithoutRenameWarningsAsPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	git := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		var out, errs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errs
		if err := cmd.Run(); err != nil {
			require.NoError(t, err, "git %v: %v: %s", args, err, errs.String())
		}
		return strings.TrimSpace(out.String()), errs.String()
	}
	git("init", "--quiet", "--template=")
	for key, value := range map[string]string{
		"user.name": "Release fixture", "user.email": "release@example.invalid",
		"commit.gpgsign": "false", "core.hooksPath": filepath.Join(dir, "no-hooks"),
		"diff.renameLimit": "1", "diff.renames": "true",
		// No detached auto gc or maintenance: one still writing .git/objects
		// made the TempDir cleanup fail ("directory not empty"), 3 runs in 8.
		"gc.auto": "0", "maintenance.auto": "false",
	} {
		git("config", "--local", key, value)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			require.NoError(t, err, err)
		}
	}
	want := []string{"old-a.txt", "old-b.txt"}
	write(want[0], "old alpha content\n")
	write(want[1], "old beta content\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "base")
	base, _ := git("rev-parse", "HEAD")
	for _, name := range want {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			require.NoError(t, err, err)
		}
	}
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("new-%04d-%s.txt", i, strings.Repeat("x", 140))
		want = append(want, name)
		write(name, fmt.Sprintf("new fixture content %d\n", i))
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "large range")
	head, _ := git("rev-parse", "HEAD")
	raw, warnings := git("diff", "--name-only", base+"..."+head)
	if len(raw) <= childCap || !strings.Contains(warnings, "rename") {
		require.FailNowf(t, "", "fixture did not exercise large stdout and warning stderr: bytes=%d stderr=%q", len(raw), warnings)
	}
	files, err := (ExecGit{}).DiffNames(ctx, dir, base, head)
	sort.Strings(want)
	if err != nil || !reflect.DeepEqual(files, want) {
		require.FailNowf(t, "", "production reader lost paths or included warnings: got=%d want=%d error=%v", len(files), len(want), err)
	}
}
