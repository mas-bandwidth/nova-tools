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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	loads, err := StandardWorkloads()
	require.NoError(t, err)
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
	require.Len(t, loads, len(want), "the standard set ships %d workloads, want %d", len(loads), len(want))
	byClass := map[string]Workload{}
	for _, w := range loads {
		byClass[w.Class] = w
	}
	for class, role := range want {
		w, ok := byClass[class]
		if !assert.True(t, ok, "the standard set ships no %s workload", class) {
			continue
		}
		assert.True(t, w.AppliesTo(role), "%s does not apply to %s (roles=%s)", class, role, strings.Join(w.Roles, ","))
		assert.NotNil(t, w.Expect, "%s carries no expect", class)
	}
	// The wall is the whole point of go-test: the card that died on hulk died inside it.
	assert.True(t, byClass["go-test"].Wall, "go-test does not run inside the wall; a plain ssh is exactly the check that passed while the card died")
	assert.NotEmpty(t, byClass["go-test"].Reads, "go-test names no read root for the wall; the toolchain outside the wall is the failure being certified against")
	assert.True(t, byClass["git-push"].Wall, "git-push, sbcl and wall-toolchain must run inside the wall: a card pushes and builds from inside it")
	assert.True(t, byClass["sbcl"].Wall, "git-push, sbcl and wall-toolchain must run inside the wall: a card pushes and builds from inside it")
	assert.True(t, byClass["wall-toolchain"].Wall, "git-push, sbcl and wall-toolchain must run inside the wall: a card pushes and builds from inside it")
	// These three are plain ssh ON PURPOSE. What they certify is the machine as a CI runner
	// or as a neighbour on the network, and wrapping them would certify the wall instead.
	for _, class := range []string{"services-reach", "path-resolves", "go-on-path", "runner-path"} {
		assert.False(t, byClass[class].Wall, "%s is a plain ssh by the card, not a wall workload", class)
	}
	assert.Equal(t, ForgeRunners, byClass["runner-online"].Forge, "runner-online must ask the forge, not a machine")
	assert.Equal(t, ForgeRegistry, byClass["registry-truth"].Forge, "registry-truth must hold the registry against the forge, not ask a machine")
	// The one report-only class, and the only one: a WARN that spreads is a WARN nobody reads.
	for _, w := range loads {
		assert.True(t, !w.Report || w.Class == "diag-size", "%s is report-only; only diag-size is", w.Class)
	}
	assert.True(t, byClass["diag-size"].Report, "diag-size must be report-only: 15.7 GB of _diag broke nothing and a FAIL for it is a check people learn to pass over")
	// path-resolves and registry-truth apply to EVERY role: a stale shadow tool and a
	// registry that disagrees with the forge are faults of any machine, not of a bench.
	for _, class := range []string{"path-resolves", "registry-truth"} {
		for _, role := range []string{RoleBench, RoleRunner, RoleServices, RoleCoordination} {
			assert.True(t, byClass[class].AppliesTo(role), "%s does not apply to %s", class, role)
		}
	}
}

// TestAWorkloadIsAFileWithFrontMatter reads one from disk and holds every field.
func TestAWorkloadIsAFileWithFrontMatter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.card")
	mustWrite(t, path, strings.Join([]string{
		"roles: bench,services",
		"expect: ^TINY OK",
		"wall: yes",
		"reads: $HOME/sdk, /usr/lib",
		"",
		"echo TINY OK",
	}, "\n")+"\n")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	w, err := ParseWorkload(path, raw)
	require.NoError(t, err)
	assert.Equal(t, "tiny", w.Class, "class = %q, want tiny (the class is the file name)", w.Class)
	assert.True(t, w.AppliesTo(RoleBench), "roles = %v", w.Roles)
	assert.True(t, w.AppliesTo(RoleServices), "roles = %v", w.Roles)
	assert.False(t, w.AppliesTo(RoleRunner), "roles = %v", w.Roles)
	assert.True(t, w.Wall, "wall: yes was not read")
	assert.Equal(t, []string{"$HOME/sdk", "/usr/lib"}, w.Reads, "reads = %v", w.Reads)
	assert.Equal(t, "echo TINY OK", strings.TrimSpace(w.Body), "body = %q", w.Body)
	assert.True(t, w.Expect.MatchString("TINY OK go=go1.26.5"), "the expect regexp does not match its own body's line")
}

