//go:build functional

package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_git_probe_functional_test.go runs roles/github-runner/tasks/git-probe.yml on localhost against a runner
// base directory in the test's temp dir. TestFunctionalRunnerGitProbe runs the Linux block with fakes for pgrep (the
// runner idle or busy) and nova-secrets (which stands in for the fetch: it records where it ran and prints what git
// printed), and for cp, df, flock or timeout where a case needs them to fail. A clone that makes git abort fails the
// run naming the machine, runner, workspace and the step that moves it aside; a clean one passes; a busy runner's is
// skipped and named; a clone that is not both shallow and a promisor is not probed, and one moved aside is reported as
// such; every other way the probe cannot check a clone is red with its reason. The workspace is never changed and no
// copy is left. TestFunctionalRunnerGitProbeRealFetch runs the same block with real git against a local bare
// repository. The block runs under /bin/dash where there is one, as on the Debian and Ubuntu runners, where /bin/sh is
// dash: dash runs no EXIT trap when a signal it does not trap ends it.

// gitProbeRig is one runner base directory with the clone <base>/o-r-1/_work/r/r, a scratch directory and a bin
// directory for the fake nova-secrets.
type gitProbeRig struct{ dir, base, scratch, bin, calls, ws, config, worker string }

func newGitProbeRig(t *testing.T, shallow, promisor bool) *gitProbeRig {
	t.Helper()
	dir := t.TempDir()
	r := &gitProbeRig{dir: dir, base: filepath.Join(dir, "actions-runner"), scratch: filepath.Join(dir, "home", ".cache", "nova-git-probe"),
		bin: filepath.Join(dir, "nova-bin"), calls: filepath.Join(dir, "fetch.calls")}
	require.NoError(t, os.MkdirAll(r.bin, 0o755))
	r.worker = r.base + "/o-r-1/bin/[R]unner.Worker"
	r.ws, r.config = r.clone(t, "o-r-1", "r", shallow, promisor)
	return r
}

// clone makes <base>/<runner>/_work/<repo>/<repo>/.git: refs, a pack, the extra header actions/checkout writes, and
// shallow and a promisor as asked. It returns the workspace and its config's text.
func (r *gitProbeRig) clone(t *testing.T, runner, repo string, shallow, promisor bool) (string, string) {
	t.Helper()
	ws := filepath.Join(r.base, runner, "_work", repo, repo)
	g := filepath.Join(ws, ".git")
	for _, d := range []string{"objects/pack", "refs/heads", "refs/tags"} {
		require.NoError(t, os.MkdirAll(filepath.Join(g, d), 0o755))
	}
	files := map[string]string{
		"HEAD": "ref: refs/heads/main\n", "packed-refs": "# pack-refs with: peeled fully-peeled sorted\n",
		"refs/heads/main": "0123456789abcdef0123456789abcdef01234567\n", "objects/pack/pack-1.pack": "PACK", "objects/pack/pack-1.idx": "IDX",
	}
	if shallow {
		files["shallow"] = "0123456789abcdef0123456789abcdef01234567\n"
	}
	config := "[remote \"origin\"]\n\turl = https://github.com/o/" + repo + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if promisor {
		config += "\tpromisor = true\n\tpartialclonefilter = tree:0\n"
	}
	files["config"] = config + "[http \"https://github.com/\"]\n\textraheader = AUTHORIZATION: basic placeholder\n"
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(g, name), []byte(body), 0o644))
	}
	return ws, files["config"]
}

// snapshot is every entry under a workspace's .git with its mode, size, modification time and contents.
func snapshot(t *testing.T, ws string) []string {
	t.Helper()
	var out []string
	g := filepath.Join(ws, ".git")
	require.NoError(t, filepath.WalkDir(g, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %v %d %d", strings.TrimPrefix(p, g), info.Mode(), info.Size(), info.ModTime().UnixNano())
		if !e.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(b)
		}
		out = append(out, line)
		return nil
	}))
	return out
}

