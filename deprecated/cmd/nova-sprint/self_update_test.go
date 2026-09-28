package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
)

const selfTestSha = "4eabff79cc2e1b5f0f7a6d3c2b1a09876543210f"

// selfCmdFake answers git as a clean dev checkout at selfTestSha, writes
// the stamped version into go build's -o file, and answers `<file> version`
// from it. It starts no process.
type selfCmdFake struct{ offDev bool }

func (f selfCmdFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	switch {
	case argv[0] == "git" && argv[1] == "rev-parse":
		return selfTestSha + "\n", nil
	case argv[0] == "git" && argv[1] == "merge-base" && f.offDev:
		return "", errors.New("exit status 1")
	case argv[0] == "git":
		return "", nil
	case argv[0] == "go":
		var o, v string
		for i, a := range argv {
			if a == "-o" {
				o = argv[i+1]
			}
			if strings.HasPrefix(a, "-X main.version=") {
				v = strings.TrimPrefix(a, "-X main.version=")
			}
		}
		return "", os.WriteFile(o, []byte(v), 0o755)
	case len(argv) == 2 && argv[1] == "version":
		b, err := os.ReadFile(argv[0])
		if err != nil {
			return "", err
		}
		return "nova-sprint " + string(b) + " darwin/arm64 go1.26.6\n", nil
	}
	return "", errors.New("unexpected " + strings.Join(argv, " "))
}

func selfCmdHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	src := filepath.Join(home, filepath.FromSlash(fleetbuild.SrcDirRel))
	for _, err := range []error{
		os.MkdirAll(filepath.Join(src, ".git"), 0o755),
		os.WriteFile(filepath.Join(src, "go.mod"), []byte("module m\n\ngo 1.26.6\n"), 0o644),
		os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755),
		os.WriteFile(filepath.Join(home, ".local", "bin", "nova-sprint"), []byte("v0.16.0-dev.c839379e"), 0o755),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// TestSelfUpdateVerbPrintsOldToNew: one command, one final line naming the
// old and new versions; a second run is SKIPPED.
func TestSelfUpdateVerbPrintsOldToNew(t *testing.T) {
	t.Parallel()
	home := selfCmdHome(t)
	deps := selfDeps{Runner: selfCmdFake{}, Home: func() (string, error) { return home, nil }, PID: 7}
	var out, errOut bytes.Buffer
	if code := runSelfWith(context.Background(), []string{"update"}, &out, &errOut, deps); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
	want := "SELF UPDATE OK v0.16.0-dev.c839379e -> v0.16.0-dev.4eabff79 commit=4eabff79cc2e toolchain=go1.26.6 bin="
	if !strings.Contains(out.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, out.String())
	}
	out.Reset()
	if code := runSelfWith(context.Background(), []string{"update", "--sha", selfTestSha[:8]}, &out, &errOut, deps); code != 0 || !strings.Contains(out.String(), "SELF UPDATE SKIPPED ") {
		t.Errorf("rerun: exit %d\n%s", code, out.String())
	}
}

// TestSelfUpdateVerbRefusalsPrint: every refusal is one line on stderr.
func TestSelfUpdateVerbRefusalsPrint(t *testing.T) {
	t.Parallel()
	home := selfCmdHome(t)
	for _, tc := range []struct {
		args []string
		fake selfCmdFake
		code int
		want string
	}{
		{nil, selfCmdFake{}, 2, "wants the subverb update"},
		{[]string{"upgrade"}, selfCmdFake{}, 2, "wants the subverb update"},
		{[]string{"update", "extra"}, selfCmdFake{}, 2, "takes no arguments"},
		{[]string{"update", "--nope"}, selfCmdFake{}, 2, "not defined"},
		{[]string{"update", "--sha", "zz"}, selfCmdFake{}, 1, "SELF UPDATE REFUSED: --sha"},
		{[]string{"update"}, selfCmdFake{offDev: true}, 1, "SELF UPDATE REFUSED: 4eabff79cc2e is not on origin/dev"},
	} {
		deps := selfDeps{Runner: tc.fake, Home: func() (string, error) { return home, nil }, PID: 7}
		var out, errOut bytes.Buffer
		code := runSelfWith(context.Background(), tc.args, &out, &errOut, deps)
		if code != tc.code || !strings.Contains(errOut.String(), tc.want) || strings.Count(errOut.String(), "\n") != 1 {
			t.Errorf("%v: exit %d stderr %q; want exit %d and one line naming %q", tc.args, code, errOut.String(), tc.code, tc.want)
		}
	}
}
