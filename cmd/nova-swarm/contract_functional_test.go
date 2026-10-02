//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// familyModel is a model id of each family the scripted child runs as: the member hands
// it to native, which picks the profile by it (cardcontract.FamilyOf).
var familyModel = map[string]string{
	"claude": "fake/claude-scripted", "openai": "fake/gpt-scripted", "gemini": "fake/gemini-scripted",
	"grok": "fake/grok-scripted", "deepseek": "fake/deepseek-scripted", "plain": "fake/fake-model",
}

// scriptFor is the scripted child of a family: its own under
// internal/cardcontract/testdata/scripted, else plain's.
func scriptFor(t *testing.T, family string) string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "cardcontract", "testdata", "scripted")
	b, err := os.ReadFile(filepath.Join(dir, family+".sh"))
	if family == "openai" {
		require.NoError(t, err, "the OpenAI profile requires its own scripted child under %s", dir)
		return string(b)
	}
	if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(dir, "plain.sh"))
	}
	require.NoError(t, err, "no scripted child for %s or plain under %s", family, dir)
	return string(b)
}

// TestTheScriptedChildEndToEnd is the harness every profile passes
// (docs/SPEC-CARD-CONTRACT.md, Writing a profile): one card on the mem twin,
// its repository a local bare origin, run by the member loop (in this
// process, one command at a time on the twin) and the built native, with the
// family's scripted child as the harness. The claude child clones into a
// directory of its own; the OpenAI child uses a linked worktree. Both commit,
// record a push and request a pull request. The member pushes the child's
// commit to the card's branch on origin, opens the pull request with the
// child's title and body, and finishes the card.
func TestTheScriptedChildEndToEnd(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none")
	}
	for _, family := range cardcontract.Families {
		family := family
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			scriptedChild(t, family, false)
		})
	}
}

// TestTheScriptedChildEndToEndInsideTheWall runs the claude and OpenAI children
// with the package's real wall binary on Darwin or Linux. TestMain builds that
// binary, so the test does not depend on nova-sandbox being installed on PATH.
func TestTheScriptedChildEndToEndInsideTheWall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("the repository builds real wall backends only on Darwin and Linux, not %s", runtime.GOOS)
	}
	for _, family := range []string{"claude", "openai"} {
		family := family
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			scriptedChild(t, family, true)
		})
	}
}

func realWallBackend(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, builtSandbox, "TestMain builds nova-sandbox for the real wall run")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, builtSandbox, "check")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	require.NoError(t, err, "nova-sandbox check failed; stdout=%q", string(out))
	fields := strings.Fields(string(out))
	require.GreaterOrEqual(t, len(fields), 2, "malformed nova-sandbox check output: %q", string(out))
	require.Equal(t, "CHECK", fields[0], "malformed nova-sandbox check output: %q", string(out))
	require.Equal(t, "OK", fields[1], "malformed nova-sandbox check output: %q", string(out))
	backend := ""
	for _, field := range fields[2:] {
		if strings.HasPrefix(field, "backend=") {
			backend = strings.TrimPrefix(field, "backend=")
			break
		}
	}
	require.NotEmpty(t, backend, "CHECK OK did not name a backend: %q", string(out))
	if backend == "none" {
		t.Skipf("the built nova-sandbox reports no supported backend on this kernel: %s", strings.TrimSpace(string(out)))
	}
	want := "sandbox-exec"
	if runtime.GOOS == "linux" {
		want = "landlock"
	}
	require.Equal(t, want, backend, "unexpected real wall backend: %q", string(out))
	return backend
}

// receiptPusher observes the completed native run before the member reports its
// finish and removes the checkout. It delegates the actual push unchanged.
type receiptPusher struct {
	member.Pusher
	before func(member.Packet)
}

func (p receiptPusher) Push(packet member.Packet, result member.Result) member.Push {
	p.before(packet)
	return p.Pusher.Push(packet, result)
}

