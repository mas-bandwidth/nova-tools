//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_git_probe_functional_test.go runs the Linux block of roles/github-runner/tasks/git-probe.yml on localhost
// against a runner base directory in the test's temp dir, with fakes for pgrep (the runner idle or busy) and
// nova-secrets (which stands in for the fetch: it records where it ran and prints what git printed). Real git reads the
// clones' config. A clone that makes git abort fails the run naming the machine, runner, workspace and the step that
// moves it aside; a clean one passes; a busy runner's is skipped; a clone that is not both shallow and a promisor is not
// probed. The workspace is never changed and no copy is left.

const gitProbeBug = "error: BUG: builtin/pack-objects.c:4842: should_include_obj should only be called on existing objects\n" +
	"fatal: could not finish pack-objects to repack local links\nfatal: index-pack failed"

// gitProbeClone makes <base>/o-r-1/_work/<repo>/<repo>/.git, shallow and a promisor as asked, with the extra header
// actions/checkout writes; it returns the workspace and its config's text.
func gitProbeClone(t *testing.T, base, repo string, shallow, promisor bool) (string, string) {
	t.Helper()
	ws := filepath.Join(base, "o-r-1", "_work", repo, repo)
	g := filepath.Join(ws, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(g, "objects", "pack"), 0o755))
	config := "[remote \"origin\"]\n\turl = https://github.com/o/" + repo + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if promisor {
		config += "\tpromisor = true\n\tpartialclonefilter = tree:0\n"
	}
	config += "[http \"https://github.com/\"]\n\textraheader = AUTHORIZATION: basic placeholder\n"
	require.NoError(t, os.WriteFile(filepath.Join(g, "config"), []byte(config), 0o644))
	if shallow {
		require.NoError(t, os.WriteFile(filepath.Join(g, "shallow"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644))
	}
	return ws, config
}

func TestFunctionalRunnerGitProbe(t *testing.T) {
	t.Parallel()
	block := runnerTask(t, taskGroups(t, "roles/github-runner/tasks/git-probe.yml")[0], "the runners' workspace clones, probed on a copy (Linux)")
	tasks := mapsOf(t, block["block"])
	for name, c := range map[string]struct {
		busy, shallow, promisor bool
		fetch                   string
		fails                   bool
		verdict                 string
		fetched                 bool
	}{
		"found":        {shallow: true, promisor: true, fetch: "echo '" + gitProbeBug + "' >&2; exit 128", fails: true, verdict: "BUG", fetched: true},
		"clean":        {shallow: true, promisor: true, fetch: "exit 0", verdict: "clean", fetched: true},
		"busy":         {busy: true, shallow: true, promisor: true, fetch: "exit 0", verdict: "skipped=busy"},
		"not shallow":  {promisor: true, fetch: "exit 0", verdict: "not-probed"},
		"not promisor": {shallow: true, fetch: "exit 0", verdict: "not-probed"},
		"fetch fails":  {shallow: true, promisor: true, fetch: "echo 'fatal: Authentication failed' >&2; exit 128", fails: true, verdict: "error=fetch-rc-128", fetched: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			base, scratch, bin := filepath.Join(dir, "actions-runner"), filepath.Join(dir, "home", ".cache", "nova-git-probe"), filepath.Join(dir, "nova-bin")
			ws, config := gitProbeClone(t, base, "r", c.shallow, c.promisor)
			require.NoError(t, os.MkdirAll(bin, 0o755))
			calls := filepath.Join(dir, "fetch.calls")
			// The fetch runs in a copy holding the clone, its extra header removed, under the scratch directory.
			secrets := "#!/bin/sh\necho \"$PWD|$*\" >> " + calls + "\n[ -d .git ] || exit 7\n" +
				"grep -q extraheader .git/config && exit 8\n" + c.fetch + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-secrets"), []byte(secrets), 0o755))
			worker := base + "/o-r-1/bin/[R]unner.Worker"
			pgrep := `[ "$1 $2" = "-f ` + worker + `" ] || { echo "unexpected pgrep $*" >&2; exit 3; }` + "\nexit 1"
			if c.busy {
				pgrep = "echo 4242"
			}
			out, err := runTasksLocally(t, tasks, map[string]any{
				"github_runner_base_dir": base, "github_runner_git_probe_scratch": scratch, "nova_bin_dir": bin,
			}, map[string]string{"pgrep": pgrep})
			line := "GIT-PROBE localhost o-r-1 " + ws + " " + c.verdict
			assert.Contains(t, out, line)
			if c.fails {
				require.Error(t, err, out)
				assert.Contains(t, out, "GIT-PROBE localhost:")
			} else {
				require.NoError(t, err, out)
			}
			if name == "found" {
				assert.Contains(t, out, "makes git abort in actions/checkout's fetch")
				assert.Contains(t, out, "GIT-PROBE-FIX localhost o-r-1: pgrep -f '"+worker+"' || mv '"+ws+"' '"+ws+".git-bug'")
			} else {
				assert.NotContains(t, out, "GIT-PROBE-FIX")
			}
			if name == "fetch fails" {
				assert.Contains(t, out, "the probe could not run on")
				assert.NotContains(t, out, "Authentication failed", "the fetch's output is searched, never printed")
			}
			got, _ := os.ReadFile(calls) // ignored: absent when nothing was fetched, compared below
			if !c.fetched {
				assert.Empty(t, string(got), "nothing is fetched")
			} else {
				lines := strings.Split(strings.TrimSpace(string(got)), "\n")
				require.Len(t, lines, 1)
				where, args, _ := strings.Cut(lines[0], "|")
				assert.True(t, strings.HasPrefix(where, scratch+"/probe."), "the fetch ran in %s, not in a copy under the scratch directory", where)
				assert.Contains(t, args, "exec --only GH_GATE_TOKEN -- git -c credential.helper= -c credential.helper=!f() { test \"$1\" = get")
				assert.Contains(t, args, "fetch --prune --no-recurse-submodules --filter=tree:0 --unshallow origin +refs/heads/*:refs/remotes/origin/* +refs/tags/*:refs/tags/*")
			}
			b, err := os.ReadFile(filepath.Join(ws, ".git", "config"))
			require.NoError(t, err)
			assert.Equal(t, config, string(b), "the workspace is never changed")
			left, _ := os.ReadDir(scratch) // ignored: absent when nothing was copied
			assert.Empty(t, left, "no copy is left")
		})
	}
}
