package launch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The wrapper log's path is a function of the results root the environment
// names and the argv the wrapper gets, and nothing in the argv can carry it
// out of the log directory.
func TestWrapperLogPathIsUnderTheResultsRootOrBesideTheWrapper(t *testing.T) {
	t.Parallel()
	wrapper := filepath.Join(t.TempDir(), "bin", WrapperName)
	root := filepath.Join(t.TempDir(), "results")
	card := []string{WrapperName, "s-launch/card-01/2"}
	if got, want := WrapperLogPath(wrapper, []string{WrapperLogEnv + "=" + root}, card), filepath.Join(root, WrapperLogDir, "s-launch", "card-01", "2.log"); got != want {
		t.Errorf("card log under the results root = %s, want %s", got, want)
	}
	// The last assignment wins, as execve resolves a repeated name.
	other := filepath.Join(t.TempDir(), "other")
	if got, want := WrapperLogPath(wrapper, []string{WrapperLogEnv + "=" + root, WrapperLogEnv + "=" + other}, card), filepath.Join(other, WrapperLogDir, "s-launch", "card-01", "2.log"); got != want {
		t.Errorf("repeated root: log = %s, want %s", got, want)
	}
	if got, want := CopyLogPath(wrapper, []string{WrapperLogEnv + "=" + root}, "task-7~2"), filepath.Join(root, WrapperLogDir, CopyArg, "task-7~2.log"); got != want {
		t.Errorf("copy log = %s, want %s", got, want)
	}
	// A relative root is no root: the log goes beside the wrapper.
	if got, want := WrapperLogPath(wrapper, []string{WrapperLogEnv + "=relative/results"}, card), filepath.Join(filepath.Dir(wrapper), WrapperLogDir, "s-launch", "card-01", "2.log"); got != want {
		t.Errorf("relative root: log = %s, want %s", got, want)
	}
	// Nothing in an argv leaves the log directory.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{WrapperName, "../../etc/passwd"}, filepath.Join("_", "_", "etc", "passwd.log")},
		{[]string{WrapperName, "a b", "c:d"}, filepath.Join("a_b", "c_d.log")},
		{[]string{WrapperName}, "wrapper.log"},
		{nil, "wrapper.log"},
	} {
		if got := WrapperLogName(c.args); got != c.want {
			t.Errorf("WrapperLogName(%q) = %s, want %s", c.args, got, c.want)
		}
	}
}

// The LAUNCHED line names the log the wrapper was started with, and the
// path is the one the start seam was handed.
func TestLaunchNamesTheWrapperLogOnTheLaunchedLine(t *testing.T) {
	t.Parallel()
	logDir := t.TempDir()
	lines := fixtureLines(2)
	var handed []string
	start := func(_ string, _ Line, _ time.Time, logPath string) (int, string, error) {
		handed = append(handed, logPath)
		return 41 + len(handed), "LAUNCHED", nil
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	res, err := Launch(strings.NewReader(lines[0].String()+"\n"+lines[1].String()+"\n"), &out,
		Config{Wrapper: exe, LogDir: logDir, start: start})
	if err != nil || res.Started != 2 {
		t.Fatalf("Launch = %+v err %v:\n%s", res, err, out.String())
	}
	for i, l := range lines {
		want := filepath.Join(logDir, l.Sprint, l.Label, "1.log")
		if l.Attempt != 1 {
			want = filepath.Join(logDir, l.Sprint, l.Label, "2.log")
		}
		if handed[i] != want {
			t.Errorf("line %d: the start seam was handed %s, want %s", i+1, handed[i], want)
		}
		line := "LAUNCHED " + l.Card() + " pid=" + strconv.Itoa(42+i) + " log=" + want + "\n"
		if !strings.Contains(out.String(), line) {
			t.Errorf("out lacks %q:\n%s", line, out.String())
		}
	}
}
