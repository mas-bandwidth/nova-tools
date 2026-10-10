package release

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wordAfter is the first word after token on the last line of stream that
// opens with it, the verdict line of a verb (docs/STANDARD.md section 2, the
// status word leads every line).
func wordAfter(stream, token string) string {
	word := ""
	for _, line := range strings.Split(stream, "\n") {
		if rest, ok := strings.CutPrefix(line, token+" "); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				word = strings.TrimSuffix(f[0], ":")
			}
		}
	}
	return word
}

// TestStatusGrammar runs each release verb to one OK, one REFUSED and one
// FAILED outcome through Run and pins the word after the verb token together
// with the exit code (docs/STANDARD.md section 2: OK at 0, FAILED at 1,
// REFUSED at 2). The cut and install verdict lines carry no OK word; their
// rows pin the word those lines print.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	type outcome struct {
		code      int
		out, errs string
	}
	run := func(args []string, deps Deps) outcome {
		var o, e bytes.Buffer
		code := Run("nova-update", args, &o, &e, deps)
		return outcome{code, o.String(), e.String()}
	}
	absent := func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }
	adoptArgs := func(t *testing.T, machines string) []string {
		return []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", machinesFile(t, machines),
			"--ssh", "/usr/bin/ssh", "--from", built(t, "v0.16.0", "", "nova-bus", "nova-update"),
			"--bin", "/home/nova/.local/bin", "--dest", "/home/nova/nova-bench/build"}
	}
	pullArgs := func(t *testing.T, out, changelog string) []string {
		return []string{"pull", "--version", "v0.16.0", "--out", out, "--changelog", changelog,
			"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh", "--dest", "~/build",
			"--reason", "it shipped a sealed key", "--platform", "linux-amd64"}
	}

	for _, tc := range []struct {
		name   string
		run    func(t *testing.T) outcome
		stderr bool
		token  string
		word   string
		code   int
	}{
		{"cut ok", func(t *testing.T) outcome {
			return run(cutArgs(changelogIn(t, t.TempDir())), cutDeps(t, cutForge()))
		}, false, "RELEASE CUT", "version=v0.16.0", 0},
		{"cut refused", func(t *testing.T) outcome {
			return run([]string{"cut"}, Deps{})
		}, true, "CUT", "REFUSED", 2},
		{"cut failed", func(t *testing.T) outcome {
			f := cutForge()
			f.failTag = fmt.Errorf("the forge said no")
			return run(cutArgs(changelogIn(t, t.TempDir())), cutDeps(t, f))
		}, true, "CUT", "FAILED", 1},

		{"build ok", func(t *testing.T) outcome {
			return run([]string{"build", "--version", "v0.16.0", "--out", t.TempDir(), "--source", sourceTree(t)},
				Deps{Toolchain: &fakeToolchain{}})
		}, false, "RELEASE BUILD", "OK", 0},
		{"build refused", func(t *testing.T) outcome {
			return run([]string{"build"}, Deps{})
		}, true, "BUILD", "REFUSED", 2},
		{"build failed", func(t *testing.T) outcome {
			return run([]string{"build", "--version", "v0.16.0", "--out", t.TempDir(), "--source", sourceTree(t)},
				Deps{Toolchain: &fakeToolchain{fail: "nova-bus"}})
		}, true, "BUILD", "FAILED", 1},

		{"install ok", func(t *testing.T) outcome {
			return run([]string{"install", "--from", built(t, "v0.16.0", "", "nova-bus"), "--version", "v0.16.0",
				"--bin", t.TempDir()}, Deps{VersionOf: absent})
		}, false, "RELEASE", "INSTALLED", 0},
		{"install refused", func(t *testing.T) outcome {
			return run([]string{"install"}, Deps{})
		}, true, "INSTALL", "REFUSED", 2},
		{"install failed", func(t *testing.T) outcome {
			goos, _ := platformOf(t, "")
			bin := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(bin, ToolFile("nova-bus", goos)), 0o755))
			return run([]string{"install", "--from", built(t, "v0.16.0", "", "nova-bus"), "--version", "v0.16.0",
				"--bin", bin}, Deps{VersionOf: absent})
		}, true, "INSTALL", "FAILED", 1},

		{"adopt ok", func(t *testing.T) outcome {
			return run(adoptArgs(t, "hulk\n"), Deps{SSH: &fakeSSH{answer: map[string]string{
				"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}})
		}, false, "RELEASE ADOPT", "OK", 0},
		{"adopt refused", func(t *testing.T) outcome {
			return run([]string{"adopt"}, Deps{})
		}, true, "ADOPT", "REFUSED", 2},
		{"adopt failed", func(t *testing.T) outcome {
			return run(adoptArgs(t, "hulk\n"), Deps{SSH: &fakeSSH{refuse: map[string]error{
				"hulk": fmt.Errorf("ssh: connect to host hulk port 22: Connection refused")}}})
		}, true, "RELEASE ADOPT", "FAILED", 1},

		{"pull ok", func(t *testing.T) outcome {
			out, s, changelog := pulled(t, "v0.16.0")
			return run(pullArgs(t, out, changelog), Deps{SSH: s})
		}, false, "RELEASE PULL", "OK", 0},
		{"pull refused", func(t *testing.T) outcome {
			return run([]string{"pull"}, Deps{})
		}, true, "PULL", "REFUSED", 2},
		{"pull failed", func(t *testing.T) outcome {
			out, s, _ := pulled(t, "v0.16.0")
			return run(pullArgs(t, out, changelogIn(t, t.TempDir())), Deps{SSH: s})
		}, true, "PULL", "FAILED", 1},

		{"cycle ok", func(t *testing.T) outcome {
			args, deps, _ := cycleRig(t, playOutput("WOULD-INSTALL", "", "batman", "vision"),
				playOutput("INSTALLED", "", "batman", "vision"))
			return run(args, deps)
		}, false, "CYCLE", "OK", 0},
		{"cycle refused", func(t *testing.T) outcome {
			return run([]string{"cycle"}, Deps{})
		}, true, "CYCLE", "REFUSED", 2},
		{"cycle failed", func(t *testing.T) outcome {
			args, deps, play := cycleRig(t, playOutput("WOULD-INSTALL", "vision", "batman", "vision"))
			play.fail = fmt.Errorf("exit status 2")
			return run(args, deps)
		}, true, "CYCLE", "FAILED", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.run(t)
			stream := got.out
			if tc.stderr {
				stream = got.errs
			}
			assert.Equal(t, tc.word, wordAfter(stream, tc.token), "stdout:\n%s\nstderr:\n%s", got.out, got.errs)
			assert.Equal(t, tc.code, got.code, "stdout:\n%s\nstderr:\n%s", got.out, got.errs)
		})
	}
}
