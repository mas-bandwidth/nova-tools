package runframe

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEnv is the streams the frame reads, in one process.
type testEnv struct{ out, err bytes.Buffer }

func (e *testEnv) Out() io.Writer { return &e.out }
func (e *testEnv) Err() io.Writer { return &e.err }

// testVerb records the arguments the frame handed it and answers a fixed code.
type testVerb struct {
	help string
	code int
	got  []string
}

func (v *testVerb) Help() string                     { return v.help }
func (v *testVerb) Do(e *testEnv, args []string) int { v.got = args; return v.code }

func testFrame() (*testEnv, map[string]*testVerb) {
	v := &testVerb{help: "run ok\n", code: 1}
	return &testEnv{}, map[string]*testVerb{"run": v}
}

func banner() string { return "the banner\n" }

// TestTheFramePrintsTheBannerAndRefusesWithNoVerb pins the frame's first rule:
// no argument is usage, on stderr, exit 2.
func TestTheFramePrintsTheBannerAndRefusesWithNoVerb(t *testing.T) {
	t.Parallel()
	e, reg := testFrame()
	require.Equal(t, 2, Run(e, nil, "tool", "go run ./tool", banner, nil, reg))
	assert.Equal(t, "the banner\n", e.err.String())
	assert.Empty(t, e.out.String())
}

// TestTheFramePrintsTheBannerOnHelp pins that a bare help is the banner on
// stdout at exit 0.
func TestTheFramePrintsTheBannerOnHelp(t *testing.T) {
	t.Parallel()
	e, reg := testFrame()
	require.Equal(t, 0, Run(e, []string{"help"}, "tool", "go run ./tool", banner, nil, reg))
	assert.Equal(t, "the banner\n", e.out.String())
	assert.Empty(t, e.err.String())
}

// TestTheFramePrintsOneVerbsHelp pins `help <verb>`: that verb's help on
// stdout, exit 0, and no dispatch.
func TestTheFramePrintsOneVerbsHelp(t *testing.T) {
	t.Parallel()
	e, reg := testFrame()
	require.Equal(t, 0, Run(e, []string{"help", "run"}, "tool", "go run ./tool", banner, nil, reg))
	assert.Equal(t, "run ok\n", e.out.String())
	assert.Nil(t, reg["run"].got, "help dispatched the verb")
}

// TestTheFrameRefusesAnUnknownVerbByNameAndDoor pins the two refusals: an
// unknown verb under help, and an unknown verb as the command, each naming the
// verb and the command to run.
func TestTheFrameRefusesAnUnknownVerbByNameAndDoor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"help", "nope"}, `tool help: unknown verb "nope"; run: go run ./tool help`},
		{"verb", []string{"nope"}, `tool: unknown verb "nope"; the verbs are run; run: go run ./tool help`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, reg := testFrame()
			require.Equal(t, 2, Run(e, tc.args, "tool", "go run ./tool", banner, []string{"run"}, reg))
			assert.Contains(t, e.err.String(), tc.want)
			assert.Empty(t, e.out.String())
		})
	}
}

// TestTheFrameRunsAKnownVerbWithItsArguments pins that a known verb runs with
// the arguments after its name and its exit code is returned unchanged.
func TestTheFrameRunsAKnownVerbWithItsArguments(t *testing.T) {
	t.Parallel()
	e, reg := testFrame()
	require.Equal(t, 1, Run(e, []string{"run", "a", "b"}, "tool", "go run ./tool", banner, nil, reg))
	assert.Equal(t, []string{"a", "b"}, reg["run"].got)
	assert.Empty(t, e.out.String())
	assert.Empty(t, e.err.String())
}
