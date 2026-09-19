package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// The Exec seam is the accept gate's wall (SPEC-TOOLWORK §1 rule 3), and what it sets on
// the command is the gate's: cold read 2 of #1721 found every site that took the seam's
// command then REPLACED its Env with goenv.Clean(os.Environ()), so the mutate phase ran
// the card's tests with the operator's HOME and no GOCACHE -- refused by the real wall
// as home_outside, exit 125, which the old scoring read as every unit red.

func seamLab(t *testing.T) (repo, base, head string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=R", "GIT_AUTHOR_EMAIL=r@e", "GIT_COMMITTER_NAME=R", "GIT_COMMITTER_EMAIL=r@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("go.mod", "module fixture\n\ngo 1.21\n")
	write("sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 1 }\n")
	write("sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 1 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base = run("rev-parse", "HEAD")
	write("sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}\n")
	write("sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 1 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "fix")
	head = run("rev-parse", "HEAD")
	return dir, base, head
}

// The seam's Env reaches the child: an Exec that marks its Env and wraps the command in
// a shell that exits 0 SILENTLY unless the mark is there. If mutate replaced the Env, the
// wrapper would swallow every run and no unit could go red.
func TestMutateKeepsTheExecSeamsEnv(t *testing.T) {
	repo, base, head := seamLab(t)
	exec0 := func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
		script := `[ "$NOVA_SEAM_MARK" = "held" ] || exit 0; exec "$0" "$@"`
		cmd := exec.CommandContext(ctx, "sh", "-c", script, name)
		cmd.Args = append(cmd.Args, args...)
		cmd.Dir = dir
		// The seam hands a CLEAN environment plus its mark, which is what the real seam
		// does: goenv.Clean drops GOFLAGS and GOTEST*, and a runner that has either set
		// made the fixture's `go test -json` print its start event and exit non-zero with
		// nothing else -- red on the CI space leg, green on hulk and the Studio, for a
		// reason that had nothing to do with the seam this test is about.
		cmd.Env = append(goenv.Clean(os.Environ()), "NOVA_SEAM_MARK=held")
		return cmd
	}
	res, err := Mutate(context.Background(), MutateOptions{Repo: repo, Base: base, Head: head, TempRoot: t.TempDir(), Exec: exec0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Red != 1 || !res.Pass {
		t.Fatalf("red=%d pass=%v skips=%v: the seam's Env did not reach the suite", res.Red, res.Pass, res.Skips)
	}
}

// A run the wall refused (exit 125, 126, 127) or that printed no result at all proved
// nothing about the range: it is a SKIP with its reason, never a red.
func TestMutateCouldNotRunIsASkipNotARed(t *testing.T) {
	repo, base, head := seamLab(t)
	for _, code := range []string{"125", "126", "127"} {
		exec0 := func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "sh", "-c", "echo 'SANDBOX REFUSED reason=home_outside' >&2; exit "+code)
			cmd.Dir = dir
			return cmd
		}
		res, err := Mutate(context.Background(), MutateOptions{Repo: repo, Base: base, Head: head, TempRoot: t.TempDir(), Exec: exec0})
		if err != nil {
			t.Fatal(err)
		}
		if res.Red != 0 || res.Pass {
			t.Fatalf("exit %s: red=%d pass=%v; a refused run was scored as a kill", code, res.Red, res.Pass)
		}
		if len(res.Skips) != 1 || !strings.Contains(res.Skips[0].Reason, "could not be run") {
			t.Fatalf("exit %s: skips=%v, want one could-not-run skip", code, res.Skips)
		}
	}
	// Exit 1 with no result line either: a run that said nothing killed nothing.
	exec1 := func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", "exit 1")
		cmd.Dir = dir
		return cmd
	}
	res, err := Mutate(context.Background(), MutateOptions{Repo: repo, Base: base, Head: head, TempRoot: t.TempDir(), Exec: exec1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Red != 0 || res.Pass || len(res.Skips) != 1 {
		t.Fatalf("silent exit 1: red=%d pass=%v skips=%v", res.Red, res.Pass, res.Skips)
	}
	// But a bare panic with no --- FAIL: line is now could-not-run, never a kill on
	// its own (HOLD on #1721).
	exec2 := func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", "echo '=== RUN   TestSignZero'; echo 'panic: boom'; exit 2")
		cmd.Dir = dir
		return cmd
	}
	res, err = Mutate(context.Background(), MutateOptions{Repo: repo, Base: base, Head: head, TempRoot: t.TempDir(), Exec: exec2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Red != 0 || len(res.Skips) != 1 || !strings.Contains(res.Skips[0].Reason, "could not be run") {
		t.Fatalf("a bare 'panic:' line with no --- FAIL: line: red=%d skips=%v, want could-not-run, not a kill", res.Red, res.Skips)
	}
}

// HOLD on #1721 (johnny-357c06749499): mutate.go:513-518 used to read "panic:" or a
// bare "FAIL\t" line anywhere in the combined output as proof every named unit died,
// and both strings are exactly what a card's own TestMain or init can print,
// regardless of whether anything it claims to test actually broke. Only a --- FAIL:
// line (scored above this fallback) or the exit status may say a unit died now.
func TestMutateABareFAILOrPanicLineWithNoStructuredFAILLineIsNotAKill(t *testing.T) {
	repo, base, head := seamLab(t)
	cases := []struct {
		name string
		exec func(ctx context.Context, dir, name string, args ...string) *exec.Cmd
	}{
		{"bare panic, no --- FAIL: line at all", func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "sh", "-c", "echo '=== RUN   TestSignZero'; echo 'panic: boom'; exit 2")
			cmd.Dir = dir
			return cmd
		}},
		{"a real unit passed, but a forged package FAIL line and no --- FAIL: for anything", func(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "sh", "-c", "echo '=== RUN   TestSignZero'; echo '--- PASS: TestSignZero (0.00s)'; printf 'FAIL\tfixture/sign\t0.01s\n'; exit 1")
			cmd.Dir = dir
			return cmd
		}},
	}
	for _, c := range cases {
		res, err := Mutate(context.Background(), MutateOptions{Repo: repo, Base: base, Head: head, TempRoot: t.TempDir(), Exec: c.exec})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Red != 0 || res.Pass {
			t.Fatalf("%s: red=%d pass=%v, want no kill from free text alone", c.name, res.Red, res.Pass)
		}
		if len(res.Skips) != 1 || !strings.Contains(res.Skips[0].Reason, "could not be run") {
			t.Fatalf("%s: skips=%v, want one could-not-run skip", c.name, res.Skips)
		}
	}
}