// secrets writes the fake nova-secrets: it records where it ran and its arguments, refuses unless it runs in a copy
// holding the clone with its extra header removed, then runs fetch.
func (r *gitProbeRig) secrets(t *testing.T, fetch string) {
	t.Helper()
	s := "#!/bin/sh\necho \"$PWD|$*\" >> " + r.calls + "\n[ -d .git ] || exit 7\ngrep -q extraheader .git/config && exit 8\n" + fetch + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(r.bin, "nova-secrets"), []byte(s), 0o755))
}

// idle is a pgrep that checks it was asked for this runner's worker and finds none.
func (r *gitProbeRig) idle() string {
	return `[ "$1 $2" = "-f ` + r.worker + `" ] || { echo "unexpected pgrep $*" >&2; exit 3; }` + "\nexit 1"
}

// run runs tasks with fakes and any more vars; a machine without flock (macOS) gets one that takes the lock.
func (r *gitProbeRig) run(t *testing.T, tasks []map[string]any, fakes map[string]string, more ...map[string]any) (string, error) {
	t.Helper()
	if _, ok := fakes["flock"]; !ok {
		if _, err := exec.LookPath("flock"); err != nil {
			fakes["flock"] = "exit 0"
		}
	}
	vars := map[string]any{"github_runner_base_dir": r.base, "github_runner_git_probe_scratch": r.scratch, "nova_bin_dir": r.bin}
	for _, m := range more {
		for k, v := range m {
			vars[k] = v
		}
	}
	return runTasksLocally(t, tasks, vars, fakes)
}

