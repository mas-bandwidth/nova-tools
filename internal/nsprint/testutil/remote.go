package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// LocalRemote is a local bare git repository configured to enforce dev-integrity.
// Its hooks refuse non-fast-forward pushes and ref deletions.
type LocalRemote struct {
	Dir string // Absolute filesystem path to the bare repo
	URL string // file:// transport URL
}

// NewLocalRemote creates a local bare git repository initialized with defaultBranch.
// It installs update and pre-receive hooks that enforce fast-forward only updates.
func NewLocalRemote(t *testing.T, defaultBranch string) *LocalRemote {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git binary not found: %v", err)
	}

	root := t.TempDir()
	bareDir := filepath.Join(root, "remote.git")

	cmd := exec.Command(gitBin, "init", "--bare", "--quiet", "--initial-branch="+defaultBranch, bareDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	hooksDir := filepath.Join(bareDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}

	// hooks/update receives: <refname> <oldrev> <newrev>
	hookScript := `#!/bin/sh
ref="$1"
oldrev="$2"
newrev="$3"

zero="0000000000000000000000000000000000000000"

# Initial ref creation is allowed
if [ "$oldrev" = "$zero" ]; then
    exit 0
fi

# Deletion is refused under dev-integrity
if [ "$newrev" = "$zero" ]; then
    echo "REFUSED deletion of $ref by dev-integrity" >&2
    exit 1
fi

# Refuse non-fast-forward
if ! git merge-base --is-ancestor "$oldrev" "$newrev"; then
    echo "REFUSED non-fast-forward push to $ref by dev-integrity" >&2
    exit 1
fi

exit 0
`
	updateHook := filepath.Join(hooksDir, "update")
	if err := os.WriteFile(updateHook, []byte(hookScript), 0o755); err != nil {
		t.Fatalf("write update hook: %v", err)
	}

	preReceiveScript := `#!/bin/sh
zero="0000000000000000000000000000000000000000"
while read oldrev newrev ref; do
    if [ "$oldrev" != "$zero" ]; then
        if [ "$newrev" = "$zero" ]; then
            echo "REFUSED deletion of $ref by dev-integrity" >&2
            exit 1
        fi
        if ! git merge-base --is-ancestor "$oldrev" "$newrev"; then
            echo "REFUSED non-fast-forward push to $ref by dev-integrity" >&2
            exit 1
        fi
    fi
done
exit 0
`
	preReceiveHook := filepath.Join(hooksDir, "pre-receive")
	if err := os.WriteFile(preReceiveHook, []byte(preReceiveScript), 0o755); err != nil {
		t.Fatalf("write pre-receive hook: %v", err)
	}

	url := "file://" + filepath.ToSlash(bareDir)
	return &LocalRemote{
		Dir: bareDir,
		URL: url,
	}
}
