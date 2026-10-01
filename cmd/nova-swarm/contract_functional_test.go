//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// family's scripted child as the harness. The child does what the
// family's models do (the claude child clones into a directory of its own,
// branches, commits, pushes and runs gh pr create); the member pushes the
// child's commit to the card's branch on origin, opens the pull request with
// the child's title and body when the child asked for one, and finishes the
// card ok.
func TestTheScriptedChildEndToEnd(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none")
	}
	bin := builtSprint(t)
	for _, family := range cardcontract.Families {
		family := family
		t.Run(family, func(t *testing.T) {
			t.Parallel()
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
			sp := &execSprint{bin: bin, actor: "m1", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}
			rn := &nativeRunner{self: builtTool, sprintBin: bin, harness: harness, model: familyModel[family], root: root,
				slots: filepath.Join(root, "slots"), resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
				tokens: "unmetered", noWall: true, stderr: io.Discard}
			pu := newGitPusher(root, rn.slots, bin)
			pu.gh = gh
			out := &lockedBuf{}
			m := member.New(member.Config{As: "m1", Width: 1}, sp, rn, pu, out)
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
			pushed := strings.TrimSpace(runGit(t, origin, "rev-parse", "--verify", "-q", "refs/heads/sprint/a-1.w1"))
			require.Len(t, pushed, 40, "the card's branch is on origin:\n%s", story)
			assert.Equal(t, "the change", strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", pushed)), "origin holds the child's commit")
			assert.Contains(t, story, "pushed="+pushed+" to sprint/a-1.w1")

			args, err := os.ReadFile(filepath.Join(dir, "gh.args"))
			if family != "claude" {
				assert.True(t, os.IsNotExist(err), "%s asked for no pull request", family)
				return
			}
			require.NoError(t, err, "the member opened the pull request")
			assert.Equal(t, []string{"pr", "create", "--repo", origin, "--head", "sprint/a-1.w1", "--title", "The change", "--body-file", "-", "--base", "main"},
				strings.Split(strings.TrimSpace(string(args)), "\n"))
			body, err := os.ReadFile(filepath.Join(dir, "gh.body"))
			require.NoError(t, err)
			assert.Equal(t, "the body, line one\nline two", strings.TrimSpace(string(body)))
			assert.Contains(t, story, "pr=https://example.com/o/n/pull/42")
		})
	}
}
