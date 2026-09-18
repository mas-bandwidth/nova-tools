package fleet

// Red tests first, for Glenn's directive of 2026-09-18: "certify fleet machines".
//
// The hurt: the first real Go card of the day died on hulk INSIDE the swarm wall --
// `~/sdk/go1.26.5` was not a readable root of the wall, so the only go the card could reach
// was `/usr/bin/go` 1.22, which go.mod refuses. Nothing had ever run a representative card
// on that bench under the wall, so the bench looked provisioned and was not. `fleet survey`
// asks a machine what it HAS; certify makes the machine DO the work a card does, under the
// same containment, and writes down that it did.
//
// Everything here is driven by fakes: a remote that answers from a table, a forge that
// answers from a list, a fixed clock. No test opens a socket, starts a program or waits on
// the host's scheduler.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// fakeRemote answers each target from a table and records every script it was handed. It is
// STRICT in the way the real ssh is strict: a target it has no answer for is the error ssh
// gives for a host it cannot reach, never an empty success, because a lenient fake is how a
// broken tool ships green (memory: "fakes strict like the real tool").
type fakeRemote struct {
	answers map[string]remoteAnswer

	mu    sync.Mutex
	calls []remoteCall
}

type remoteAnswer struct {
	out string
	err error
}

type remoteCall struct {
	target string
	script string
}

func (f *fakeRemote) Run(ctx context.Context, target, script string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, remoteCall{target: target, script: script})
	f.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	a, ok := f.answers[target+"|"+scriptKey(script)]
	if !ok {
		if a, ok = f.answers[target]; !ok {
			return "ssh: connect to host " + target + " port 22: Connection refused\n",
				errors.New("exit status 255")
		}
	}
	return a.out, a.err
}

func (f *fakeRemote) scripts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.script)
	}
	return out
}

