package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseEnvRejoinsAValueThatSpansLines(t *testing.T) {
	t.Parallel()
	got := parseEnv([]string{"A=1", "MULTI=first", "second line", "", "B=two=parts", "9BAD=x", "C="})
	want := map[string]string{
		"A":     "1",
		"MULTI": "first\nsecond line\n",
		"B":     "two=parts\n9BAD=x",
		"C":     "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnv = %q, want %q", got, want)
	}
}

func TestEnvNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"PATH": true, "_": true, "a1": true, "NOVA_GO": true,
		"": false, "1A": false, "A-B": false, "A B": false,
	} {
		if isEnvName(name) != want {
			t.Errorf("isEnvName(%q) = %v", name, !want)
		}
	}
}

func TestEnvListIsInKeyOrder(t *testing.T) {
	t.Parallel()
	got := envList(map[string]string{"B": "2", "A": "1", "C": ""})
	if !reflect.DeepEqual(got, []string{"A=1", "B=2", "C="}) {
		t.Errorf("envList = %v", got)
	}
}

func TestEscapeGlobMakesADirectoryLiteral(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a backslash is the separator here")
	}
	dir := t.TempDir()
	odd := filepath.Join(dir, "a[b]*")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(odd, "x.key"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := &witness{h: osHost{}}
	got := w.glob(odd, "*.key")
	if len(got) != 1 || filepath.Base(got[0]) != "x.key" {
		t.Errorf("glob in a directory with metacharacters = %v", got)
	}
}

func TestLookPathIsCommandV(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("execute bits are not the test on windows")
	}
	a, b := t.TempDir(), t.TempDir()
	write := func(dir, name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(a, "notexec", 0o644)
	write(b, "notexec", 0o755)
	want := write(b, "tool", 0o755)
	if err := os.Mkdir(filepath.Join(a, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := osHost{}
	path := a + string(os.PathListSeparator) + "" + string(os.PathListSeparator) + b
	if p, ok := h.LookPath("tool", path); !ok || p != want {
		t.Errorf("LookPath(tool) = %q, %v, want %q", p, ok, want)
	}
	// The first EXECUTABLE file wins, not the first file.
	if p, ok := h.LookPath("notexec", path); !ok || p != filepath.Join(b, "notexec") {
		t.Errorf("LookPath(notexec) = %q, %v", p, ok)
	}
	// A directory is not a command.
	if _, ok := h.LookPath("adir", path); ok {
		t.Errorf("a directory was found as a command")
	}
	if _, ok := h.LookPath("absent", path); ok {
		t.Errorf("an absent command was found")
	}
	// A name with a slash is a path, not a search.
	if p, ok := h.LookPath(want, ""); !ok || p != want {
		t.Errorf("LookPath(%s) = %q, %v", want, p, ok)
	}
}

func TestRunReportsOutputCodeAndAFailureToStart(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	h := osHost{}
	res := h.Run(runSpec{name: "/bin/sh", args: []string{"-c", "echo out; echo err >&2; exit 3"}, env: []string{"PATH=/usr/bin:/bin"}})
	if res.err != nil || res.code != 3 || res.stdout != "out\n" || res.stderr != "err\n" {
		t.Errorf("Run = %+v", res)
	}
	if res.ok() {
		t.Errorf("a process that exited 3 is ok")
	}
	if got := res.combined(); got != "out\nerr\n" {
		t.Errorf("combined = %q", got)
	}
	res = h.Run(runSpec{name: "/bin/sh", args: []string{"-c", "exit 0"}})
	if !res.ok() {
		t.Errorf("Run of exit 0 = %+v", res)
	}
	res = h.Run(runSpec{name: filepath.Join(t.TempDir(), "absent")})
	if res.err == nil || res.ok() {
		t.Errorf("a program that cannot start reported %+v", res)
	}
}

func TestRunGivesTheProcessOnlyTheEnvironmentItIsHanded(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	// The test process has a HOME; the process it starts is not given it.
	res := osHost{}.Run(runSpec{name: "/bin/sh", args: []string{"-c", `echo "[$HOME][$ONLY]"`}, env: []string{"ONLY=mine"}})
	if res.stdout != "[][mine]\n" {
		t.Errorf("the process saw %q", res.stdout)
	}
}

// SourceEnv returns what sourcing a file SET: the card environment is the one a
// person gets by sourcing it, so a caller's PATH does not decide what is found.
func TestSourceEnvReturnsWhatTheFileSetOrChanged(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "env.sh")
	body := `export PATH="$HOME/sdk/go/bin:$HOME/.local/bin:/usr/bin:/bin"
export NOVA_FROM_FILE=set
export MULTI="one
two"
echo "noise on stdout"
echo "noise on stderr" >&2
`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	environ := []string{"HOME=/home/u", "PATH=/usr/bin:/bin", "UNCHANGED=same"}
	got, err := osHost{}.SourceEnv(file, environ)
	if err != nil {
		t.Fatal(err)
	}
	if got["PATH"] != "/home/u/sdk/go/bin:/home/u/.local/bin:/usr/bin:/bin" {
		t.Errorf("PATH = %q", got["PATH"])
	}
	if got["NOVA_FROM_FILE"] != "set" || got["MULTI"] != "one\ntwo" {
		t.Errorf("sourced = %q", got)
	}
	for _, k := range []string{"HOME", "UNCHANGED", "SHLVL", "PWD", "_"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s was reported as set by the file", k)
		}
	}
	// The file's own output is not the witness's.
	for k, v := range got {
		if strings.Contains(v, "noise") {
			t.Errorf("%s carries the file's output: %q", k, v)
		}
	}
}

func TestProcCgroupReadsProcOnLinuxAndAnswersFalseForNoProcess(t *testing.T) {
	t.Parallel()
	if _, ok := (osHost{}).ProcCgroup("999999999"); ok {
		t.Errorf("a pid that cannot exist had a cgroup")
	}
}
