package friend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
)

// laneWallChild is set while TestALaneRunsInsideItsWallProfile runs: this test binary,
// started again by the lane, is then the wall (`<bin> wall ...`) or the fake harness
// inside it (`<bin> run --session ...`, the opencode turn), and never the tests.
const laneWallChild = "NOVA_FRIEND_LANE_WALL_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(laneWallChild) != "" && len(os.Args) > 1 {
		switch os.Args[1] {
		case WallVerb:
			os.Exit(RunWall(os.Args[2:], os.Environ(), os.Stdin, os.Stdout, os.Stderr))
		case "run":
			os.Exit(fakeLaneHarness())
		}
	}
	os.Exit(m.Run())
}

// fakeLaneHarness is a harness's turn inside the wall: it writes its job directory,
// tries the coordinator's self (memory/, identity/, MEMORY-HOT.md), a file in HOME
// outside the wall, and a connection to a port outside the allow list, and says what
// each did on one line.
func fakeLaneHarness() int {
	write := func(path string) string {
		if err := os.WriteFile(path, []byte("lane\n"), 0o644); err != nil {
			return "refused"
		}
		return "written"
	}
	self := os.Getenv("LANE_WALL_SELF")
	dial := "refused"
	if c, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", os.Getenv("LANE_WALL_ADDR")); err == nil {
		c.Close() // ignored: the test listener's accepted connection; the test reads nothing after
		dial = "connected"
	}
	fmt.Printf("HARNESS job=%s memory=%s identity=%s memory_md=%s outside=%s net=%s\n",
		write(filepath.Join(os.Getenv("LANE_WALL_JOB"), "out.txt")),
		write(filepath.Join(self, "memory", "lane.md")),
		write(filepath.Join(self, "identity", "lane.md")),
		write(filepath.Join(self, "MEMORY-HOT.md")),
		write(filepath.Join(os.Getenv("LANE_WALL_OUTSIDE"), "lane.txt")),
		dial)
	return 0
}

// A lane's turn runs its harness inside the wall of the friend profile: the job
// directory is written, the coordinator's self and a connection outside the allow list
// are refused, and a profile whose grant would cover the self is refused before anything
// runs (docs/SPEC-FRIEND.md and docs/SPEC-SANDBOX.md, buds-in-the-wall-r.w5).
func TestALaneRunsInsideItsWallProfile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the wall has a body on linux and darwin only")
	}
	if _, ok := sandbox.Available(); !ok {
		t.Skip("no OS wall on this machine: " + sandbox.Note())
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root")
	}
	bin, err := os.Executable()
	require.NoError(t, err)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	self := filepath.Join(home, "rowan-working", "rowan-new")
	friendDir := filepath.Join(root, "friend")
	job := filepath.Join(friendDir, "jobs", "card~1")
	config := filepath.Join(root, "config")
	for _, d := range []string{filepath.Join(self, "memory"), filepath.Join(self, "identity"), job, config} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0") // a port of this test's own, outside the allow list
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) // ignored: closing the test listener at cleanup
	// the child's environment is this test's own (cmd.Env), never the process's
	run := envExec(laneWallChild+"=1",
		"HOME="+home, // the deny list's ~ is the wall's HOME
		"LANE_WALL_SELF="+self,
		"LANE_WALL_JOB="+job,
		"LANE_WALL_OUTSIDE="+home,
		"LANE_WALL_ADDR="+ln.Addr().String())

	turn := func(w Wall, ctx context.Context) (LaneTurn, string, error) {
		var out bytes.Buffer
		oc := &OpenCode{Dir: friendDir, Program: bin, Run: w.Exec(run), Out: &out}
		lt, err := oc.DeliverTo(ctx, "ses_lane1", "card~1")
		return lt, out.String(), err
	}
	// the self is denied as the adopter names it, under ~/ (the wall's HOME)
	wall := Wall{Self: []string{bin}, Dir: friendDir, ConfigDir: config, Deny: []string{"~/rowan-working/rowan-new"}}

	lt, out, err := turn(wall, LaneContext(t.Context()))
	require.NoError(t, err, out)
	require.Equal(t, 0, lt.Exit, out)
	require.Contains(t, out, "HARNESS job=written memory=refused identity=refused memory_md=refused outside=refused net=refused", out)
	got, err := os.ReadFile(filepath.Join(job, "out.txt"))
	require.NoError(t, err)
	require.Equal(t, "lane\n", string(got))
	for _, p := range []string{filepath.Join(self, "memory", "lane.md"), filepath.Join(self, "identity", "lane.md"), filepath.Join(self, "MEMORY-HOT.md")} {
		require.NoFileExists(t, p)
	}

	// a grant over the self is refused before the harness runs
	over := wall
	over.Jobs = []string{filepath.Join(home, "rowan-working")}
	lt, out, err = turn(over, LaneContext(t.Context()))
	require.NoError(t, err, out)
	require.Equal(t, sandbox.ExitRefused, lt.Exit, out)
	require.Contains(t, out, "WALL REFUSED reason=denied_write", out)
	require.NotContains(t, out, "HARNESS", out)

	// a profile that is not one is refused, never run unwalled
	other := wall
	other.Profile = "open"
	lt, out, err = turn(other, LaneContext(t.Context()))
	require.NoError(t, err, out)
	require.Equal(t, sandbox.ExitRefused, lt.Exit, out)
	require.Contains(t, out, `WALL REFUSED reason=bad_profile no wall profile "open"`, out)

	// a wall that denies nothing is refused, never run with the self open
	open := wall
	open.Deny = nil
	lt, out, err = turn(open, LaneContext(t.Context()))
	require.NoError(t, err, out)
	require.Equal(t, sandbox.ExitRefused, lt.Exit, out)
	require.Contains(t, out, "WALL REFUSED reason=bad_profile the friend profile denies nothing", out)
	require.NotContains(t, out, "HARNESS", out)

	// a lane with no program to run the wall is refused
	_, _, err = turn(Wall{Dir: friendDir}, LaneContext(t.Context()))
	require.ErrorContains(t, err, "never runs outside it")

	// what is not a lane's runs as it did: the wall is the lanes'
	require.False(t, InLane(t.Context()))
	require.True(t, strings.HasPrefix(strings.Join(wall.Args(), " "), "wall --profile friend --dir "+friendDir+" --deny ~/rowan-working/rowan-new --config-dir "+config+" --"))
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
