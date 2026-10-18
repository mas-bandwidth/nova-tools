package friend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// laneWallChild is set while TestALaneRunsInsideItsWallProfile runs: this test binary,
// started again by the lane, is then the wall (`<bin> wall ...`) or the fake harness
// inside it (`<bin> run --session ...`, the opencode turn), and never the tests.
const laneWallChild = "NOVA_FRIEND_LANE_WALL_CHILD"

// laneHarness is the fake harness a lane runs inside the wall, set by
// lane_wall_functional_test.go: TestALaneRunsInsideItsWallProfile opens a loopback
// port of its own and dials it from inside the wall, so it is a functional test
// (2026-10-17, Glenn: no real sockets in unit tests, ever). Nil in the unit tier,
// where no test runs a harness turn through this binary.
var laneHarness func() int

func TestMain(m *testing.M) {
	if os.Getenv(laneWallChild) != "" && len(os.Args) > 1 {
		switch os.Args[1] {
		case WallVerb:
			os.Exit(RunWall(os.Args[2:], os.Environ(), os.Stdin, os.Stdout, os.Stderr))
		case "run":
			if laneHarness == nil {
				fmt.Fprintln(os.Stderr, "the lane harness is a functional test: build with -tags functional")
				os.Exit(2)
			}
			os.Exit(laneHarness())
		}
	}
	os.Exit(m.Run())
}

// envExec is RealExec's answer shape for a command run with this process's environment
// and env over it: the per-test seam that keeps the wall test off t.Setenv, so it runs in
// parallel. On a nonzero exit the output carries stderr after stdout, as RealExec's does.
func envExec(env ...string) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out.String() + Head(stderr.String(), OutputKept), exitErr.ExitCode(), nil
		}
		return out.String(), 0, err
	}
}
