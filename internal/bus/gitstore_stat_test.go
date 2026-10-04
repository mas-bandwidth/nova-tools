package bus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/require"
)

// The stat of a path in a git tree reads the blob's size with `git cat-file -s`. The size
// is a number git prints or the read does not happen; this pins that a git that prints
// something else, or a git that fails, is an error rather than a size of zero.
//
// gitFS runs the process's `git` with no seam for a fake, so each case runs in a child
// test process whose PATH holds the fake first: a parent that set PATH itself would be
// changing it under every parallel test that is also running git.
func TestGitFSStatRefusesASizeItCannotRead(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	cases := map[string]struct {
		body string
		want string
	}{
		"not a number": {
			body: "case \"$1\" in\n  cat-file)\n    case \"$2\" in\n      -t) echo blob;;\n      -s) echo not-a-number;;\n    esac;;\nesac\n",
			want: "did not answer with a number",
		},
		"the git fails": {
			body: "case \"$1\" in\n  cat-file)\n    case \"$2\" in\n      -t) echo blob;;\n      -s) echo \"fatal: bad object\" >&2; exit 128;;\n    esac;;\nesac\n",
			want: "bad object",
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := t.TempDir()
			script := "#!/bin/sh\nshift 2\n" + tc.body
			require.NoError(t, testbin.WriteExecutable(filepath.Join(fake, "git"), []byte(script), 0o755))

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitFSStatHelper$")
			cmd.WaitDelay = 2 * time.Second
			env := make([]string, 0, len(os.Environ())+2)
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, gitStatChildEnv+"=") {
					continue
				}
				env = append(env, entry)
			}
			cmd.Env = append(env, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), gitStatChildEnv+"="+tc.want)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "the child did not see the refusal %q: %s", tc.want, out)
		})
	}
}

// gitStatChildEnv carries the phrase the child's stat refusal must hold, and its presence
// says this process is the child.
const gitStatChildEnv = "NOVA_BUS_GITSTAT_CHILD"

// TestGitFSStatHelper is the child half of TestGitFSStatRefusesASizeItCannotRead: it stats
// a path through the fake git on its PATH and asserts the refusal. It runs only when
// gitStatChildEnv is set, so it does nothing in an ordinary package run.
func TestGitFSStatHelper(t *testing.T) {
	t.Parallel()
	want := os.Getenv(gitStatChildEnv)
	if want == "" {
		t.Skip("the helper runs only in the child process the parent starts")
	}
	_, err := (gitFS{dir: t.TempDir(), commit: "HEAD"}).Stat("from-ada/one.md")
	require.Error(t, err, "a git cat-file -s the stat cannot read was read as size zero")
	require.Contains(t, err.Error(), "size", "the refusal does not say it is the size that failed: %v", err)
	require.Contains(t, err.Error(), want, "the refusal does not hold %q: %v", want, err)
}

// The one that must not regress: a git that answers with a number still reports it.
func TestGitFSStatReportsTheSizeGitPrints(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nshift 2\ncase \"$1\" in\n  cat-file)\n    case \"$2\" in\n      -t) echo blob;;\n      -s) echo 42;;\n    esac;;\nesac\n"
	require.NoError(t, testbin.WriteExecutable(filepath.Join(fake, "git"), []byte(script), 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitFSStatSizeHelper$")
	cmd.WaitDelay = 2 * time.Second
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, gitStatSizeChildEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), gitStatSizeChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the child did not read the size git printed: %s", out)
}

const gitStatSizeChildEnv = "NOVA_BUS_GITSTAT_SIZE_CHILD"

// TestGitFSStatSizeHelper is the child half of TestGitFSStatReportsTheSizeGitPrints.
func TestGitFSStatSizeHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv(gitStatSizeChildEnv) == "" {
		t.Skip("the helper runs only in the child process the parent starts")
	}
	info, err := (gitFS{dir: t.TempDir(), commit: "HEAD"}).Stat("from-ada/one.md")
	require.NoError(t, err)
	require.Equal(t, int64(42), info.Size())
}