// TestAWorkloadWithoutARoleOrAnExpectIsRefused: a workload that applies to nothing, or that
// cannot say what a pass looks like, is a certificate that means nothing.
func TestAWorkloadWithoutARoleOrAnExpectIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body, want string }{
		{"no roles", "expect: ^OK\n\necho OK\n", "roles"},
		{"no expect", "roles: bench\n\necho OK\n", "expect"},
		{"unknown role", "roles: potato\nexpect: ^OK\n\necho OK\n", "potato"},
		{"bad regexp", "roles: bench\nexpect: ^(\n\necho OK\n", "expect"},
		{"no body", "roles: bench\nexpect: ^OK\n\n\n", "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseWorkload("broken.card", []byte(tc.body))
			require.Error(t, err, "a broken workload was read without a refusal")
			assert.Contains(t, err.Error(), tc.want, "refusal %q does not name %q", err, tc.want)
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
	t.Parallel()

	standard := writeFile(t, "standard.go", "echo standard v1\n")
	loads, err := StandardWorkloads()
	require.NoError(t, err)
	first, err := StandardHash(standard, loads)
	require.NoError(t, err)
	again, err := StandardHash(standard, loads)
	require.NoError(t, err)
	require.Equal(t, again, first, "the hash is not stable: %s then %s", first, again)
	moved := writeFile(t, "standard.go", "echo standard v2\n")
	h, _ := StandardHash(moved, loads)
	assert.NotEqual(t, first, h, "the standard file changed and the hash did not")
	extra := append(append([]Workload{}, loads...), Workload{
		Class: "zzz", Roles: []string{RoleBench}, Expect: regexp.MustCompile("^OK"), Body: "echo OK",
	})
	h, _ = StandardHash(standard, extra)
	assert.NotEqual(t, first, h, "a workload was added and the hash did not move")
}

// ---------------------------------------------------------------------------
// certificates
// ---------------------------------------------------------------------------

// TestCertifiedIsTrueOnlyForTheCurrentBuildAndHash is the whole currency rule.
func TestCertifiedIsTrueOnlyForTheCurrentBuildAndHash(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "certs.tsv", "")
	at := fixedNow()
	for _, c := range []Certificate{
		{Machine: "space", Build: "v0.17.0", Hash: "abc", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at},
		{Machine: "space", Build: "v0.17.0", Hash: "abc", Class: "sbcl", Verdict: VerdictFail, Evidence: "sbcl: not found", At: at},
		{Machine: "hulk", Build: "v0.16.0", Hash: "abc", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at},
	} {
		require.NoError(t, AppendCertificate(path, c))
	}
	certs, err := ReadCertificates(path)
	require.NoError(t, err)
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
			got := Certified(certs, tc.machine, tc.class, tc.build, tc.hash)
			assert.Equal(t, tc.want, got, "Certified = %v, want %v", got, tc.want)
		})
	}
}

// TestTheLastRowWins: certify appends, so a machine that failed and was repaired is current
// on its newest row and not on its oldest.
func TestTheLastRowWins(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "certs.tsv", "")
	at := fixedNow()
	must(t, AppendCertificate(path, Certificate{Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictFail, Evidence: "go1.22 refused by go.mod", At: at}))
	must(t, AppendCertificate(path, Certificate{Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictOK, Evidence: "go version go1.26.5", At: at.Add(time.Hour)}))
	certs, err := ReadCertificates(path)
	require.NoError(t, err)
	assert.True(t, Certified(certs, "hulk", "go-test", "v1", "h"), "the repaired bench is not certified; the newest row must win")
}