// scriptedChild runs one family's scripted child through the member loop and
// native, walled or not, and asserts the finish (TestTheScriptedChildEndToEnd).
func scriptedChild(t *testing.T, family string, walled bool) {
	t.Helper()
	wallBackend := ""
	if walled {
		wallBackend = realWallBackend(t)
	}
	bin := builtSprint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)

	harness := filepath.Join(dir, "child.sh")
	require.NoError(t, testbin.WriteExecutable(harness, []byte(scriptFor(t, family)), 0o755))
	gh := filepath.Join(dir, "gh")
	require.NoError(t, testbin.WriteExecutable(gh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+dir+"/gh.args'\ncat > '"+dir+"/gh.body'\necho https://example.com/o/n/pull/42\n"), 0o755))

	first, rest, _ := strings.Cut(memberCard, "\n")
	brief := first + "\nbase-repo: " + origin + "\nBASE: main\n" + rest
	d := &memberDrive{t: t, addr: "mem:" + filepath.Join(dir, "sprint.twin"), bin: bin}
	d.must("init", "--members", "m1:1")
	d.must("add", "--stream", "a", "--count", "1", "--brief", brief)
	d.must("start")

	// The member loop runs in this process, its verbs and the test's ticks one
	// at a time: a mem twin is one command at a time (cmd/nova-sprint twin.go).
	// Each card is still one native child of the built binary.
	root := filepath.Join(dir, "m1")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	sp := d.worker()
	rn := &nativeRunner{self: builtTool, harness: harness, model: familyModel[family], root: root,
		slots: filepath.Join(root, "slots"), resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: !walled, stderr: io.Discard}
	if walled {
		require.NotEmpty(t, builtSandbox, "TestMain builds the wall binary for the walled profile run")
		rn.env = []string{"PATH=" + filepath.Dir(builtSandbox) + string(os.PathListSeparator) + os.Getenv("PATH")}
	}
	pu := newGitPusher(root, rn.slots)
	pu.gh = gh
	out := &lockedBuf{}
	var receiptMu sync.Mutex
	var receipt []byte
	var receiptErr error
	var receiptJob, receiptCwd os.FileInfo
	var receiptJobPath string
	var pusher member.Pusher = pu
	if walled {
		pusher = receiptPusher{Pusher: pu, before: func(packet member.Packet) {
			receiptMu.Lock()
			defer receiptMu.Unlock()
			launchDir := filepath.Join(root, "slots", launchName(packet))
			receiptJobPath = filepath.Join(launchDir, "jobs", packet.Card)
			receipt, receiptErr = os.ReadFile(filepath.Join(launchDir, "native.log"))
			if receiptErr != nil {
				return
			}
			receiptJob, receiptErr = os.Stat(receiptJobPath)
			if receiptErr != nil {
				return
			}
			_, cwd, reason := wallNamed(string(receipt))
			if reason == "" {
				receiptCwd, receiptErr = os.Stat(cwd)
			}
		}}
	}
	m := member.New(member.Config{As: "m1", Width: 1}, sp, rn, pusher, out)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("member output:\n%s", out.String())
		}
	})

	var story string
	for !strings.Contains(story, "m1 finished attempt 1") {
		require.NoError(t, ctx.Err(), "the card did not finish in time:\n%s", story)
		d.must("tick")
		_, err := m.Tick(time.Now())
		require.NoError(t, err)
		story = d.must("card", "a-1")
		time.Sleep(100 * time.Millisecond)
	}
	assert.NotContains(t, story, "FAILED", story)
	pushed := strings.TrimSpace(runGit(t, origin, "rev-parse", "--verify", "-q", "refs/heads/sprint/a-1.w1.g1.e0"))
	require.Len(t, pushed, 40, "the card's branch is on origin:\n%s", story)
	assert.Equal(t, "the change", strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", pushed)), "origin holds the child's commit")
	assert.Contains(t, story, "pushed="+pushed+" to sprint/a-1.w1.g1.e0")
	if walled {
		receiptMu.Lock()
		raw, readErr, jobInfo, cwdInfo, jobDir := receipt, receiptErr, receiptJob, receiptCwd, receiptJobPath
		receiptMu.Unlock()
		require.NotEmpty(t, raw, "the pusher captured the inner native log before cleanup")
		require.NoError(t, readErr)
		backend, cwd, reason := wallNamed(string(raw))
		require.Empty(t, reason, "native log contains no valid SANDBOX OK receipt: %s", raw)
		require.Equal(t, wallBackend, backend, "the framed child ran under the checked real backend")
		require.NotNil(t, jobInfo)
		require.NotNil(t, cwdInfo)
		require.True(t, os.SameFile(cwdInfo, jobInfo), "SANDBOX OK cwd %q must name job %q", cwd, jobDir)
		assert.NoDirExists(t, jobDir, "a successful launch leaves no checkout behind")
	}

	args, err := os.ReadFile(filepath.Join(dir, "gh.args"))
	if family != "claude" && family != "openai" {
		assert.True(t, os.IsNotExist(err), "%s asked for no pull request", family)
		return
	}
	require.NoError(t, err, "the member opened the pull request")
	assert.Equal(t, []string{"pr", "create", "--repo", origin, "--head", "sprint/a-1.w1.g1.e0", "--title", "The change", "--body-file", "-", "--base", "main"},
		strings.Split(strings.TrimSpace(string(args)), "\n"))
	body, err := os.ReadFile(filepath.Join(dir, "gh.body"))
	require.NoError(t, err)
	wantBody := "the body, line one\nline two"
	if family == "openai" {
		wantBody = "## Summary\n\nthe body, line two"
	}
	assert.Equal(t, wantBody, strings.TrimSpace(string(body)))
	assert.Contains(t, story, "pr=https://example.com/o/n/pull/42")
}