func (r *gitProbeRig) fetches(t *testing.T) []string {
	t.Helper()
	got, _ := os.ReadFile(r.calls) // ignored: absent when nothing was fetched
	if s := strings.TrimSpace(string(got)); s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

func (r *gitProbeRig) left(t *testing.T) []string {
	t.Helper()
	var left []string
	entries, _ := os.ReadDir(r.scratch) // ignored: absent when the probe stopped before making it
	for _, e := range entries {
		left = append(left, e.Name())
	}
	return left
}

// gitProbeLinuxBlock is the Linux block's tasks, its shell /bin/dash where there is one.
func gitProbeLinuxBlock(t *testing.T) []map[string]any {
	t.Helper()
	block := runnerTask(t, taskGroups(t, "roles/github-runner/tasks/git-probe.yml")[0], "the runners' workspace clones, probed on a copy (Linux)")
	tasks := mapsOf(t, block["block"])
	if _, err := os.Stat("/bin/dash"); err == nil {
		args, _ := tasks[0]["args"].(map[string]any)
		require.Equal(t, "/bin/sh", args["executable"])
		args["executable"] = "/bin/dash"
	}
	return tasks
}

func TestFunctionalRunnerGitProbe(t *testing.T) {
	t.Parallel()
	const bug = "BUG: builtin/pack-objects.c:4842: should_include_obj should only be called on existing objects\n" +
		"fatal: could not finish pack-objects to repack local links\nfatal: index-pack failed"
	type probeCase struct {
		notShallow, notPromisor bool
		fetch                   string
		fakes                   func(r *gitProbeRig) map[string]string
		setup                   func(t *testing.T, r *gitProbeRig)
		fails, fetched          bool
		verdict                 string   // the clone's line ends in this, after the workspace
		why                     string   // its GIT-PROBE-WHY line, if any
		want, not               []string // more text the output has, and does not have
		left                    []string // what the scratch directory holds after the run, beside .lock
		noScratch               bool     // the probe stops before it makes the scratch directory
		changes                 bool     // the case changes the live clone itself (the fake cp)
		vars                    map[string]any
	}
	busyAt := func(n int) func(r *gitProbeRig) map[string]string {
		return func(r *gitProbeRig) map[string]string {
			count := filepath.Join(r.dir, "pgrep.n")
			return map[string]string{"pgrep": `c=$(cat ` + count + ` 2>/dev/null || echo 0); c=$((c+1)); echo $c > ` + count + "\n" +
				`[ $c -ge ` + string(rune('0'+n)) + ` ] && { echo 4242; exit 0; }` + "\n" + r.idle()}
		}
	}
	// signalled: cp copies, then sends the probe's shell sig, as a hangup, Ctrl-C or kill would mid-run.
	signalled := func(sig string) probeCase {
		return probeCase{fetch: "exit 0", fails: true, fakes: func(*gitProbeRig) map[string]string {
			return map[string]string{"cp": "/bin/cp \"$@\"; kill -" + sig + " $PPID; exit 0"}
		}, want: []string{"the probe stopped, rc="}}
	}
	cases := map[string]probeCase{
		"found": {fetch: "echo '" + bug + "' >&2; exit 128", fails: true, fetched: true, verdict: "BUG",
			want: []string{"makes git abort in actions/checkout's fetch", "done bug=1 clean=0 skipped-busy=-"}},
		"found by signal 6": {fetch: "echo 'error: pack-objects died of signal 6' >&2; echo 'fatal: index-pack failed' >&2; exit 128",
			fails: true, fetched: true, verdict: "BUG"},
		"clean": {fetch: "exit 0", fetched: true, verdict: "clean", want: []string{"done bug=0 clean=1 skipped-busy=-"}},
		"busy": {fetch: "exit 0", fakes: busyAt(1), verdict: "skipped=busy",
			want: []string{"done bug=0 clean=0 skipped-busy=o-r-1"}},
		"busy after the copy": {fetch: "exit 0", fakes: busyAt(2), verdict: "skipped=busy",
			want: []string{"done bug=0 clean=0 skipped-busy=o-r-1"}},
		"changed during the copy": {fetch: "exit 0", changes: true, verdict: "skipped=busy",
			fakes: func(*gitProbeRig) map[string]string {
				return map[string]string{"cp": "/bin/cp \"$@\"; rc=$?; touch -t 203001010000 \"$2/HEAD\"; exit $rc"}
			}, want: []string{"done bug=0 clean=0 skipped-busy=o-r-1"}},
		"pgrep fails": {fetch: "exit 0", fakes: func(*gitProbeRig) map[string]string { return map[string]string{"pgrep": "exit 2"} },
			fails: true, verdict: "error=pgrep-rc-2"},
		"not shallow":  {notShallow: true, fetch: "exit 0", verdict: "not-probed"},
		"not promisor": {notPromisor: true, fetch: "exit 0", verdict: "not-probed"},
		"auth": {fetch: "echo \"fatal: unable to access 'https://x-access-token:ghs_secret123@github.com/o/r/': The requested URL returned error: 403\" >&2; exit 128",
			fails: true, fetched: true, verdict: "error=auth",
			why: "repo=https://github.com/o/r: fatal: unable to access 'https://REDACTED@github.com/o/r/': The requested URL returned error: 403",
			not: []string{"ghs_secret123"}},
		"network": {fetch: "echo \"fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com\" >&2; exit 128",
			fails: true, fetched: true, verdict: "error=network", why: "repo=https://github.com/o/r: fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com"},
		"secrets refuses": {fetch: "echo 'nova-secrets: GH_GATE_TOKEN is not granted to this seat' >&2; exit 125", fails: true, fetched: true,
			verdict: "error=secrets", why: "rc=125: nova-secrets: GH_GATE_TOKEN is not granted to this seat",
			want: []string{"nova-secrets exec --only GH_GATE_TOKEN -- true"}},
		"timeout": {fetch: "exit 0", fakes: func(*gitProbeRig) map[string]string { return map[string]string{"timeout": "exit 124"} },
			fails: true, verdict: "error=fetch-timeout"},
		"copy fails": {fetch: "exit 0", fakes: func(*gitProbeRig) map[string]string { return map[string]string{"cp": `mkdir -p "$3/objects"; exit 1`} },
			fails: true, verdict: "error=copy"},
		"no disk": {fetch: "exit 0", fakes: func(*gitProbeRig) map[string]string {
			return map[string]string{"df": "echo 'Filesystem 1024-blocks Used Available Capacity Mounted on'; echo '/dev/x 100 99 0 100% /'"}
		}, fails: true, verdict: "skipped=disk", why: "free=0K need=", want: []string{"not checked:"}},
		"locked": {fetch: "exit 0", fakes: func(*gitProbeRig) map[string]string { return map[string]string{"flock": "exit 1"} },
			fails: true, want: []string{"skipped=locked", "another probe holds"}, not: []string{"GIT-PROBE localhost done"}},
		"moved aside": {fetch: "exit 0", fetched: true, verdict: "clean",
			setup: func(t *testing.T, r *gitProbeRig) {
				aside, _ := r.clone(t, "o-r-1", "r", true, true)
				require.NoError(t, os.Rename(aside, r.ws+".git-bug"))
				r.ws, r.config = r.clone(t, "o-r-1", "r", true, true)
			}, want: []string{"/_work/r/r.git-bug moved-aside", "done bug=0 clean=1"}, not: []string{".git-bug.git-bug"}},
		"stale copy": {fetch: "exit 0", fetched: true, verdict: "clean", left: []string{"keep", "probe.new"},
			setup: func(t *testing.T, r *gitProbeRig) {
				for d, at := range map[string]time.Time{"probe.old": time.Unix(1000, 0), "keep": time.Unix(1000, 0), "probe.new": time.Unix(4102444800, 0)} {
					p := filepath.Join(r.scratch, d)
					require.NoError(t, os.MkdirAll(filepath.Join(p, ".git"), 0o755))
					require.NoError(t, os.Chtimes(p, at, at))
				}
				old := filepath.Join(r.scratch, ".stamp.old")
				require.NoError(t, os.WriteFile(old, nil, 0o600))
				require.NoError(t, os.Chtimes(old, time.Unix(1000, 0), time.Unix(1000, 0)))
			}},
		// A repository whose url names nova-secrets is refused as any other is: the remedy is not nova-secrets'.
		"auth for a repo named nova-secrets": {fetch: "echo \"fatal: unable to access 'https://github.com/o/nova-secrets/': The requested URL returned error: 403\" >&2; exit 128",
			fails: true, fetched: true, verdict: "error=auth",
			why: "repo=https://github.com/o/r: fatal: unable to access 'https://github.com/o/nova-secrets/': The requested URL returned error: 403"},
		// Runners of a login of their own: nothing is read or copied, and the run is red so it is not read as clean.
		"runner login": {fetch: "exit 0", fails: true, noScratch: true,
			vars: map[string]any{"github_runner_other": true, "github_runner_user": "ghrunner"},
			want: []string{" error=runner-login\"", "the runners run as ghrunner, not the fleet login"},
			not:  []string{"GIT-PROBE localhost done"}},
		"hangup":    signalled("HUP"),
		"interrupt": signalled("INT"),
		"terminate": signalled("TERM"),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newGitProbeRig(t, !c.notShallow, !c.notPromisor)
			r.secrets(t, c.fetch)
			fakes := map[string]string{"pgrep": r.idle()}
			if c.fakes != nil {
				fakes = c.fakes(r)
				if _, ok := fakes["pgrep"]; !ok {
					fakes["pgrep"] = r.idle()
				}
			}
			if c.setup != nil {
				c.setup(t, r)
			}
			before := snapshot(t, r.ws)
			out, err := r.run(t, gitProbeLinuxBlock(t), fakes, c.vars)
			if c.verdict != "" {
				assert.Contains(t, out, "GIT-PROBE localhost o-r-1 "+r.ws+" "+c.verdict+"\"")
			}
			if c.why != "" {
				assert.Contains(t, out, "GIT-PROBE-WHY localhost o-r-1: "+c.why)
			}
			if c.fails {
				require.Error(t, err, out)
				assert.Contains(t, out, "GIT-PROBE localhost:")
			} else {
				require.NoError(t, err, out)
			}
			for _, s := range c.want {
				assert.Contains(t, out, s)
			}
			for _, s := range c.not {
				assert.NotContains(t, out, s)
			}
			if c.verdict == "BUG" {
				assert.Contains(t, out, "GIT-PROBE-FIX localhost o-r-1: pgrep -f '"+r.worker+"' || mv '"+r.ws+"' '"+r.ws+".git-bug'")
			} else {
				assert.NotContains(t, out, "GIT-PROBE-FIX")
			}
			if name != "secrets refuses" {
				assert.NotContains(t, out, r.bin+"/nova-secrets exec --only GH_GATE_TOKEN -- true", "nova-secrets is named only when exec failed")
			}
			fetches := r.fetches(t)
			if !c.fetched {
				assert.Empty(t, fetches, "nothing is fetched")
			} else {
				require.Len(t, fetches, 1)
				where, args, _ := strings.Cut(fetches[0], "|")
				assert.True(t, strings.HasPrefix(where, r.scratch+"/probe."), "the fetch ran in %s, not in a copy under the scratch directory", where)
				assert.Contains(t, args, "exec --only GH_GATE_TOKEN -- git -c credential.helper= -c credential.helper=!f() { test \"$1\" = get")
				assert.Contains(t, args, "fetch --prune --no-recurse-submodules --filter=tree:0 --unshallow origin +refs/heads/*:refs/remotes/origin/* +refs/tags/*:refs/tags/*")
			}
			if !c.changes {
				assert.Equal(t, before, snapshot(t, r.ws), "the workspace is never changed")
			}
			if c.noScratch {
				assert.Empty(t, r.left(t), "nothing is made")
				return
			}
			assert.Equal(t, append([]string{".lock"}, c.left...), r.left(t), "no copy and no stamp is left; only an earlier run's copy and stamp are removed")
		})
	}
}