// TestEvidenceIsOneLineOnTheRowAndOnTheLine: a workload that fails with a screenful must not
// be able to write a second line into the certificates file or into the output.
func TestEvidenceIsOneLineOnTheRowAndOnTheLine(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "certs.tsv", "")
	must(t, AppendCertificate(path, Certificate{
		Machine: "hulk", Build: "v1", Hash: "h", Class: "go-test", Verdict: VerdictFail,
		Evidence: "first line\nsecond line\tand a tab", At: fixedNow(),
	}))
	raw := readFile(t, path)
	assert.Equal(t, 0, strings.Count(strings.TrimRight(raw, "\n"), "\n"), "the certificates file holds %d rows for one certificate:\n%s", strings.Count(raw, "\n"), raw)
	assert.Equal(t, 6, strings.Count(raw, "\t"), "a row has 7 fields and so 6 tabs, got %d: %q", strings.Count(raw, "\t"), raw)
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
	t.Parallel()

	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: benchOK()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 0, code, "exit = %d, want 0\nstdout:%s\nstderr:%s", code, out, errs)
	for _, class := range spaceBenchClasses {
		assert.Contains(t, out, "CERTIFY space "+class+" OK evidence=", "no OK line for %s:\n%s", class, out)
	}
	assert.Contains(t, out, fmt.Sprintf("CERTIFY OK machines=1 ok=%d fail=0 warn=0", len(spaceBenchClasses)), "no closing line:\n%s", out)
	rows, err := ReadCertificates(certs)
	require.NoError(t, err)
	require.Len(t, rows, len(spaceBenchClasses), "wrote %d certificate rows, want %d", len(rows), len(spaceBenchClasses))
	for _, r := range rows {
		assert.Equal(t, "v0.17.0", r.Build, "%s carries build %q; the build defaults to the machine's own nova-merge version line", r.Class, r.Build)
		assert.Equal(t, "h", r.Hash, "%s carries hash %q", r.Class, r.Hash)
	}
	// No runner, services or coordination workload may have been run on a bench.
	for _, s := range remote.scripts() {
		k := scriptKey(s)
		assert.NotEqual(t, "loki-ready", k, "a %s workload ran on a bench; workloads follow the machine's roles", k)
		assert.NotEqual(t, "release-path", k, "a %s workload ran on a bench; workloads follow the machine's roles", k)
		assert.NotEqual(t, "runner-path", k, "a %s workload ran on a bench; workloads follow the machine's roles", k)
	}
}

// TestAFailingWorkloadIsOneFAILRowAndExitOne: the verdict comes from what the machine SAID
// matched against expect, never from the exit code alone -- the hulk card exited non-zero
// for a reason the exit code could not name.
func TestAFailingWorkloadIsOneFAILRowAndExitOne(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, 1, code, "exit = %d, want 1", code)
	all := out + errs
	assert.Contains(t, all, "CERTIFY space go-test FAIL evidence=", "no FAIL line:\n%s", all)
	assert.Contains(t, all, "go.mod requires go >= 1.26.5", "the FAIL line does not carry what the machine said:\n%s", all)
	assert.Contains(t, all, "fail=1", "the closing line does not count the failure:\n%s", all)
	assert.True(t, Certified(mustRead(t, certs), "space", "c-build", "v0.17.0", "h"), "one failing workload took the passing ones with it; each class is its own certificate")
	assert.False(t, Certified(mustRead(t, certs), "space", "go-test", "v0.17.0", "h"), "a FAIL was written as a certificate")
}

// TestGoTestRunsInsideTheWallWithTheToolchainAsAReadRoot is the hurt, as a test: the script
// certify composes for a wall workload wraps the body in nova-sandbox with a writable job
// directory, a HOME inside it, and the toolchain named as a read root.
func TestGoTestRunsInsideTheWallWithTheToolchainAsAReadRoot(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{answers: benchOK()}
	_, _, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 0, code, "exit = %d", code)
	var script string
	for _, s := range remote.scripts() {
		if scriptKey(s) == "go-test" {
			script = s
		}
	}
	require.NotEqual(t, "", script, "no go-test script was sent")
	for _, want := range []string{"nova-sandbox", "--write", "--read", "HOME="} {
		assert.Contains(t, script, want, "the go-test script carries no %s; it is not the wall:\n%s", want, script)
	}
	assert.Contains(t, script, "sdk", "the go-test script does not name the toolchain as a read root -- this is the whole failure it certifies against:\n%s", script)
	// And a plain-ssh workload must NOT be wrapped: a redis PING from the bench is not a
	// card, and wrapping it would certify the wall instead of the reachability.
	for _, s := range remote.scripts() {
		assert.False(t, scriptKey(s) == "services-reach" && strings.Contains(s, "nova-sandbox"), "the services-reach workload was wrapped in the wall; the card says plain ssh")
	}
}