// scriptKey names the workload a script belongs to, so a table can answer per workload. The
// marker is the one certify writes into every script it composes.
func scriptKey(script string) string {
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "# nova-certify workload "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// fakeForge answers the runner list from a table.
type fakeForge struct {
	runners map[string][]RunnerStatus
	err     error
}

func (f fakeForge) Runners(repo string) ([]RunnerStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runners[repo], nil
}

func fixedNow() time.Time {
	return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
}

// testRegistry writes a machines registry with one of each role.
func testRegistry(t *testing.T) string {
	t.Helper()
	return writeFile(t, "machines.tsv", strings.Join([]string{
		"hulk\thulk\tlinux/x64\tbench,runner\t-\t64\tallow-shared=2026-09-18 eight CI runners until the pull worker containerises cards",
		"space\tspace\tlinux/x64\tbench\trowan\t16\t-",
		"batman\tbatman\tdarwin/arm64\trunner\t-\t10\t-",
		"loki\tloki\tlinux/x64\tservices\t-\t4\tLoki, Grafana, Redis",
		"studio\tstudio\tdarwin/arm64\tcoordination\trowan\t24\t-",
	}, "\n")+"\n")
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	mustWrite(t, path, body)
	return path
}

// ---------------------------------------------------------------------------
// workloads
// ---------------------------------------------------------------------------

// TestTheStandardWorkloadsAreTheShippedClasses holds the shipped set: the classes, a role
// each must apply to, and the ones that are not a plain ssh (the wall, and the forge).
//
// Eight of them exist because the fleet was certified BY HAND on 2026-09-18 before this verb
// could run, and every one of the eight names a fault that pass found on a machine the
// survey called healthy.
func TestTheStandardWorkloadsAreTheShippedClasses(t *testing.T) {
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		// the card's original set
		"go-test":        RoleBench,
		"c-build":        RoleBench,
		"cpp-build":      RoleBench,
		"sbcl":           RoleBench,
		"git-push":       RoleBench,
		"runner-online":  RoleRunner,
		"loki-ready":     RoleServices,
		"redis-ping":     RoleServices,
		"postgres-ready": RoleServices,
		"bus-push":       RoleCoordination,
		"release-path":   RoleCoordination,
		// what the hand pass of 2026-09-18 found
		"path-resolves":  RoleBench,  // ~/go/bin shadows and a PATH no ssh reads
		"go-on-path":     RoleBench,  // three benches had no go non-interactively
		"runner-path":    RoleRunner, // 16 .path files with no go; 16 runners with no unit
		"git-identity":   RoleBench,  // empty on all four Linux machines
		"wall-toolchain": RoleBench,  // the card that silently built 1.26 with 1.22
		"services-reach": RoleBench,  // redis on 127.0.0.1, and `space` resolving nowhere
		"registry-truth": RoleBench,  // 16 online runners on a machine with no runner role
		"diag-size":      RoleRunner, // 15.7 GB of _diag, and a WARN not a FAIL
	}
	if len(loads) != len(want) {
		t.Fatalf("the standard set ships %d workloads, want %d", len(loads), len(want))
	}
	byClass := map[string]Workload{}
	for _, w := range loads {
		byClass[w.Class] = w
	}
	for class, role := range want {
		w, ok := byClass[class]
		if !ok {
			t.Errorf("the standard set ships no %s workload", class)
			continue
		}
		if !w.AppliesTo(role) {
			t.Errorf("%s does not apply to %s (roles=%s)", class, role, strings.Join(w.Roles, ","))
		}
		if w.Expect == nil {
			t.Errorf("%s carries no expect", class)
		}
	}
	// The wall is the whole point of go-test: the card that died on hulk died inside it.
	if !byClass["go-test"].Wall {
		t.Error("go-test does not run inside the wall; a plain ssh is exactly the check that passed while the card died")
	}
	if len(byClass["go-test"].Reads) == 0 {
		t.Error("go-test names no read root for the wall; the toolchain outside the wall is the failure being certified against")
	}
	if !byClass["git-push"].Wall || !byClass["sbcl"].Wall || !byClass["wall-toolchain"].Wall {
		t.Error("git-push, sbcl and wall-toolchain must run inside the wall: a card pushes and builds from inside it")
	}
	// These three are plain ssh ON PURPOSE. What they certify is the machine as a CI runner
	// or as a neighbour on the network, and wrapping them would certify the wall instead.
	for _, class := range []string{"services-reach", "path-resolves", "go-on-path", "runner-path"} {
		if byClass[class].Wall {
			t.Errorf("%s is a plain ssh by the card, not a wall workload", class)
		}
	}
	if byClass["runner-online"].Forge != ForgeRunners {
		t.Error("runner-online must ask the forge, not a machine")
	}
	if byClass["registry-truth"].Forge != ForgeRegistry {
		t.Error("registry-truth must hold the registry against the forge, not ask a machine")
	}
	// The one report-only class, and the only one: a WARN that spreads is a WARN nobody reads.
	for _, w := range loads {
		if w.Report && w.Class != "diag-size" {
			t.Errorf("%s is report-only; only diag-size is", w.Class)
		}
	}
	if !byClass["diag-size"].Report {
		t.Error("diag-size must be report-only: 15.7 GB of _diag broke nothing and a FAIL for it is a check people learn to pass over")
	}
	// path-resolves and registry-truth apply to EVERY role: a stale shadow tool and a
	// registry that disagrees with the forge are faults of any machine, not of a bench.
	for _, class := range []string{"path-resolves", "registry-truth"} {
		for _, role := range []string{RoleBench, RoleRunner, RoleServices, RoleCoordination} {
			if !byClass[class].AppliesTo(role) {
				t.Errorf("%s does not apply to %s", class, role)
			}
		}
	}
}

// TestAWorkloadIsAFileWithFrontMatter reads one from disk and holds every field.
func TestAWorkloadIsAFileWithFrontMatter(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "tiny.card"), strings.Join([]string{
		"roles: bench,services",
		"expect: ^TINY OK",
		"wall: yes",
		"reads: $HOME/sdk, /usr/lib",
		"",
		"echo TINY OK",
	}, "\n")+"\n")
	loads, err := ReadWorkloads(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 1 {
		t.Fatalf("read %d workloads, want 1", len(loads))
	}
	w := loads[0]
	if w.Class != "tiny" {
		t.Errorf("class = %q, want tiny (the class is the file name)", w.Class)
	}
	if !w.AppliesTo(RoleBench) || !w.AppliesTo(RoleServices) || w.AppliesTo(RoleRunner) {
		t.Errorf("roles = %v", w.Roles)
	}
	if !w.Wall {
		t.Error("wall: yes was not read")
	}
	if len(w.Reads) != 2 || w.Reads[0] != "$HOME/sdk" || w.Reads[1] != "/usr/lib" {
		t.Errorf("reads = %v", w.Reads)
	}
	if strings.TrimSpace(w.Body) != "echo TINY OK" {
		t.Errorf("body = %q", w.Body)
	}
	if !w.Expect.MatchString("TINY OK go=go1.26.5") {
		t.Error("the expect regexp does not match its own body's line")
	}
}