// Two runners, two clones each: o-r-1 is busy (named once in the done line), o-r-2 idle with one clone that aborts and
// one clean. Each probed clone has its own copy, the counters add up across clones, and the run is red for the one BUG.
func TestFunctionalRunnerGitProbeTwoRunners(t *testing.T) {
	t.Parallel()
	r := newGitProbeRig(t, true, true)
	ws := map[string]string{"o-r-1 r": r.ws}
	for _, c := range [][2]string{{"o-r-1", "b"}, {"o-r-2", "q"}, {"o-r-2", "z"}} {
		ws[c[0]+" "+c[1]], _ = r.clone(t, c[0], c[1], true, true)
	}
	before := map[string][]string{}
	for k, w := range ws {
		before[k] = snapshot(t, w)
	}
	r.secrets(t, "grep -q 'o/q$' .git/config && { echo 'BUG: builtin/pack-objects.c:4842: should_include_obj' >&2; exit 128; }\nexit 0")
	pgrep := "case \"$2\" in *'/o-r-1/bin/[R]unner.Worker') echo 4242; exit 0 ;; *'/o-r-2/bin/[R]unner.Worker') exit 1 ;; esac\nexit 3"
	out, err := r.run(t, gitProbeLinuxBlock(t), map[string]string{"pgrep": pgrep})
	require.Error(t, err, out)
	assert.Contains(t, out, "GIT-PROBE localhost o-r-1 "+ws["o-r-1 r"]+" skipped=busy")
	assert.Contains(t, out, "GIT-PROBE localhost o-r-1 "+ws["o-r-1 b"]+" skipped=busy")
	assert.Contains(t, out, "GIT-PROBE localhost o-r-2 "+ws["o-r-2 q"]+" BUG")
	assert.Contains(t, out, "GIT-PROBE localhost o-r-2 "+ws["o-r-2 z"]+" clean")
	assert.Contains(t, out, "GIT-PROBE localhost done bug=1 clean=1 skipped-busy=o-r-1\"")
	assert.Contains(t, out, "GIT-PROBE-FIX localhost o-r-2: pgrep -f '"+r.base+"/o-r-2/bin/[R]unner.Worker' || mv '"+ws["o-r-2 q"]+"'")
	fetches := r.fetches(t)
	require.Len(t, fetches, 2)
	first, _, _ := strings.Cut(fetches[0], "|")
	second, _, _ := strings.Cut(fetches[1], "|")
	assert.NotEqual(t, first, second, "each clone has its own copy")
	for k, w := range ws {
		assert.Equal(t, before[k], snapshot(t, w), "%s is never changed", k)
	}
	assert.Equal(t, []string{".lock"}, r.left(t))
}