// TestGitPushMakesItsOwnScratchRemoteAndTakesItAway: the push workload must not leave a bare
// repository behind, and must not push to anything but the throwaway it made.
func TestGitPushMakesItsOwnScratchRemoteAndTakesItAway(t *testing.T) {
	t.Parallel()

	loads, err := StandardWorkloads()
	require.NoError(t, err)
	var body string
	for _, w := range loads {
		if w.Class == "git-push" {
			body = w.Body
		}
	}
	require.NotEqual(t, "", body, "no git-push workload")
	for _, want := range []string{"git init --bare", "git push", "rm -rf"} {
		assert.Contains(t, body, want, "git-push does not %s:\n%s", want, body)
	}
	assert.NotContains(t, body, "github.com", "git-push names a real remote; the remote is a bare repository made on the machine for the run")
}

// TestRunnerPathProbesBothSystemdScopesAndBothUnitNamings is a correction, and it is the
// most expensive lesson of 2026-09-18: the first version of the check probed
// `systemctl --user` only, called space's sixteen SYSTEM units unmanaged, and acting on that
// finding installed svc.sh beside them -- thirty-two Runner.Listener processes for sixteen
// units, both halves taking merge-group shards, for ten minutes. A check that can be wrong
// in that direction must hold the listener count against the unit count.
func TestRunnerPathProbesBothSystemdScopesAndBothUnitNamings(t *testing.T) {
	t.Parallel()

	body := workloadBody(t, "runner-path")
	for _, want := range []string{
		"--system", "--user", "nova-runner-*", "actions.runner.*", "launchctl",
		"Runner.Listener", "double registration",
	} {
		assert.Contains(t, body, want, "runner-path does not probe %q:\n%s", want, body)
	}
	// The listener count must be COMPARED, not merely read: a count nobody compares is a
	// number in a log.
	assert.Contains(t, body, `[ "$LISTENERS" -gt "$UNITS" ]`, "runner-path reads the listeners and never holds them against the units:\n%s", body)
}

// TestDiagSizeReportsTheRateAndNotOnlyTheSize: hulk held 3576 MB with nothing older than two
// days -- about 1.8 GB a day -- so a seven-day retention rule cannot bound it and a size read
// alone cannot say so.
func TestDiagSizeReportsTheRateAndNotOnlyTheSize(t *testing.T) {
	t.Parallel()

	body := workloadBody(t, "diag-size")
	for _, want := range []string{"MB/day", "oldest", "RATE=$((TOTAL / DAYS))"} {
		assert.Contains(t, body, want, "diag-size does not report the growth rate (%q missing):\n%s", want, body)
	}
}

// TestServicesReachDistinguishesRefusedFromDeniedFromPONG. The three answers have three
// different remedies, and one word for all of them sends a person to the wrong machine:
// redis on space is bound on the tailnet with protected mode and no password, so a PING from
// a bench is DENIED -- the name and the path work and only the password gate remains.
func TestServicesReachDistinguishesRefusedFromDeniedFromPONG(t *testing.T) {
	t.Parallel()

	body := workloadBody(t, "services-reach")
	for _, want := range []string{"PONG", "protected mode", "refused", "requirepass", "nova-secrets"} {
		assert.Contains(t, body, want, "services-reach does not name %q:\n%s", want, body)
	}
	// Only PONG is OK: the two other cases must exit non-zero before the OK line.
	denied := strings.Index(body, "denied (protected mode)")
	ok := strings.LastIndex(body, "SERVICES OK $TRIED")
	assert.GreaterOrEqual(t, denied, 0, "the denied case does not come before the OK line:\n%s", body)
	assert.GreaterOrEqual(t, ok, 0, "the denied case does not come before the OK line:\n%s", body)
	assert.LessOrEqual(t, denied, ok, "the denied case does not come before the OK line:\n%s", body)
	// And no tailnet address is REQUIRED: the Studio has none and a LAN address is as good
	// an answer, so the check is that the name resolves at all.
	assert.NotContains(t, body, "100.", "services-reach hard-codes a tailnet address; the check is that the NAME resolves:\n%s", body)
}

// workloadBody is one shipped workload's body, by class.
func workloadBody(t *testing.T, class string) string {
	t.Helper()
	loads, err := StandardWorkloads()
	require.NoError(t, err)
	for _, w := range loads {
		if w.Class == class {
			return w.Body
		}
	}
	t.Fatalf("no %s workload ships", class)
	return ""
}

