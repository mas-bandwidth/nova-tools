package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCIOneBenchRunner (#2932 control 4, #3350): no function outside
// internal/benchsh (and the guard, internal/testguard) runs ssh itself. #3350
// moved the last of the #2932 inventory's sites onto internal/benchsh and
// deleted its allow list, so there is no row to add: a new site is a red test.
func TestCIOneBenchRunner(t *testing.T) {
	root := repoRoot(t)
	sites, err := FindBenchRunners(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		t.Errorf("%s:%d: %s runs ssh itself; run bench scripts through internal/benchsh (benchsh.Run, benchsh.Command: `bash -s --`, script on stdin, data after benchsh's one exec line; #2932, #3291, #3350)",
			s.File, s.Line, s.Func)
	}

	t.Run("git-transport-excluded", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, "internal", "secrets", "storepull.go"))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := BenchRunnersInSource("internal/secrets/storepull.go", raw); err != nil || len(got) != 0 {
			t.Fatalf("storepull.go sites = %v (%v); StorePullSSHCommand and PullStore reach ssh only as git's transport", got, err)
		}
		if !strings.Contains(string(raw), "func StorePullSSHCommand") || !strings.Contains(string(raw), "func PullStore") {
			t.Fatal("storepull.go no longer has StorePullSSHCommand and PullStore; re-derive this subtest")
		}
		fixture := `package x
import ("os/exec"; "github.com/mas-bandwidth/nova-tools/internal/testguard")
func transportCmd(host string) string { testguard.RefuseHosts("ssh", host); return "ssh -o BatchMode=yes" }
func pull(host string) error {
	c := exec.Command("git", "fetch", host)
	c.Env = append(c.Env, "GIT_SSH_COMMAND="+transportCmd(host))
	return c.Run()
}
func guardedOnly(host string) { testguard.RefuseHosts("ssh", host) }
`
		got, err := BenchRunnersInSource("x/x.go", []byte(fixture))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Func != "guardedOnly" {
			t.Fatalf("fixture sites = %v; want guardedOnly only (RefuseHosts(\"ssh\") with no git transport is still a site)", got)
		}
	})

	t.Run("benchsh-callers-are-not-sites", func(t *testing.T) {
		fixture := `package x
import ("context"; "strings"; "github.com/mas-bandwidth/nova-tools/internal/benchsh")
func viaRun(ctx context.Context, host string) { _, _ = benchsh.Run(ctx, benchsh.Target{Host: host}, "true") }
func viaCommand(ctx context.Context, host string) {
	cmd, _ := benchsh.Command(ctx, benchsh.Target{Host: host, SSH: "ssh"}, "cat > \"$1\"", strings.NewReader("v"), "/p")
	_ = cmd.Run()
}
`
		got, err := BenchRunnersInSource("x/x.go", []byte(fixture))
		if err != nil || len(got) != 0 {
			t.Fatalf("fixture sites = %v (%v); a benchsh caller runs no ssh itself", got, err)
		}
	})

	t.Run("rule-shapes", func(t *testing.T) {
		fixture := `package x
import ("context"; "flag"; "os/exec")
type ExecSSH struct{ Path string }
type runner struct{ Program string }
var def = runner{Program: "ssh"}
func literal() { exec.Command("ssh", "h", "true").Run() }
func argv0() { argv := []string{"ssh", "h"}; exec.Command(argv[0], argv[1:]...).Run() }
func setIdent(bin string) { if bin == "" { bin = "ssh" }; exec.Command(bin, "h").Run() }
func flagDefault(fs *flag.FlagSet) { prog := fs.String("ssh", "ssh", ""); exec.Command(*prog, "h").Run() }
func param(ctx context.Context, sshPath string) { exec.CommandContext(ctx, sshPath, "h").Run() }
func (r runner) Run() { exec.Command(r.Program, "h").Run() }
func (e ExecSSH) Go() { exec.Command(e.Path, "h").Run() }
func notSSH(ctx context.Context) {
	exec.Command("gh", "api").Run(); exec.Command("git", "push").Run()
	exec.Command("rsync", "-a", "h:x", ".").Run(); exec.Command("scp", "h:x", ".").Run()
}
`
		got, err := BenchRunnersInSource("x/x.go", []byte(fixture))
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, s := range got {
			keys = append(keys, s.Func)
		}
		want := "literal argv0 setIdent flagDefault param runner.Run ExecSSH.Go"
		if strings.Join(keys, " ") != want {
			t.Fatalf("fixture sites %q, want %q", strings.Join(keys, " "), want)
		}
	})
}
