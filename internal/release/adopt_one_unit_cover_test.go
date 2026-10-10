package release

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverBin and coverDest are the remote paths these tests type, so the version
// argv the fake intercepts is the one adopt composes out of --bin.
const (
	coverBin  = "/home/nova/.local/bin"
	coverDest = "/home/nova/nova-bench/build"
)

// coverSSH is fakeSSH with one seam closed. adopt's dry run asks each machine
// `<bin>/nova-update version` and reads the second word, but fakeSSH answers
// every argv of a machine with the same text, so the version before and after
// an install cannot differ. This answers that one two-word argv from a list, in
// call order, and delegates everything else -- including the longer install
// argv, which merely names --version too.
type coverSSH struct {
	*fakeSSH
	bin      string
	versions []string
	asked    int
}

func (s *coverSSH) Run(ctx context.Context, machine string, argv []string) (string, error) {
	if len(argv) == 2 && argv[0] == path.Join(s.bin, ToolFile("nova-update", runtime.GOOS)) && argv[1] == "version" && s.asked < len(s.versions) {
		answer := s.versions[s.asked]
		s.asked++
		return answer, nil
	}
	return s.fakeSSH.Run(ctx, machine, argv)
}

// coverOne is one OneMachine wired to the answering fake: the flags the adopt
// tests type, a fresh Dir, and the fake as its only edge.
func coverOne(t *testing.T, s *fakeSSH, versions ...string) (*OneMachine, *coverSSH) {
	t.Helper()
	f := &coverSSH{fakeSSH: s, bin: coverBin, versions: versions}
	o := &OneMachine{
		Version: "v0.16.0",
		Flags: []string{"--no-certify", "--ssh", "/usr/bin/ssh",
			"--from", built(t, "v0.16.0", "", "nova-update"), "--bin", coverBin, "--dest", coverDest},
		Dir:  t.TempDir(),
		Deps: Deps{SSH: f},
	}
	return o, f
}

// adopt reads the version installed before and after, and leaves no list
// behind: the file it writes to name the one machine is removed on the way out.
func TestReleaseAdoptOneCoverReadsInstalledBeforeAndAfter(t *testing.T) {
	t.Parallel()
	s := &fakeSSH{answer: map[string]string{"m1": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}
	o, _ := coverOne(t, s, "nova-update v0.15.0\n", "nova-update v0.16.0\n")

	from, to, err := o.adopt("m1", "v0.16.0")
	require.NoError(t, err)
	assert.Equal(t, "v0.15.0", from)
	assert.Equal(t, "v0.16.0", to)

	left, gerr := filepath.Glob(filepath.Join(o.Dir, "adopt-one-*.machines"))
	require.NoError(t, gerr)
	assert.Empty(t, left, "the one-machine list is removed after the adoption")
}

// An installed=- line is nothing installed there yet, and reads as the empty
// version rather than the literal "-".
func TestReleaseAdoptOneCoverEmptyInstalledReadsAsNothing(t *testing.T) {
	t.Parallel()
	s := &fakeSSH{answer: map[string]string{"m1": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}
	o, _ := coverOne(t, s, "", "nova-update v0.16.0\n")

	from, to, err := o.adopt("m1", "v0.16.0")
	require.NoError(t, err)
	assert.Equal(t, "", from, "an installed=- line is nothing installed yet")
	assert.Equal(t, "v0.16.0", to)
}

// A machine the fake cannot reach fails the first dry run, and the message
// carries the dry run's exit and the refusal the run left as its last line.
func TestReleaseAdoptOneCoverRefusesAMachineTheFakeRefuses(t *testing.T) {
	t.Parallel()
	s := &fakeSSH{refuse: map[string]error{"m1": fmt.Errorf("ssh: connect to host m1 port 22: Connection refused")}}
	o, _ := coverOne(t, s)

	_, _, err := o.adopt("m1", "v0.16.0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "adopt --dry-run exited")
	assert.ErrorContains(t, err, "RELEASE ADOPT FAILED machines=1 adopted=0 refused=1")
}

// An install whose output carries no RELEASE INSTALLED line is not an adoption
// however it exited; adopt says so with the exit code and the last line.
func TestReleaseAdoptOneCoverFailsWhenInstallSaysNoReceipt(t *testing.T) {
	t.Parallel()
	s := &fakeSSH{answer: map[string]string{"m1": "bash: nova-update: command not found\n"}}
	o, _ := coverOne(t, s, "nova-update v0.15.0\n")

	from, _, err := o.adopt("m1", "v0.16.0")
	require.Error(t, err)
	assert.Equal(t, "v0.15.0", from)
	assert.ErrorContains(t, err, "adopt exited 1:")
}

// A Dir that is not there fails at CreateTemp before anything is run.
func TestReleaseAdoptOneCoverRefusesADirThatDoesNotExist(t *testing.T) {
	t.Parallel()
	s := &fakeSSH{answer: map[string]string{"m1": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}
	o, _ := coverOne(t, s, "nova-update v0.15.0\n", "nova-update v0.16.0\n")
	o.Dir = filepath.Join(t.TempDir(), "missing")

	_, _, err := o.adopt("m1", "v0.16.0")
	require.Error(t, err)
	assert.Empty(t, s.runs, "CreateTemp failed before any run")
	assert.Empty(t, s.sends, "CreateTemp failed before any send")
}

// args puts the caller's Flags between "adopt" and the list and version it
// appends, so a flag never displaces the two arguments the verb needs.
func TestReleaseAdoptOneCoverArgsPlacesFlags(t *testing.T) {
	t.Parallel()
	o := &OneMachine{Flags: []string{"--no-certify", "--ssh", "/usr/bin/ssh"}}

	assert.Equal(t,
		[]string{"adopt", "--no-certify", "--ssh", "/usr/bin/ssh", "--machines", "/tmp/list", "--version", "v0.16.0"},
		o.args("v0.16.0", "/tmp/list"))
}

// lastLine is the last line of the first output that says anything, trimmed,
// and says so when every one of them is blank.
func TestReleaseAdoptOneCoverLastLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		outs []string
		want string
	}{
		{"last of one", []string{"a\nb\n"}, "b"},
		{"first that says anything", []string{"", "x\ny"}, "y"},
		{"all blank", []string{"", "  "}, "it said nothing"},
		{"none", nil, "it said nothing"},
		{"trimmed", []string{"  one  "}, "one"},
		{"trailing blanks", []string{"\n\ntwo\n"}, "two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, lastLine(tc.outs...))
		})
	}
}
