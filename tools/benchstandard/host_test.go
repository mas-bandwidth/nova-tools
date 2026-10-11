package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

func TestParseEnvRejoinsAValueThatSpansLines(t *testing.T) {
	t.Parallel()
	assert.Equal(t, map[string]string{
		"A":     "1",
		"MULTI": "first\nsecond line\n",
		"B":     "two=parts\n9BAD=x",
		"C":     "",
	}, parseEnv([]string{"A=1", "MULTI=first", "second line", "", "B=two=parts", "9BAD=x", "C="}))
}

func TestEnvNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"PATH": true, "_": true, "a1": true, "NOVA_GO": true,
		"": false, "1A": false, "A-B": false, "A B": false,
	} {
		assert.Equal(t, want, isEnvName(name), "isEnvName(%q)", name)
	}
}

func TestEnvListIsInKeyOrder(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"A=1", "B=2", "C="}, envList(map[string]string{"B": "2", "A": "1", "C": ""}))
}

func TestEscapeGlobMakesADirectoryLiteral(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "a backslash is the separator here")
	odd := filepath.Join(t.TempDir(), "a[b]*")
	require.NoError(t, os.MkdirAll(odd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(odd, "x.key"), nil, 0o644))
	w := &witness{h: osHost{}}
	got := w.glob(odd, "*.key")
	require.Len(t, got, 1)
	assert.Equal(t, "x.key", filepath.Base(got[0]))
}

func TestLookPathIsCommandV(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "execute bits are not the test on windows")
	a, b := t.TempDir(), t.TempDir()
	write := func(dir, name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), mode))
		return p
	}
	write(a, "notexec", 0o644)
	write(b, "notexec", 0o755)
	want := write(b, "tool", 0o755)
	require.NoError(t, os.Mkdir(filepath.Join(a, "adir"), 0o755))
	h := osHost{}
	path := a + string(os.PathListSeparator) + "" + string(os.PathListSeparator) + b
	p, ok := h.LookPath("tool", path)
	assert.True(t, ok)
	assert.Equal(t, want, p, "the first executable file wins")
	p, ok = h.LookPath("notexec", path)
	assert.True(t, ok)
	assert.Equal(t, filepath.Join(b, "notexec"), p)
	_, ok = h.LookPath("adir", path)
	assert.False(t, ok, "a directory is not a command")
	_, ok = h.LookPath("absent", path)
	assert.False(t, ok, "an absent command was found")
	p, ok = h.LookPath(want, "")
	assert.True(t, ok)
	assert.Equal(t, want, p, "a name with a slash is a path, not a search")
}

func TestRunReportsOutputCodeAndAFailureToStart(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "needs sh")
	h := osHost{}
	res := h.Run(runSpec{name: "/bin/sh", args: []string{"-c", "echo out; echo err >&2; exit 3"}, env: []string{"PATH=/usr/bin:/bin"}})
	assert.NoError(t, res.err)
	assert.Equal(t, 3, res.code)
	assert.Equal(t, "out\n", res.stdout)
	assert.Equal(t, "err\n", res.stderr)
	assert.False(t, res.ok(), "a process that exited 3 is ok")
	assert.Equal(t, "out\nerr\n", res.combined())
	res = h.Run(runSpec{name: "/bin/sh", args: []string{"-c", "exit 0"}})
	assert.True(t, res.ok())
	res = h.Run(runSpec{name: filepath.Join(t.TempDir(), "absent")})
	assert.Error(t, res.err)
	assert.False(t, res.ok(), "a program that cannot start reported %+v", res)
}

func TestRunGivesTheProcessOnlyTheEnvironmentItIsHanded(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "needs sh")
	// The test process has a HOME; the process it starts is not given it.
	res := osHost{}.Run(runSpec{name: "/bin/sh", args: []string{"-c", `echo "[$HOME][$ONLY]"`}, env: []string{"ONLY=mine"}})
	assert.Equal(t, "[][mine]\n", res.stdout)
}

// SourceEnv returns what sourcing a file SET: the card environment is the one a
// person gets by sourcing it, so a caller's PATH does not decide what is found.
func TestSourceEnvReturnsWhatTheFileSetOrChanged(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "needs sh")
	file := filepath.Join(t.TempDir(), "env.sh")
	body := `export PATH="$HOME/sdk/go/bin:$HOME/.local/bin:/usr/bin:/bin"
export NOVA_FROM_FILE=set
export MULTI="one
two"
echo "noise on stdout"
echo "noise on stderr" >&2
`
	require.NoError(t, os.WriteFile(file, []byte(body), 0o644))
	got, said, err := osHost{}.SourceEnv(file, []string{"HOME=/home/u", "PATH=/usr/bin:/bin", "UNCHANGED=same"})
	require.NoError(t, err)
	// What the file printed is shown to the person, never read as environment.
	assert.Contains(t, said, "noise on stdout")
	assert.Contains(t, said, "noise on stderr")
	assert.Equal(t, "/home/u/sdk/go/bin:/home/u/.local/bin:/usr/bin:/bin", got["PATH"])
	assert.Equal(t, "set", got["NOVA_FROM_FILE"])
	assert.Equal(t, "one\ntwo", got["MULTI"])
	for _, k := range []string{"HOME", "UNCHANGED", "SHLVL", "PWD", "_"} {
		assert.NotContains(t, got, k)
	}
	// The file's own output is not the witness's.
	for k, v := range got {
		assert.NotContains(t, v, "noise", "%s carries the file's output", k)
	}
}

// An error the file raises is shown, as a shell sourcing it shows it, and the
// assignments before and after it still count.
func TestSourceEnvShowsTheFilesErrors(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "needs a shell")
	file := filepath.Join(t.TempDir(), "env.sh")
	body := "export BEFORE=1\nno_such_command_in_this_env_file\nexport AFTER=2\n"
	require.NoError(t, os.WriteFile(file, []byte(body), 0o644))
	got, said, err := osHost{}.SourceEnv(file, []string{"PATH=/usr/bin:/bin"})
	require.NoError(t, err)
	assert.Contains(t, said, "no_such_command_in_this_env_file")
	assert.Equal(t, "1", got["BEFORE"])
	assert.Equal(t, "2", got["AFTER"])
}

func TestProcCgroupReadsProcOnLinuxAndAnswersFalseForNoProcess(t *testing.T) {
	t.Parallel()
	_, ok := (osHost{}).ProcCgroup("999999999")
	assert.False(t, ok, "a pid that cannot exist had a cgroup")
}
