package member

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// scriptRepo is a temporary repository whose one start commit holds a.txt and b.txt, and
// a fake wall whose program upper-cases a.txt: the program the card names.
type scriptRepo struct {
	dir, start string
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	env := testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := gitrun.Output(context.Background(), gitrun.Options{C: dir, Env: env, OwnRepo: true}, args...)
	require.NoError(t, err)
	return out
}

func newScriptRepo(t *testing.T) *scriptRepo {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644))
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "start")
	return &scriptRepo{dir: dir, start: gitIn(t, dir, "rev-parse", "HEAD")}
}

// program is the card's program, as the wall would run it: argv[0] names what it does.
func program(_ context.Context, dir string, argv []string) error {
	if len(argv) == 0 || argv[0] != "upcase" {
		return os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.ToUpper(string(b))), 0o644)
}

// head commits a.txt's text on a branch off the start commit and returns the commit.
func (r *scriptRepo) head(t *testing.T, branch, text string, extra map[string]string) string {
	t.Helper()
	gitIn(t, r.dir, "checkout", "-q", "-b", branch, r.start)
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte(text), 0o644))
	for name, body := range extra {
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o644))
	}
	gitIn(t, r.dir, "add", "-A")
	gitIn(t, r.dir, "commit", "-q", "--allow-empty", "-m", branch)
	return gitIn(t, r.dir, "rev-parse", "HEAD")
}

const scriptBrief = "STATUS: x\n\nCLASS: script\nSCRIPT: upcase\nDEADLINE: finish within 5 minutes\n\nSTEP 1. run it.\n"

// A script card whose head is its program's output needs no model read: a script reader
// checks out the attempt's start commit, runs the SCRIPT program, and compares the diff
// with the head's byte for byte; identical is an ok read counted as all the reads the card
// needs, and any difference, or a failed run, is no verdict and the read goes to a model
// (docs/SPEC-SPRINT.md, the script read).
func TestAScriptCardWhoseDiffMatchesItsProgramNeedsNoModelRead(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	verifier := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: program}
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	edited := repo.head(t, "edited", "HELLO\nWORLD, by hand\n", nil)
	extra := repo.head(t, "extra", "HELLO\nWORLD\n", map[string]string{"c.txt": "also\n"})
	same := repo.head(t, "same", "hello\nworld\n", nil) // a commit with no diff

	read := func(head string) Packet {
		return Packet{Card: "r1", Kind: "read", As: "r", Primary: "p1", Attempt: 1, Epoch: 7, Head: head, BaseHead: repo.start, Brief: scriptBrief}
	}
	tick := func(t *testing.T, g *rig, p Packet) {
		t.Helper()
		g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &p)))
		_, err := g.tick(t)
		require.NoError(t, err)
		if c := g.r.child("r1"); c != nil {
			c.end(Result{Ran: true, Verdict: "broken", Report: "model read ran"})
		}
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
		g.s.reset()
		_, err = g.tick(t)
		require.NoError(t, err)
	}

	t.Run("a head that is the program's output is an ok script read and no model starts", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 1, Reader: true, ScriptVerify: verifier.Verify})
		tick(t, g, read(made))
		assert.Empty(t, g.r.started(), "no model child: %s", g.out.String())
		lines := g.s.lines("report")
		require.Len(t, lines, 1, g.out.String())
		assert.Contains(t, lines[0], "read --as r --ok r1 --finding "+ScriptReadPrefix+`ran "upcase" at `+short(repo.start))
		assert.Contains(t, lines[0], "identical")
	})

	for _, tc := range []struct{ name, head, why string }{
		{"a head edited by hand is sent to a model", edited, "is not the head's"},
		{"a head with a file the program did not make is sent to a model", extra, "is not the head's"},
		{"a head with no diff is sent to a model", same, "is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "r", Width: 1, Reader: true, ScriptVerify: verifier.Verify})
			tick(t, g, read(tc.head))
			assert.Equal(t, []string{"r1"}, g.r.started(), "the model read starts: %s", g.out.String())
			assert.Contains(t, g.out.String(), "read r1: script read gave no verdict (")
			assert.Contains(t, g.out.String(), tc.why)
			for _, l := range g.s.lines("report") {
				assert.NotContains(t, l, ScriptReadPrefix, "no script finding without a match")
			}
		})
	}

	t.Run("a program that fails is no verdict", func(t *testing.T) {
		t.Parallel()
		v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: func(context.Context, string, []string) error { return os.ErrPermission }}
		ok, why := v.Verify(read(made), mustClass(t, scriptBrief))
		assert.False(t, ok)
		assert.Contains(t, why, "the program failed")
	})

	t.Run("with no start commit named the merge base with the work's base branch is the start", func(t *testing.T) {
		t.Parallel()
		p := read(made)
		p.BaseHead, p.WorkBase = "", "master"
		p.WorkBase = repo.start // any ref that names the start commit
		ok, why := verifier.Verify(p, mustClass(t, scriptBrief))
		assert.True(t, ok, why)
		p.WorkBase = ""
		ok, why = verifier.Verify(p, mustClass(t, scriptBrief))
		assert.False(t, ok)
		assert.Contains(t, why, "no start commit")
	})

	t.Run("a reader with no script verifier and a model card start a model child as today", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 1, Reader: true})
		tick(t, g, read(made))
		assert.Equal(t, []string{"r1"}, g.r.started())
		g = newRig(Config{As: "r", Width: 1, Reader: true, ScriptVerify: verifier.Verify})
		p := read(made)
		p.Brief = "STATUS: x\n\nTHE TASK. model work.\n"
		tick(t, g, p)
		assert.Equal(t, []string{"r1"}, g.r.started())
		assert.NotContains(t, g.out.String(), "script read")
	})
}

func mustClass(t *testing.T, brief string) cardhdr.Class {
	t.Helper()
	c, why := cardhdr.ReadClass(brief)
	require.Empty(t, why)
	return c
}
