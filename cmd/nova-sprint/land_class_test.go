package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real lander's base gate must see the first red class, not only build
// and vet (docs/SPEC-SPRINT.md, the base's class gate). The bench is a fake.
func TestARedClassOnTheBaseStopsLandings(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the class package", map[string]string{"internal/ci/ci.go": "package ci\n"})
	r.files("the base's unused helper", map[string]string{"unused.go": "package main\n\nfunc helper() {}\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	r.queued(map[string]string{"s1-1": r.card("s1-1", map[string]string{"other.go": "package main\n"})}, "s1-1")
	var runs []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(_ context.Context, _ string, dir string, commands [][]string, _ bool) (string, int, error) {
		for _, command := range commands {
			run := strings.Join(command, " ")
			runs = append(runs, run)
			if strings.Contains(run, "TestStaticcheckFindings") {
				body, err := os.ReadFile(filepath.Join(dir, "unused.go"))
				require.NoError(t, err)
				if strings.Contains(string(body), "func helper") {
					return gateMark + run + "\n--- FAIL: TestStaticcheckFindings (0.00s)\nunused.go:3:6: func helper is unused (U1000)\nFAIL github.com/mas-bandwidth/nova-tools/internal/ci\n", 1, nil
				}
			}
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, out+errs, "BASE RED ")
	assert.Contains(t, out+errs, "TestStaticcheckFindings")
	assert.NotContains(t, out+errs, "fact=conflict", "the base's failing class test cannot blame the card")
	assert.Equal(t, "stopped base", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NBaseRed))
	for _, want := range []string{"staticcheck", "U1000", "fix-red-staticcheck-main", "TestStaticcheckFindings"} {
		assert.Contains(t, inbox, want)
	}
	assert.Contains(t, strings.Join(runs, "\n"), "gofmt -l .")
	assert.Contains(t, strings.Join(runs, "\n"), "functional")
	_, out, errs = r.do("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out+errs, "still fails its tree gate", "a stopped stream re-checks its base")
	assert.NotContains(t, out+errs, "fact=conflict")
	assert.Equal(t, 1, strings.Count(r.ok("inbox"), sprint.NBaseRed), "the same red base has one judgment")
}

// gofmt's exit zero is not green when it prints. Both execution routes
// enforce that contract; these fake programs perform no Go work.
func TestTheClassGateReadsGofmtOutputLocallyAndOnTheBench(t *testing.T) {
	t.Parallel()
	for _, needsFormatting := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "formatting required"}[needsFormatting], func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			require.NoError(t, os.Mkdir(bin, 0700))
			body := "#!/bin/sh\nexit 0\n"
			if needsFormatting {
				body = "#!/bin/sh\nprintf 'bad.go\\n'\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(bin, "gofmt"), []byte(body), 0700))
			env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			l := &lander{}
			output, runErr := l.goRun(t.Context(), dir, []string{filepath.Join(bin, "gofmt"), "-l", "."})
			runErr = gateRunError([]string{"gofmt", "-l", "."}, output, runErr)
			if needsFormatting {
				assert.ErrorContains(t, runErr, "gofmt listed files")
				assert.Contains(t, output, "bad.go")
			} else {
				assert.NoError(t, runErr)
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-c", gateScript([][]string{{"gofmt", "-l", "."}, {"sh", "-c", "touch reached"}}))
			cmd.Dir, cmd.Env = dir, env
			out, err := cmd.CombinedOutput()
			if needsFormatting {
				assert.Error(t, err, string(out))
				assert.Contains(t, string(out), "bad.go")
				_, err = os.Stat(filepath.Join(dir, "reached"))
				assert.True(t, os.IsNotExist(err), "first red ends the suite")
			} else {
				assert.NoError(t, err, string(out))
				assert.FileExists(t, filepath.Join(dir, "reached"))
			}
		})
	}
}