// A runner base directory with a blank in its path: the clone that aborts is still found and the run is red.
func TestFunctionalRunnerGitProbePathWithBlanks(t *testing.T) {
	t.Parallel()
	r := newGitProbeRig(t, true, true)
	spaced := filepath.Join(r.dir, "actions runner")
	require.NoError(t, os.Rename(r.base, spaced))
	r.ws = strings.Replace(r.ws, r.base, spaced, 1)
	r.worker = strings.Replace(r.worker, r.base, spaced, 1)
	r.base = spaced
	r.secrets(t, "echo 'BUG: builtin/pack-objects.c:4842: should_include_obj' >&2; exit 128")
	out, err := r.run(t, gitProbeLinuxBlock(t), map[string]string{"pgrep": r.idle()})
	require.Error(t, err, out)
	assert.Contains(t, out, "GIT-PROBE localhost o-r-1 "+r.ws+" BUG\"")
	assert.Contains(t, out, "makes git abort in actions/checkout's fetch")
}

// A scratch directory that cannot be made stops the probe: rc != 0 is red, with the reason.
func TestFunctionalRunnerGitProbeStopsWithoutScratch(t *testing.T) {
	t.Parallel()
	r := newGitProbeRig(t, true, true)
	r.secrets(t, "exit 0")
	file := filepath.Join(r.dir, "a-file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	r.scratch = filepath.Join(file, "scratch")
	out, err := r.run(t, gitProbeLinuxBlock(t), map[string]string{"pgrep": r.idle()})
	require.Error(t, err, out)
	assert.Contains(t, out, "the probe stopped, rc=1")
	assert.Contains(t, out, "cannot write the scratch directory "+r.scratch)
	assert.Empty(t, r.fetches(t))
}

// The whole task file: a Linux host is probed; any other prints the skip and probes nothing.
func TestFunctionalRunnerGitProbeIsLinuxOnly(t *testing.T) {
	t.Parallel()
	r := newGitProbeRig(t, true, true)
	r.secrets(t, "exit 0")
	out, err := r.run(t, taskGroups(t, "roles/github-runner/tasks/git-probe.yml")[0], map[string]string{"pgrep": r.idle()})
	require.NoError(t, err, out)
	if runtime.GOOS == "linux" {
		assert.Contains(t, out, "GIT-PROBE localhost done bug=0 clean=1")
		assert.NotContains(t, out, "the probe is for Linux runners, this is")
		return
	}
	assert.Contains(t, out, "GIT-PROBE localhost skipped: the probe is for Linux runners, this is ")
	assert.NotContains(t, out, "GIT-PROBE localhost done")
	assert.Empty(t, r.fetches(t))
}

// gitIn runs real git in dir; the test's own config only (no system or global file, no prompt).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=bench-1", "-c", "user.email=bench-1@example.invalid", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// Real git: the workspace is a shallow tree:0 clone of a local bare repository over file:// (the origin allows
// filters), with a second branch and a tag the clone has not fetched. nova-secrets is a stand-in for exec: it puts a
// token into the environment, asks the probe's own credential helper through real git what it answers, runs the
// probe's fetch, and records the copy's state after it. The fetch unshallows the copy, fetches every branch and tag by
// the refspecs, keeps the filter, and the helper answers with the token from the environment. No network is used.
func TestFunctionalRunnerGitProbeRealFetch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine")
	}
	r := newGitProbeRig(t, true, true)
	src, bare := filepath.Join(r.dir, "src"), filepath.Join(r.dir, "origin.git")
	require.NoError(t, os.MkdirAll(src, 0o755))
	gitIn(t, src, "init", "-q")
	for i, f := range []string{"a", "b", "c"} {
		require.NoError(t, os.MkdirAll(filepath.Join(src, f), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, f, "x"), []byte(f), 0o644))
		gitIn(t, src, "add", "-A")
		gitIn(t, src, "commit", "-q", "-m", f)
		if i == 0 {
			gitIn(t, src, "tag", "v1")
		}
	}
	gitIn(t, src, "branch", "side")
	gitIn(t, r.dir, "clone", "-q", "--bare", src, bare)
	gitIn(t, bare, "config", "uploadpack.allowFilter", "true")
	gitIn(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	require.NoError(t, os.RemoveAll(r.ws))
	require.NoError(t, os.MkdirAll(filepath.Dir(r.ws), 0o755))
	gitIn(t, filepath.Dir(r.ws), "clone", "-q", "--depth=1", "--single-branch", "--no-tags", "--filter=tree:0", "file://"+bare, r.ws)
	require.FileExists(t, filepath.Join(r.ws, ".git", "shallow"))
	require.Equal(t, "true", gitIn(t, r.ws, "config", "--get", "remote.origin.promisor"))
	gitIn(t, r.ws, "config", "http.https://github.com/.extraheader", "AUTHORIZATION: basic placeholder")
	before := snapshot(t, r.ws)

	rec := filepath.Join(r.dir, "copy.state")
	secrets := `#!/bin/sh
[ "$1 $2 $3 $4" = "exec --only GH_GATE_TOKEN --" ] || { echo "nova-secrets: unexpected $*" >&2; exit 2; }
shift 4
export GH_GATE_TOKEN=token-for-test GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
for a in "$@"; do case $a in credential.helper=!*) helper=${a#credential.helper=} ;; esac; done
printf 'protocol=https\nhost=github.com\n\n' | git -c credential.helper= -c credential.helper="$helper" credential fill > ` + rec + `.cred
"$@"
rc=$?
{ git rev-parse --is-shallow-repository; git config --get remote.origin.partialclonefilter; git for-each-ref --format='%(refname)'; } > ` + rec + `
exit $rc
`
	require.NoError(t, os.WriteFile(filepath.Join(r.bin, "nova-secrets"), []byte(secrets), 0o755))
	out, err := r.run(t, gitProbeLinuxBlock(t), map[string]string{"pgrep": r.idle()})
	require.NoError(t, err, out)
	assert.Contains(t, out, "GIT-PROBE localhost o-r-1 "+r.ws+" clean")

	cred, err := os.ReadFile(rec + ".cred")
	require.NoError(t, err)
	assert.Contains(t, string(cred), "username=x-access-token\npassword=token-for-test\n", "the helper answers from the environment")
	state, err := os.ReadFile(rec)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(state)), "\n")
	require.GreaterOrEqual(t, len(lines), 2, string(state))
	assert.Equal(t, "false", lines[0], "the copy is unshallowed")
	assert.Equal(t, "tree:0", lines[1], "the filter is kept")
	for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/side", "refs/tags/v1"} {
		assert.True(t, slices.Contains(lines[2:], ref), "%s fetched by the refspecs: %v", ref, lines[2:])
	}
	assert.Equal(t, before, snapshot(t, r.ws), "the workspace is never changed")
	assert.Equal(t, []string{".lock"}, r.left(t), "no copy is left")
}
