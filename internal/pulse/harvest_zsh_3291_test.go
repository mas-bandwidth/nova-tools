package pulse

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// #3291: superman and batman harvested nothing from 2026-09-23T00:36Z. The shipped
// sshShell handed the stage script to ssh as the remote command, so the bench's login
// shell read it; on the Macs that is zsh, and zsh took `$3:refs/harvest/$4` as the `:r`
// modifier, so `rowan/probe-superman-1404` arrived as `rowan/probe-superman-1404efs/...`
// and every fetch failed. These tests put a zsh login shell behind a fake ssh and run the
// real stage script through the real sshShell.

// fakeSSHWithLoginShell writes a fake ssh into a temp dir that behaves as sshd does: the
// options are skipped, and the remote words, joined by spaces, go to the login shell as
// `<login> -c "<words>"` with ssh's stdin passed through. The login shell is real zsh when
// the machine has it; otherwise a stand-in that applies zsh's `$name:r` modifier and then
// runs the words under bash, so the test is red for the same reason on a Linux runner.
func fakeSSHWithLoginShell(t *testing.T) (ssh, login string) {
	t.Helper()
	dir := t.TempDir()
	login, err := exec.LookPath("zsh")
	if err != nil {
		login = filepath.Join(dir, "zsh-like")
		standIn := "#!/usr/bin/env bash\n" +
			"# zsh's :r modifier (drop the extension), the one #3291 tripped on\n" +
			"[ \"$1\" = -c ] || exit 2\n" +
			"words=$(printf '%s' \"$2\" | sed -E 's/\\$([A-Za-z0-9_]+):r/${\\1%.*}/g')\n" +
			"exec bash -c \"$words\"\n"
		if err := testbin.WriteExecutable(login, []byte(standIn), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ssh = filepath.Join(dir, "ssh")
	fake := "#!/usr/bin/env bash\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -n) exec </dev/null; shift;; -o) shift 2;; -*) shift;; *) break;; esac; done\n" +
		"shift # the host\n" +
		"exec \"$FAKE_LOGIN_SHELL\" -c \"$*\"\n"
	if err := testbin.WriteExecutable(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_LOGIN_SHELL", login)
	return ssh, login
}

func git3291(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The shipped sshShell runs the stage script as `bash -s` on stdin, so a branch whose
// refspec a zsh login shell would mangle is staged intact under refs/harvest/<label>.
func TestHarvestStageScriptSurvivesAZshLoginShell3291(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh and its login shell are bash scripts")
	}
	ssh, login := fakeSSHWithLoginShell(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	job := filepath.Join(home, "jobs", "card-probe-superman-1404")
	repo := filepath.Join(job, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git3291(t, repo, "init", "-q")
	git3291(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	git3291(t, repo, "checkout", "-q", "-b", "rowan/probe-superman-1404")
	git3291(t, repo, "commit", "-q", "--allow-empty", "-m", "the card's work")
	want := git3291(t, repo, "rev-parse", "HEAD")

	cands := []*batchCandidate{{
		job: benchJob{Dir: job}, repo: "mas-bandwidth/nova-tools",
		branch: "rowan/probe-superman-1404", label: "rowan/x-1",
	}}
	out, err := sshShell{Program: ssh}.Run("superman", benchStageScript(cands))
	if err != nil {
		t.Fatalf("the stage script through a %s login shell: %v\n%s", login, err, out)
	}
	stages, staged, failed := parseStaged(out)
	if len(failed) != 0 {
		t.Fatalf("with a %s login shell the stage failed %v; the login shell read the script and ate the refspec (#3291)", login, failed)
	}
	if staged["rowan/x-1"] != want {
		t.Fatalf("staged %v, want rowan/x-1 at %s; out %q", staged, want, out)
	}
	st := stages["nova-tools"]
	if st == "" {
		t.Fatalf("no STAGE line for nova-tools in %q", out)
	}
	if got := git3291(t, st, "rev-parse", "refs/harvest/rowan/x-1"); got != want {
		t.Fatalf("refs/harvest/rowan/x-1 in the stage is %s, want %s", got, want)
	}
}

// The control: the same refspec handed to the login shell as a command string is mangled.
// If this ever passes, the fake login shell is not zsh-like and the test above proves
// nothing.
func TestTheFakeLoginShellEatsTheRefspec3291(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake login shell is a bash script")
	}
	_, login := fakeSSHWithLoginShell(t)
	t.Setenv("HOME", t.TempDir())
	out, err := exec.Command(login, "-c", `x=rowan/probe-superman-1404; echo "refs/heads/$x:refs/harvest/y"`).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", login, err, out)
	}
	if got := strings.TrimSpace(string(out)); got == "refs/heads/rowan/probe-superman-1404:refs/harvest/y" {
		t.Fatalf("the login shell %s left the refspec intact; it must behave as zsh does", login)
	}
}