// TestAWorkloadWithoutARoleOrAnExpectIsRefused: a workload that applies to nothing, or that
// cannot say what a pass looks like, is a certificate that means nothing.
func TestAWorkloadWithoutARoleOrAnExpectIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no roles", "expect: ^OK\n\necho OK\n", "roles"},
		{"no expect", "roles: bench\n\necho OK\n", "expect"},
		{"unknown role", "roles: potato\nexpect: ^OK\n\necho OK\n", "potato"},
		{"bad regexp", "roles: bench\nexpect: ^(\n\necho OK\n", "expect"},
		{"no body", "roles: bench\nexpect: ^OK\n\n\n", "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "broken.card"), tc.body)
			_, err := ReadWorkloads(dir)
			if err == nil {
				t.Fatal("a broken workload was read without a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// the standard hash
// ---------------------------------------------------------------------------

// TestTheStandardHashMovesWithTheStandardAndWithTheWorkloads: the hash is what makes a
// certificate expire. If either half can change without the hash changing, a bench keeps a
// certificate for a standard it no longer meets.
func TestTheStandardHashMovesWithTheStandardAndWithTheWorkloads(t *testing.T) {
	standard := writeFile(t, "bench-standard.sh", "echo standard v1\n")
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	first, err := StandardHash(standard, loads)
	if err != nil {
		t.Fatal(err)
	}
	again, err := StandardHash(standard, loads)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("the hash is not stable: %s then %s", first, again)
	}
	moved := writeFile(t, "bench-standard.sh", "echo standard v2\n")
	if h, _ := StandardHash(moved, loads); h == first {
		t.Error("the standard file changed and the hash did not")
	}
	extra := append(append([]Workload{}, loads...), Workload{
		Class: "zzz", Roles: []string{RoleBench}, Expect: regexp.MustCompile("^OK"), Body: "echo OK",
	})
	if h, _ := StandardHash(standard, extra); h == first {
		t.Error("a workload was added and the hash did not move")
	}
}

// ---------------------------------------------------------------------------
// certificates
// ---------------------------------------------------------------------------

// TestCertifiedIsTrueOnlyForTheCurrentBuildAndHash is the whole currency rule.
func TestCertifiedIsTrueOnlyForTheCurrentBuildAndHash(t *testing.T) {
	path := writeFile(t, "certs.tsv", "")
	at := fixedNow()
	for _, c := range []Certificate{
		{Machine: "space", Build: "v0.17.0", Hash: "abc", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at},
		{Machine: "space", Build: "v0.17.0", Hash: "abc", Class: "sbcl", Verdict: VerdictFail, Evidence: "sbcl: not found", At: at},
		{Machine: "hulk", Build: "v0.16.0", Hash: "abc", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at},
	} {
		if err := AppendCertificate(path, c); err != nil {
			t.Fatal(err)
		}
	}
	certs, err := ReadCertificates(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                        string
		machine, class, build, hash string
		want                        bool
	}{
		{"current", "space", "go-test", "v0.17.0", "abc", true},
		{"the build moved on", "space", "go-test", "v0.17.1", "abc", false},
		{"the standard moved on", "space", "go-test", "v0.17.0", "def", false},
		{"a FAIL is not a certificate", "space", "sbcl", "v0.17.0", "abc", false},
		{"a class nobody ran", "space", "c-build", "v0.17.0", "abc", false},
		{"another machine's row", "vision", "go-test", "v0.17.0", "abc", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Certified(certs, tc.machine, tc.class, tc.build, tc.hash); got != tc.want {
				t.Errorf("Certified = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTheLastRowWins: certify appends, so a machine that failed and was repaired is current
// on its newest row and not on its oldest.
func TestTheLastRowWins(t *testing.T) {
	path := writeFile(t, "certs.tsv", "")
	at := fixedNow()
	must(t, AppendCertificate(path, Certificate{Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictFail, Evidence: "go1.22 refused by go.mod", At: at}))
	must(t, AppendCertificate(path, Certificate{Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at.Add(time.Hour)}))
	certs, err := ReadCertificates(path)
	if err != nil {
		t.Fatal(err)
	}
	if !Certified(certs, "hulk", "go-test", "v1", "h") {
		t.Error("the repaired bench is not certified; the newest row must win")
	}
}

// TestEvidenceIsOneLineOnTheRowAndOnTheLine: a workload that fails with a screenful must not
// be able to write a second line into the certificates file or into the output.
func TestEvidenceIsOneLineOnTheRowAndOnTheLine(t *testing.T) {
	path := writeFile(t, "certs.tsv", "")
	must(t, AppendCertificate(path, Certificate{
		Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictFail,
		Evidence: "first line\nsecond line\tand a tab", At: fixedNow(),
	}))
	raw := readFile(t, path)
	if strings.Count(strings.TrimRight(raw, "\n"), "\n") != 0 {
		t.Errorf("the certificates file holds %d rows for one certificate:\n%s", strings.Count(raw, "\n"), raw)
	}
	if strings.Count(raw, "\t") != 6 {
		t.Errorf("a row has 7 fields and so 6 tabs, got %d: %q", strings.Count(raw, "\t"), raw)
	}
}

// ---------------------------------------------------------------------------
// the run
// ---------------------------------------------------------------------------

// spaceBenchClasses is every workload a bench-only machine runs, in class order. It is
// spelled out rather than derived so that adding a class to the shipped set is a decision
// somebody makes in a test as well as in a directory.
var spaceBenchClasses = []string{
	"c-build", "cpp-build", "git-identity", "git-push", "go-on-path", "go-test",
	"path-resolves", "registry-truth", "sbcl", "services-reach", "wall-toolchain",
}

func benchOK() map[string]remoteAnswer {
	return map[string]remoteAnswer{
		"space|build":          {out: "nova-merge v0.17.0\n"},
		"space|go-test":        {out: "GO OK go version go1.26.5 linux/amd64 ok\n"},
		"space|c-build":        {out: "C OK\n"},
		"space|cpp-build":      {out: "CPP OK\n"},
		"space|sbcl":           {out: "SBCL OK 2.5.8\n"},
		"space|git-push":       {out: "GIT PUSH OK\n"},
		"space|path-resolves":  {out: "PATH OK /home/u/.local/bin/nova-merge v0.17.0\n"},
		"space|go-on-path":     {out: "GO PATH OK /home/u/go/bin/go go version go1.26.5 linux/amd64\n"},
		"space|git-identity":   {out: "GIT IDENTITY OK Rowan Claude <rowan@mas-bandwidth.com>\n"},
		"space|wall-toolchain": {out: "WALL TOOLCHAIN OK go version go1.26.5 linux/amd64\n"},
		"space|services-reach": {out: "SERVICES OK name=space addr=100.115.99.19 redis=PONG loki=ready\n"},
	}
}

// TestCertifyRunsEveryWorkloadOfTheMachinesRolesAndWritesARowEach is the happy path.
func TestCertifyRunsEveryWorkloadOfTheMachinesRolesAndWritesARowEach(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: benchOK()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out, errs)
	}
	for _, class := range spaceBenchClasses {
		if !strings.Contains(out, "CERTIFY space "+class+" OK evidence=") {
			t.Errorf("no OK line for %s:\n%s", class, out)
		}
	}
	if !strings.Contains(out, fmt.Sprintf("CERTIFY OK machines=1 ok=%d fail=0 warn=0", len(spaceBenchClasses))) {
		t.Errorf("no closing line:\n%s", out)
	}
	rows, err := ReadCertificates(certs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(spaceBenchClasses) {
		t.Fatalf("wrote %d certificate rows, want %d", len(rows), len(spaceBenchClasses))
	}
	for _, r := range rows {
		if r.Build != "v0.17.0" {
			t.Errorf("%s carries build %q; the build defaults to the machine's own nova-merge version line", r.Class, r.Build)
		}
		if r.Hash != "h" {
			t.Errorf("%s carries hash %q", r.Class, r.Hash)
		}
	}
	// No runner, services or coordination workload may have been run on a bench.
	for _, s := range remote.scripts() {
		if k := scriptKey(s); k == "loki-ready" || k == "bus-push" || k == "runner-path" {
			t.Errorf("a %s workload ran on a bench; workloads follow the machine's roles", k)
		}
	}
}

// TestAFailingWorkloadIsOneFAILRowAndExitOne: the verdict comes from what the machine SAID
// matched against expect, never from the exit code alone -- the hulk card exited non-zero
// for a reason the exit code could not name.
func TestAFailingWorkloadIsOneFAILRowAndExitOne(t *testing.T) {
	answers := benchOK()
	answers["space|go-test"] = remoteAnswer{
		out: "go: go.mod requires go >= 1.26.5 (running go 1.22.2)\n",
		err: errors.New("exit status 1"),
	}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	all := out + errs
	if !strings.Contains(all, "CERTIFY space go-test FAIL evidence=") {
		t.Errorf("no FAIL line:\n%s", all)
	}
	if !strings.Contains(all, "go.mod requires go >= 1.26.5") {
		t.Errorf("the FAIL line does not carry what the machine said:\n%s", all)
	}
	if !strings.Contains(all, "fail=1") {
		t.Errorf("the closing line does not count the failure:\n%s", all)
	}
	if !Certified(mustRead(t, certs), "space", "c-build", "v0.17.0", "h") {
		t.Error("one failing workload took the passing ones with it; each class is its own certificate")
	}
	if Certified(mustRead(t, certs), "space", "go-test", "v0.17.0", "h") {
		t.Error("a FAIL was written as a certificate")
	}
}

// TestGoTestRunsInsideTheWallWithTheToolchainAsAReadRoot is the hurt, as a test: the script
// certify composes for a wall workload wraps the body in nova-sandbox with a writable job
// directory, a HOME inside it, and the toolchain named as a read root.
func TestGoTestRunsInsideTheWallWithTheToolchainAsAReadRoot(t *testing.T) {
	remote := &fakeRemote{answers: benchOK()}
	_, _, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var script string
	for _, s := range remote.scripts() {
		if scriptKey(s) == "go-test" {
			script = s
		}
	}
	if script == "" {
		t.Fatal("no go-test script was sent")
	}
	for _, want := range []string{"nova-sandbox", "--write", "--read", "HOME="} {
		if !strings.Contains(script, want) {
			t.Errorf("the go-test script carries no %s; it is not the wall:\n%s", want, script)
		}
	}
	if !strings.Contains(script, "sdk") {
		t.Errorf("the go-test script does not name the toolchain as a read root -- this is the whole failure it certifies against:\n%s", script)
	}
	// And a plain-ssh workload must NOT be wrapped: a redis PING from the bench is not a
	// card, and wrapping it would certify the wall instead of the reachability.
	for _, s := range remote.scripts() {
		if scriptKey(s) == "services-reach" && strings.Contains(s, "nova-sandbox") {
			t.Error("the services-reach workload was wrapped in the wall; the card says plain ssh")
		}
	}
}

// TestGitPushMakesItsOwnScratchRemoteAndTakesItAway: the push workload must not leave a bare
// repository behind, and must not push to anything but the throwaway it made.
func TestGitPushMakesItsOwnScratchRemoteAndTakesItAway(t *testing.T) {
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	var body string
	for _, w := range loads {
		if w.Class == "git-push" {
			body = w.Body
		}
	}
	if body == "" {
		t.Fatal("no git-push workload")
	}
	for _, want := range []string{"git init --bare", "git push", "rm -rf"} {
		if !strings.Contains(body, want) {
			t.Errorf("git-push does not %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "github.com") {
		t.Error("git-push names a real remote; the remote is a bare repository made on the machine for the run")
	}
}

// TestRunnerOnlineAsksTheForgeAndNamesTheRunnerThatIsNot.
func TestRunnerOnlineAsksTheForgeAndNamesTheRunnerThatIsNot(t *testing.T) {
	forge := fakeForge{runners: map[string][]RunnerStatus{
		"mas-bandwidth/nova-tools": {
			{Name: "batman-nova-1", Status: "online"},
			{Name: "batman-nova-2", Status: "offline"},
			{Name: "vision-nova-1", Status: "online"},
		},
	}}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "batman", Certs: certs, Repo: "mas-bandwidth/nova-tools",
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"batman|build":         {out: "nova-merge v0.17.0\n"},
			"batman|go-on-path":    {out: "GO PATH OK /Users/nova/sdk/go1.27.1/bin/go go version go1.27.1 darwin/arm64\n"},
			"batman|runner-path":   {out: "RUNNER PATH OK runners=6 go>=go1.26.5 all under a unit\n"},
			"batman|path-resolves": {out: "PATH OK /Users/nova/.local/bin/nova-merge v0.17.0\n"},
			"batman|diag-size":     {out: "DIAG OK 378 MB across 6 _diag directories\n"},
		}}, Forge: forge, Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (batman-nova-2 is offline)\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY batman runner-online FAIL") || !strings.Contains(all, "batman-nova-2") {
		t.Errorf("the refusal does not name the runner that is offline:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY batman runner-path ") {
		t.Errorf("the runner's .path was not certified:\n%s", all)
	}
	if strings.Contains(all, "vision-nova-1") {
		t.Error("another machine's runners were counted; the rule is <machine>-nova-*")
	}
}

// TestAnUnknownMachineIsRefusedByNameBeforeAnySSH.
func TestAnUnknownMachineIsRefusedByNameBeforeAnySSH(t *testing.T) {
	remote := &fakeRemote{answers: benchOK()}
	_, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "batmobile", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "unknown-machine") || !strings.Contains(errs, "batmobile") {
		t.Errorf("the refusal does not name the machine or the reason:\n%s", errs)
	}
	if len(remote.calls) != 0 {
		t.Errorf("an unknown machine was reached %d times before the refusal", len(remote.calls))
	}
}

// TestDryRunSaysWhatItWouldRunAndWritesNothing.
func TestDryRunSaysWhatItWouldRunAndWritesNothing(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: benchOK()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs, DryRun: true,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errs)
	}
	if len(remote.calls) != 0 {
		t.Errorf("--dry-run reached the machine %d times", len(remote.calls))
	}
	if body := readFile(t, certs); strings.TrimSpace(body) != "" {
		t.Errorf("--dry-run wrote certificates:\n%s", body)
	}
	if !strings.Contains(out, "CERTIFY space go-test WOULD") {
		t.Errorf("--dry-run does not say what it would run:\n%s", out)
	}
}

// TestAllRunsEveryMachineInTheRegistryUnderItsOwnRoles.
func TestAllRunsEveryMachineInTheRegistryUnderItsOwnRoles(t *testing.T) {
	answers := benchOK()
	for _, m := range []string{"hulk", "batman", "loki", "studio"} {
		answers[m] = remoteAnswer{out: "nova-merge v0.17.0\nEVERYTHING OK PONG ready online\n"}
	}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), All: true, Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Forge: fakeForge{}, Repo: "mas-bandwidth/nova-tools",
		Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if !strings.Contains(all, "machines=5") {
		t.Errorf("--all did not run the whole registry:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY loki loki-ready") || !strings.Contains(all, "CERTIFY studio bus-push") {
		t.Errorf("--all did not run the services and coordination workloads:\n%s", all)
	}
	_ = code
}

// TestNeitherMachineNorAllIsARefusal: certify never guesses the whole fleet.
func TestNeitherMachineNorAllIsARefusal(t *testing.T) {
	_, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{}, Hash: "h", Now: fixedNow,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "--machine") || !strings.Contains(errs, "--all") {
		t.Errorf("the refusal does not name both ways to say which machines:\n%s", errs)
	}
}

// runCertify fills in the parts every case shares and captures both streams.
func runCertify(t *testing.T, in CertifyInput) (string, string, int) {
	t.Helper()
	var out, errs strings.Builder
	in.Stdout, in.Stderr = &out, &errs
	if in.Timeout == 0 {
		in.Timeout = time.Minute
	}
	if in.Workloads == nil {
		loads, err := StandardWorkloads()
		if err != nil {
			t.Fatal(err)
		}
		in.Workloads = loads
	}
	// Every machine has a registry-truth workload, so every case needs a forge. The default
	// is the empty one: no runners registered anywhere, which is the truth for a bench.
	if in.Forge == nil {
		in.Forge = fakeForge{}
	}
	if in.Repo == "" {
		in.Repo = "mas-bandwidth/nova-tools"
	}
	code := Certify(in)
	return out.String(), errs.String(), code
}

func mustRead(t *testing.T, path string) []Certificate {
	t.Helper()
	certs, err := ReadCertificates(path)
	if err != nil {
		t.Fatal(err)
	}
	return certs
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
