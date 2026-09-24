package ci

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// benchRunnerBaseSHA is the nova-tools dev commit the inventory was derived
// at: the base-sha of the #2932 Part A build.
const benchRunnerBaseSHA = "ce8631be6"

// benchRunnerAtBase is the site set derived at benchRunnerBaseSHA (the 18
// rows of the #2932 rev 4 inventory plus cmd/nova-sprint/expire.go, which
// landed after it; harvest.SSHPusher moved onto internal/benchsh in the same
// build and has no row). It may only shrink: a site retired by #3350 or #3291
// leaves this list and the allow file together.
var benchRunnerAtBase = []string{
	"cmd/nova-sprint/expire.go sshProber.Probe",
	"internal/nsprint/deal/ssh.go remoteSession.Run",
	"internal/pulse/fleet.go fleetSSH",
	"internal/pulse/harvestbench.go sshShell.Run",
	"internal/release/edges.go ExecSSH.Fetch",
	"internal/release/edges.go ExecSSH.Run",
	"internal/release/edges.go ExecSSH.Send",
	"internal/secrets/place.go sshPlaceSecret",
	"internal/swarm/bench.go remoteRun",
	"internal/swarm/benchpull.go sshOutput",
	"internal/swarm/benchpull.go sshRun",
}

type benchRunnerAllow struct {
	header string
	rows   map[string]string // key -> "shape\tissue"
}

func readBenchRunnerAllow(t *testing.T) benchRunnerAllow {
	t.Helper()
	f, err := os.Open(BenchRunnerAllowPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	a := benchRunnerAllow{rows: map[string]string{}}
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			a.header, first = line, false
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || strings.Count(parts[0], " ") != 1 || parts[1] == "" || !strings.HasPrefix(parts[2], "#") {
			t.Errorf("%s: row %q is not `<file> <func>\\t<shape>\\t<retiring issue>`", BenchRunnerAllowPath, line)
			continue
		}
		if _, dup := a.rows[parts[0]]; dup {
			t.Errorf("%s: %s listed twice", BenchRunnerAllowPath, parts[0])
		}
		a.rows[parts[0]] = parts[1] + "\t" + parts[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestCIOneBenchRunner (#2932 control 4): every ssh exec site outside
// internal/benchsh is a row of testdata/bench-runners.allow, every row is
// still a site (the file shrinks as sites retire), and no row is added after
// the base the inventory was derived at.
func TestCIOneBenchRunner(t *testing.T) {
	root := repoRoot(t)
	sites, err := FindBenchRunners(root)
	if err != nil {
		t.Fatal(err)
	}
	allow := readBenchRunnerAllow(t)
	base := map[string]bool{}
	for _, k := range benchRunnerAtBase {
		base[k] = true
	}
	found := map[string]bool{}
	for _, s := range sites {
		found[s.Key()] = true
		if _, ok := allow.rows[s.Key()]; !ok {
			t.Errorf("%s:%d: %s runs ssh itself; run bench scripts through internal/benchsh (`bash -s --`, script on stdin; #2932, #3291), never a new row in %s",
				s.File, s.Line, s.Func, BenchRunnerAllowPath)
		}
	}
	for k := range allow.rows {
		if !found[k] {
			t.Errorf("%s lists %s, which is no longer an ssh exec site: delete the row (and its benchRunnerAtBase entry; the list only shrinks)", BenchRunnerAllowPath, k)
		}
		if !base[k] {
			t.Errorf("%s row %s was added after %s: the allow file may only shrink; use internal/benchsh", BenchRunnerAllowPath, k, benchRunnerBaseSHA)
		}
	}

	t.Run("inventory-at-base", func(t *testing.T) {
		if !strings.Contains(allow.header, "derived at nova-tools dev "+benchRunnerBaseSHA) {
			t.Fatalf("%s line 1 %q does not name the base-sha %s", BenchRunnerAllowPath, allow.header, benchRunnerBaseSHA)
		}
		var got []string
		for _, s := range sites {
			got = append(got, s.Key())
		}
		want := append([]string(nil), benchRunnerAtBase...)
		sort.Strings(want)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("the rule finds\n%s\nwant exactly the %d rows derived at %s\n%s\n(a site the rule misses is a red test)",
				strings.Join(got, "\n"), len(want), benchRunnerBaseSHA, strings.Join(want, "\n"))
		}
	})

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