// TestRunnerOnlineAsksTheForgeAndNamesTheRunnerThatIsNot.
func TestRunnerOnlineAsksTheForgeAndNamesTheRunnerThatIsNot(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, 1, code, "exit = %d, want 1 (batman-nova-2 is offline)\n%s", code, all)
	assert.Contains(t, all, "CERTIFY batman runner-online FAIL", "the refusal does not name the runner that is offline:\n%s", all)
	assert.Contains(t, all, "batman-nova-2", "the refusal does not name the runner that is offline:\n%s", all)
	assert.Contains(t, all, "CERTIFY batman runner-path ", "the runner's .path was not certified:\n%s", all)
	assert.NotContains(t, all, "vision-nova-1", "another machine's runners were counted; the rule is <machine>-nova-*")
}

// TestAnUnknownMachineIsRefusedByNameBeforeAnySSH.
func TestAnUnknownMachineIsRefusedByNameBeforeAnySSH(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{answers: benchOK()}
	_, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "batmobile", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 2, code, "exit = %d, want 2", code)
	assert.Contains(t, errs, "unknown-machine", "the refusal does not name the machine or the reason:\n%s", errs)
	assert.Contains(t, errs, "batmobile", "the refusal does not name the machine or the reason:\n%s", errs)
	assert.Empty(t, remote.calls, "an unknown machine was reached %d times before the refusal", len(remote.calls))
}

// TestDryRunSaysWhatItWouldRunAndWritesNothing.
func TestDryRunSaysWhatItWouldRunAndWritesNothing(t *testing.T) {
	t.Parallel()

	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: benchOK()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs, DryRun: true,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 0, code, "exit = %d, want 0\n%s%s", code, out, errs)
	assert.Empty(t, remote.calls, "--dry-run reached the machine %d times", len(remote.calls))
	body := readFile(t, certs)
	assert.Equal(t, "", strings.TrimSpace(body), "--dry-run wrote certificates:\n%s", body)
	assert.Contains(t, out, "CERTIFY space go-test WOULD", "--dry-run does not say what it would run:\n%s", out)
}

// TestAllRunsEveryMachineInTheRegistryUnderItsOwnRoles.
func TestAllRunsEveryMachineInTheRegistryUnderItsOwnRoles(t *testing.T) {
	t.Parallel()

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
	assert.Contains(t, all, "machines=5", "--all did not run the whole registry:\n%s", all)
	assert.Contains(t, all, "CERTIFY loki loki-ready", "--all did not run the services and coordination workloads:\n%s", all)
	assert.Contains(t, all, "CERTIFY studio release-path", "--all did not run the services and coordination workloads:\n%s", all)
	_ = code
}

// TestNeitherMachineNorAllIsARefusal: certify never guesses the whole fleet.
func TestNeitherMachineNorAllIsARefusal(t *testing.T) {
	t.Parallel()

	_, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{}, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 2, code, "exit = %d, want 2", code)
	assert.Contains(t, errs, "--machine", "the refusal does not name both ways to say which machines:\n%s", errs)
	assert.Contains(t, errs, "--all", "the refusal does not name both ways to say which machines:\n%s", errs)
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
		require.NoError(t, err)
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
	require.NoError(t, err)
	return certs
}

