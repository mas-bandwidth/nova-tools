package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// benchRunnerBaseSHA is the nova-tools dev commit the inventory was derived
// at: the base-sha of the #2932 Part A build.
const benchRunnerBaseSHA = "ce8631be6"

// benchRunnerAtBase is the site set derived at benchRunnerBaseSHA (the 18
// rows of the #2932 rev 4 inventory plus cmd/nova-sprint/expire.go, which
// landed after it; harvest.SSHPusher moved onto internal/benchsh in the same
// build and has no row). It is the allow file's ceiling: every row and every
// site the rule finds is one of these, so nothing is added after the base. A
// site retired by #3350 or #3291 leaves the allow file (NOVA_CI_UPDATE=1 drops
// the row); it may stay here, where it allows nothing.
var benchRunnerAtBase = []string{
	"cmd/nova-sprint/expire.go sshProber.Probe",
	"internal/nsprint/deal/ssh.go remoteSession.Run",
	"internal/release/edges.go ExecSSH.Fetch",
	"internal/release/edges.go ExecSSH.Run",
	"internal/release/edges.go ExecSSH.Send",
	"internal/secrets/place.go sshPlaceSecret",
	"internal/swarm/bench.go remoteRun",
	"internal/swarm/benchpull.go sshOutput",
	"internal/swarm/benchpull.go sshRun",
}

// benchRunnerAllowOptions keys a row of the allow file by `<file> <func>`, the
// text before its first tab; the file only shrinks.
var benchRunnerAllowOptions = allowlist.Options{Key: allowlist.Fields(2), Ceiling: true}

func readBenchRunnerAllow(t *testing.T) *allowlist.List {
	t.Helper()
	allow := loadAllowlist(t, BenchRunnerAllowPath, benchRunnerAllowOptions)
	first := map[string]bool{}
	for _, row := range allow.Rows() {
		parts := strings.Split(row.Text, "\t")
		if len(parts) != 3 || strings.Count(parts[0], " ") != 1 || parts[1] == "" || !strings.HasPrefix(parts[2], "#") {
			t.Errorf("%s: row %q is not `<file> <func>\\t<shape>\\t<retiring issue>`", BenchRunnerAllowPath, row.Text)
			continue
		}
		if first[row.Key] {
			t.Errorf("%s: %s listed twice", BenchRunnerAllowPath, row.Key)
		}
		first[row.Key] = true
	}
	return allow
}

// TestCIOneBenchRunner (#2932 control 4): every ssh exec site outside
// internal/benchsh is a row of testdata/bench-runners.allow, every row is
// still a site (the file shrinks as sites retire), and no row is added after
// the base the inventory was derived at.
func TestCIOneBenchRunner(t *testing.T) {
	t.Parallel()

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
		if !allow.Has(s.Key()) {
			t.Errorf("%s:%d: %s runs ssh itself; the fleet plays reach benches, never a new row in %s",
				s.File, s.Line, s.Func, BenchRunnerAllowPath)
		}
	}
	for _, row := range allow.Rows() {
		if !base[row.Key] {
			t.Errorf("%s row %s was added after %s: the allow file may only shrink", BenchRunnerAllowPath, row.Key, benchRunnerBaseSHA)
		}
	}
	for _, row := range allowlist.Check(t, allow, found).Stale {
		t.Errorf("%s lists %s, which is no longer an ssh exec site: delete the row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", BenchRunnerAllowPath, row.Key)
	}

	t.Run("inventory-at-base", func(t *testing.T) {
		header, _, _ := strings.Cut(allow.Text(), "\n")
		if !strings.Contains(header, "derived at nova-tools dev "+benchRunnerBaseSHA) {
			t.Fatalf("%s line 1 %q does not name the base-sha %s", BenchRunnerAllowPath, header, benchRunnerBaseSHA)
		}
		// Every site the rule finds was in the inventory derived at the base: a
		// site outside it is new, and a site the rule misses leaves its row
		// stale above, which is red too.
		var outside []string
		for _, s := range sites {
			if !base[s.Key()] {
				outside = append(outside, s.Key())
			}
		}
		sort.Strings(outside)
		if len(outside) > 0 {
			t.Fatalf("the rule finds\n%s\noutside the %d rows derived at %s", strings.Join(outside, "\n"), len(benchRunnerAtBase), benchRunnerBaseSHA)
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