func must(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// TestBuildVersionRejectsErrorAndBannerText verifies that BuildVersion extracts valid
// build version tokens and rejects SSH banners, diagnostics, and error messages.
func TestBuildVersionRejectsErrorAndBannerText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "semver tag with binary prefix",
			out:  "nova-merge v0.17.0\n",
			want: "v0.17.0",
		},
		{
			name: "four token buildinfo line",
			out:  "nova-merge v0.17.0 linux/amd64 go1.26.5 build=ed95537c43b4\n",
			want: "v0.17.0",
		},
		{
			name: "vcs hex revision",
			out:  "nova-merge ed95537c43b4\n",
			want: "ed95537c43b4",
		},
		{
			name: "devel build",
			out:  "nova-merge devel\n",
			want: "devel",
		},
		{
			name: "pseudo version",
			out:  "nova-merge 20260921145725-c1670c8884cd\n",
			want: "20260921145725-c1670c8884cd",
		},
		{
			name: "banner followed by valid version",
			out:  "Authorized uses only. All activity may be monitored.\nnova-merge v0.17.0\n",
			want: "v0.17.0",
		},
		{
			name: "banner only rejected",
			out:  "Authorized uses only. All activity may be monitored.\n",
			want: "",
		},
		{
			name: "ssh connection refused rejected",
			out:  "ssh: connect to host hulk port 22: Connection refused\n",
			want: "",
		},
		{
			name: "command not found rejected",
			out:  "bash: nova-merge: command not found\n",
			want: "",
		},
		{
			name: "permission denied rejected",
			out:  "Permission denied (publickey).\n",
			want: "",
		},
		{
			name: "diagnostic text rejected",
			out:  "fatal: not a git repository (or any of the parent directories): .git\n",
			want: "",
		},
		{
			name: "diagnostic containing a hex revision rejected",
			out:  "fatal: bad object deadbeef1234\n",
			want: "",
		},
		{
			name: "two field diagnostic with hex token rejected (no nova-merge tool field)",
			out:  "fatal deadbeef1234\n",
			want: "",
		},
		{
			name: "three field diagnostic with malformed platform rejected (empty goarch)",
			out:  "nova-merge deadbeef1234 bad/\n",
			want: "",
		},
		{
			name: "three field platform with an extra slash rejected",
			out:  "nova-merge deadbeef1234 linux/amd64/extra\n",
			want: "",
		},
		{
			name: "three field platform with a repeated slash rejected",
			out:  "nova-merge deadbeef1234 linux//amd64\n",
			want: "",
		},
		{
			name: "four token buildinfo line with an extra platform slash rejected",
			out:  "nova-merge deadbeef1234 linux/amd64/extra go1.26.5\n",
			want: "",
		},
		{
			name: "four token buildinfo line from a different tool rejected",
			out:  "nova-sandbox v0.17.0 linux/amd64 go1.26.5\n",
			want: "",
		},
		{
			name: "empty output",
			out:  "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildVersion(tc.out)
			assert.Equal(t, tc.want, got, "BuildVersion(%q) = %q, want %q", tc.out, got, tc.want)
		})
	}
}

// TestMachineBuildProbeFailsOnRunScriptError tests that a failure in the machine-build probe's
// remote script execution fails the certification, prints FAIL to stderr, and writes no row.
func TestMachineBuildProbeFailsOnRunScriptError(t *testing.T) {
	t.Parallel()

	certs := writeFile(t, "certs.tsv", "")
	ans := benchOK()
	ans["space|build"] = remoteAnswer{err: errors.New("ssh: connection timed out")}
	remote := &fakeRemote{answers: ans}

	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 1, code, "exit = %d, want 1\nstdout:%s\nstderr:%s", code, out, errs)
	assert.Contains(t, errs, "CERTIFY space build FAIL evidence=", "stderr missing build FAIL line:\n%s", errs)
	assert.Contains(t, errs, "ssh: connection timed out", "stderr does not name the connection error:\n%s", errs)
	rows := mustRead(t, certs)
	require.Empty(t, rows, "wrote %d certificate rows on build probe failure, want 0", len(rows))
}

// TestMachineBuildProbeFailsOnBannerOrErrorOutput tests that when the build probe script exits 0
// but outputs banner or error text without a valid build version, certification fails cleanly with no row.
func TestMachineBuildProbeFailsOnBannerOrErrorOutput(t *testing.T) {
	t.Parallel()

	certs := writeFile(t, "certs.tsv", "")
	ans := benchOK()
	ans["space|build"] = remoteAnswer{out: "Authorized uses only. All activity may be monitored.\n"}
	remote := &fakeRemote{answers: ans}

	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	require.Equal(t, 1, code, "exit = %d, want 1\nstdout:%s\nstderr:%s", code, out, errs)
	assert.Contains(t, errs, "CERTIFY space build FAIL evidence=", "stderr missing build FAIL line:\n%s", errs)
	assert.Contains(t, errs, "no valid build version in output", "stderr does not explain missing version:\n%s", errs)
	rows := mustRead(t, certs)
	require.Empty(t, rows, "wrote %d certificate rows on invalid build output, want 0", len(rows))
}

// TestCertifyRefusesInvalidBuildOverride tests that passing an invalid build version string
// to Certify causes failure and writes no certificate rows.
func TestCertifyRefusesInvalidBuildOverride(t *testing.T) {
	t.Parallel()

	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: benchOK()}

	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
		Build: "ssh: Connection refused",
	})
	require.Equal(t, 1, code, "exit = %d, want 1\nstdout:%s\nstderr:%s", code, out, errs)
	assert.Contains(t, errs, "CERTIFY space build FAIL evidence=\"invalid build version", "stderr missing invalid build version refusal:\n%s", errs)
	rows := mustRead(t, certs)
	require.Empty(t, rows, "wrote %d certificate rows on invalid build override, want 0", len(rows))
}
