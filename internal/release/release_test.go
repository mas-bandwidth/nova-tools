package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The fakes. Every edge this package has to the world outside the process is an
// interface, and these three are the whole of what a unit test here talks to:
// no gh, no ssh, no compiler, no network.
// ---------------------------------------------------------------------------

type fakeForge struct {
	head     map[string]string
	checks   map[string][]CheckRun
	tags     []string
	commits  map[string][]Commit
	tagged   []string
	failTag  error
	failHead error
}

func (f *fakeForge) HeadSHA(_ context.Context, _, branch string) (string, error) {
	if f.failHead != nil {
		return "", f.failHead
	}
	sha, ok := f.head[branch]
	if !ok {
		return "", fmt.Errorf("no such branch %s", branch)
	}
	return sha, nil
}
func (f *fakeForge) CheckRuns(_ context.Context, _, sha string) ([]CheckRun, error) {
	return f.checks[sha], nil
}
func (f *fakeForge) Tags(_ context.Context, _ string) ([]string, error) { return f.tags, nil }
func (f *fakeForge) Compare(_ context.Context, _, base, head string) ([]Commit, error) {
	return f.commits[base+"..."+head], nil
}
func (f *fakeForge) Tag(_ context.Context, _, tag, sha string) error {
	if f.failTag != nil {
		return f.failTag
	}
	f.tagged = append(f.tagged, tag+" "+sha)
	return nil
}

type fakeToolchain struct {
	calls []string
	fail  string // package whose build fails
}

func (b *fakeToolchain) Build(_ context.Context, source, pkg, out, goos, goarch string, args []string) (string, error) {
	b.calls = append(b.calls, fmt.Sprintf("%s %s %s %s/%s %s", source, pkg, out, goos, goarch, strings.Join(args, " ")))
	if b.fail != "" && strings.HasSuffix(pkg, b.fail) {
		return "undefined: nope", fmt.Errorf("exit status 2")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	// A stub that is a real file with real bytes, so the checksum file the
	// caller writes is over something.
	return "", os.WriteFile(out, []byte("binary "+filepath.Base(out)+" "+strings.Join(args, " ")), 0o755)
}

type fakeSSH struct {
	runs  []string
	sends []string
	// answer is keyed by machine; a machine absent from it refuses.
	answer map[string]string
	refuse map[string]error
}

func (s *fakeSSH) Run(_ context.Context, machine string, argv []string) (string, error) {
	s.runs = append(s.runs, machine+": "+strings.Join(argv, " "))
	if err := s.refuse[machine]; err != nil {
		return "", err
	}
	return s.answer[machine], nil
}
func (s *fakeSSH) Send(_ context.Context, machine, dir, dest string) (string, error) {
	s.sends = append(s.sends, machine+": "+dir+" -> "+dest)
	if err := s.refuse[machine]; err != nil {
		return "", err
	}
	return "", nil
}

func at(t *testing.T) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, "2026-09-18T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return when
}

func greenRuns() []CheckRun {
	return []CheckRun{
		{Name: "ci", Status: "completed", Conclusion: "success"},
		{Name: "certification", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "completed", Conclusion: "skipped"},
	}
}

func cutDeps(t *testing.T, f Forge) Deps {
	t.Helper()
	return Deps{Forge: f, Now: func() time.Time { return at(t) }}
}

// ---------------------------------------------------------------------------
// cut
// ---------------------------------------------------------------------------

// A tag is a claim about source, and the one thing a release must never do is
// make that claim about a commit nobody vouched for. release.yml has said so
// since v0.15.0; this is the same refusal one step earlier, where the person
// cutting the release is still at the keyboard.
func TestCutRefusesAShaWhoseChecksAreNotGreen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		runs  []CheckRun
		wants string
	}{
		{"a failure", []CheckRun{{Name: "ci", Status: "completed", Conclusion: "failure"}}, "ci"},
		{"still running", []CheckRun{{Name: "ci", Status: "in_progress"}}, "ci"},
		{"nothing at all", nil, "no check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeForge{
				head:   map[string]string{"main": "abc123abc123"},
				checks: map[string][]CheckRun{"abc123abc123": tc.runs},
			}
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
				"--version", "v0.16.0", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")},
				&out, &errs, cutDeps(t, f))
			if code != 2 {
				t.Fatalf("green-gate did not refuse: code=%d out=%s errs=%s", code, out.String(), errs.String())
			}
			if !strings.Contains(errs.String(), "CUT REFUSED") || !strings.Contains(errs.String(), tc.wants) {
				t.Fatalf("refusal does not name the evidence: %s", errs.String())
			}
			if len(f.tagged) != 0 {
				t.Fatalf("a red sha was tagged: %v", f.tagged)
			}
		})
	}
}

func cutForge() *fakeForge {
	return &fakeForge{
		head:   map[string]string{"main": "abc123abc123def"},
		checks: map[string][]CheckRun{"abc123abc123def": greenRuns()},
		tags:   []string{"v0.15.3", "v0.9.0", "v0.15.10", "not-a-version"},
		commits: map[string][]Commit{
			"v0.15.10...abc123abc123def": {
				{SHA: "111", Message: "feat: nova-pulse hygiene, the path-safe bench cleanup verbs (#1142) (#1253)\n\nbody"},
				{SHA: "222", Message: "integration-2: 5 pre-tested PRs (deterministic idle watch) (#1312)\n\nMembers:\n- #1301 idle watch\n- #1305 sandbox parent guard\n"},
				{SHA: "333", Message: "a commit with no pull request number"},
			},
		},
	}
}

func TestCutWritesTheChangelogTagsAndSaysWhatItDid(t *testing.T) {
	f := cutForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# nova-tools changelog\n\n## v0.15.10 — 2026-09-17\n\n- #1 older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	// The one line a person reads off the terminal.
	want := "RELEASE CUT version=v0.16.0 sha=abc123abc123def prs=2"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("no cut line %q in:\n%s", want, out.String())
	}
	if len(f.tagged) != 1 || f.tagged[0] != "v0.16.0 abc123abc123def" {
		t.Fatalf("tagged %v", f.tagged)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	// The previous tag is the HIGHEST version tag, not the first one listed:
	// v0.15.10 is after v0.15.3, which a lexical sort gets backwards.
	for _, s := range []string{
		"## v0.16.0 — 2026-09-18",
		"since v0.15.10",
		"#1253",
		"feat: nova-pulse hygiene, the path-safe bench cleanup verbs (#1142)",
		"#1312",
		"integration-2: 5 pre-tested PRs (deterministic idle watch)",
		"#1301",
		"#1305",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("the new section does not carry %q:\n%s", s, got)
		}
	}
	// Prepended, never overwritten: the old section is still there and the new
	// one is above it.
	if !strings.Contains(got, "## v0.15.10 — 2026-09-17") {
		t.Fatalf("the previous section was lost:\n%s", got)
	}
	if strings.Index(got, "## v0.16.0") > strings.Index(got, "## v0.15.10") {
		t.Fatalf("the new section is below the old one:\n%s", got)
	}
	if !strings.HasPrefix(got, "# nova-tools changelog\n") {
		t.Fatalf("the file's title was displaced:\n%s", got)
	}
}

// A commit whose subject carries no (#n) is not a pull request and is not a
// changelog line; the count says 2 because two of the three commits were.
func TestCutCountsOnlyCommitsThatNameAPullRequest(t *testing.T) {
	prs := PullRequests(cutForge().commits["v0.15.10...abc123abc123def"])
	if len(prs) != 2 {
		t.Fatalf("prs=%d %v", len(prs), prs)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
	if prs[0].Number != 1253 || prs[0].Title != "feat: nova-pulse hygiene, the path-safe bench cleanup verbs (#1142)" {
		t.Fatalf("first pr %+v", prs[0])
	}
	// The batch's members come out of its own body, so a batch line does not
	// hide five pieces of work behind one number.
	if got := fmt.Sprint(prs[1].Members); got != "[1301 1305]" {
		t.Fatalf("members %s", got)
	}
}

func TestCutDryRunWritesNothingAndTagsNothing(t *testing.T) {
	f := cutForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v0.16.0",
		"--changelog", path, "--dry-run"}, &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT version=v0.16.0") || !strings.Contains(out.String(), "dry-run=yes") {
		t.Fatalf("dry run says nothing about itself: %s", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote %s", path)
	}
	if len(f.tagged) != 0 {
		t.Fatalf("--dry-run tagged %v", f.tagged)
	}
}

func TestCutRefusesAVersionNoLaterStepCouldCheck(t *testing.T) {
	for _, v := range []string{"", "0.16.0", "v0.16", "v0.16.0 rc1", "v0.16.0%s", "v0.16=0"} {
		if err := ValidVersion(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []string{"v0.16.0", "v1.0.0-rc1", "v0.15.3-0.20260918044559-d576bf6bbabb"} {
		if err := ValidVersion(v); err != nil {
			t.Errorf("refused %q: %v", v, err)
		}
	}
}

func TestEveryReleaseVerbNamesItsMissingFlagsAtOnce(t *testing.T) {
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut"}, &out, &errs, Deps{})
	if code != 2 {
		t.Fatal(code)
	}
	for _, flag := range []string{"--repo", "--from", "--version", "--changelog"} {
		if !strings.Contains(errs.String(), flag) {
			t.Fatalf("missing %s: %s", flag, errs.String())
		}
	}
	if strings.Count(errs.String(), "\n") > 1 {
		t.Fatalf("the refusal printed a banner: %s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// build
// ---------------------------------------------------------------------------

func sourceTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"nova-update", "nova-bus", "nova-swarm"} {
		if err := os.MkdirAll(filepath.Join(dir, "cmd", tool), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmd", tool, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Not a nova tool: the build loop ships cmd/nova-*, so this must not appear.
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildStampsEveryToolAndWritesOneChecksumFile(t *testing.T) {
	source, out := sourceTree(t), t.TempDir()
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: tc})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if len(tc.calls) != 3 {
		t.Fatalf("built %d packages: %v", len(tc.calls), tc.calls)
	}
	for _, call := range tc.calls {
		if !strings.Contains(call, "-trimpath") {
			t.Fatalf("no -trimpath: %s", call)
		}
		// The stamp is what makes `nova-X version` answer the release rather
		// than `devel`; an empty -X is a legal linker flag and was #118.
		if !strings.Contains(call, "-ldflags -s -w -X main.version=v0.16.0") {
			t.Fatalf("no version stamp: %s", call)
		}
		if !strings.Contains(call, "cmd/nova-") {
			t.Fatalf("built something that is not a nova tool: %s", call)
		}
	}
	plat := runtime.GOOS + "-" + runtime.GOARCH
	dir := filepath.Join(out, "v0.16.0", plat)
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(sums)), "\n") + 1; n != 3 {
		t.Fatalf("SHA256SUMS has %d lines:\n%s", n, sums)
	}
	// The checksum file cannot list itself, and every line is over a file that
	// is really there with really that hash.
	if strings.Contains(string(sums), "SHA256SUMS") {
		t.Fatalf("SHA256SUMS lists itself:\n%s", sums)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("not a sha256sum line: %q", line)
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s: %s recorded, %s on disk", name, want, got)
		}
	}
	if !strings.Contains(o.String(), "RELEASE BUILT version=v0.16.0") || !strings.Contains(o.String(), "tools=3") {
		t.Fatalf("no build line: %s", o.String())
	}
}

func TestBuildRefusesWhenOneToolDoesNotCompile(t *testing.T) {
	source, out := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: &fakeToolchain{fail: "nova-swarm"}})
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(e.String(), "nova-swarm") {
		t.Fatalf("the failure does not name the tool: %s", e.String())
	}
	// A half-built directory must not carry a checksum file: SHA256SUMS over
	// two of three tools is a file that agrees with itself and with nothing.
	if _, err := os.Stat(filepath.Join(out, "v0.16.0", runtime.GOOS+"-"+runtime.GOARCH, "SHA256SUMS")); err == nil {
		t.Fatal("a failed build still wrote SHA256SUMS")
	}
}

func TestBuildRefusesASourceTreeWithNoNovaTools(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", t.TempDir(), "--source", t.TempDir()},
		&o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 2 || !strings.Contains(e.String(), "no cmd/nova-") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

// ---------------------------------------------------------------------------
// install
// ---------------------------------------------------------------------------

// built lays down what `build` writes, so the install tests start from the
// artifact rather than from a hand-made directory that might not match it.
func built(t *testing.T, version string, tools ...string) string {
	t.Helper()
	out := t.TempDir()
	source := t.TempDir()
	for _, tool := range tools {
		if err := os.MkdirAll(filepath.Join(source, "cmd", tool), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "cmd", tool, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"build", "--version", version, "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: &fakeToolchain{}}); code != 0 {
		t.Fatalf("fixture build: %d %s", code, e.String())
	}
	return out
}

func TestInstallVerifiesRenamesAndSkipsWhatIsAlreadyCurrent(t *testing.T) {
	from := built(t, "v0.16.0", "nova-bus", "nova-swarm", "nova-wake")
	bin := t.TempDir()
	// nova-wake is already at the release; the other two are not.
	if err := os.WriteFile(filepath.Join(bin, "nova-wake"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
		&o, &e, Deps{VersionOf: func(_ context.Context, path string) (string, error) {
			if filepath.Base(path) == "nova-wake" {
				return "nova-wake v0.16.0 " + runtime.GOOS + "/" + runtime.GOARCH, nil
			}
			return "", fmt.Errorf("no such file")
		}})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if want := "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=1"; !strings.Contains(o.String(), want) {
		t.Fatalf("no install line %q in:\n%s", want, o.String())
	}
	for _, tool := range []string{"nova-bus", "nova-swarm"} {
		info, err := os.Stat(filepath.Join(bin, tool))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s is not executable: %v", tool, info.Mode())
		}
	}
	// Skipped means untouched, not overwritten with the same bytes.
	body, err := os.ReadFile(filepath.Join(bin, "nova-wake"))
	if err != nil || string(body) != "old" {
		t.Fatalf("a skipped tool was rewritten: %q %v", body, err)
	}
	// The temporary name never survives the verb.
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

func TestInstallRefusesABinaryThatDoesNotMatchItsChecksum(t *testing.T) {
	from := built(t, "v0.16.0", "nova-bus", "nova-swarm")
	dir := filepath.Join(from, "v0.16.0", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(dir, "nova-bus"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
		&o, &e, Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 2 {
		t.Fatalf("code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), "nova-bus") || !strings.Contains(e.String(), "INSTALL REFUSED") {
		t.Fatalf("refusal: %s", e.String())
	}
	// NOTHING is installed when one file fails: the set is verified whole
	// before the first rename, so a bad artifact cannot land beside good ones.
	if entries, err := os.ReadDir(bin); err != nil || len(entries) != 0 {
		t.Fatalf("a refused install still wrote %v (%v)", entries, err)
	}
}

func TestInstallRefusesAVersionThatWasNeverBuilt(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", built(t, "v0.16.0", "nova-bus"),
		"--version", "v0.17.0", "--bin", t.TempDir()}, &o, &e, Deps{})
	if code != 2 || !strings.Contains(e.String(), "v0.17.0") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

// ---------------------------------------------------------------------------
// adopt
// ---------------------------------------------------------------------------

func machinesFile(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdoptSendsInstallsAndWritesOneReceiptPerMachine(t *testing.T) {
	from := built(t, "v0.16.0", "nova-bus", "nova-update")
	list := machinesFile(t, "# the Linux benches\nhulk\nvision\n\nmini\n")
	s := &fakeSSH{answer: map[string]string{
		"hulk":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n",
		"vision": "RELEASE INSTALLED version=v0.16.0 tools=0 skipped=2\n",
		"mini":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", list,
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	for _, machine := range []string{"hulk", "vision", "mini"} {
		want := "RELEASE ADOPTED machine=" + machine + " version=v0.16.0 tools="
		if !strings.Contains(o.String(), want) {
			t.Fatalf("no receipt for %s:\n%s", machine, o.String())
		}
	}
	if !strings.Contains(o.String(), "RELEASE ADOPT OK machines=3 adopted=3 refused=0") {
		t.Fatalf("no verdict line:\n%s", o.String())
	}
	if len(s.sends) != 3 || len(s.runs) != 3 {
		t.Fatalf("sends=%v runs=%v", s.sends, s.runs)
	}
	// The release installs ITSELF: the nova-update that runs the remote
	// install is the one just sent, so a bench with no nova-tools at all can
	// still adopt. That is the whole of what fleet-install-tools.sh's nested
	// ssh quoting was doing.
	run := s.runs[0]
	for _, part := range []string{
		"/home/nova/nova-bench/build/v0.16.0/" + runtime.GOOS + "-" + runtime.GOARCH + "/nova-update",
		"release install",
		"--version v0.16.0",
		"--bin /home/nova/.local/bin",
	} {
		if !strings.Contains(run, part) {
			t.Fatalf("the remote command does not carry %q: %s", part, run)
		}
	}
}

func TestAdoptRefusesOneMachineAndStillReportsTheRest(t *testing.T) {
	from := built(t, "v0.16.0", "nova-bus", "nova-update")
	list := machinesFile(t, "hulk\nvision\n")
	s := &fakeSSH{
		answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"},
		refuse: map[string]error{"vision": fmt.Errorf("ssh: connect to host vision port 22: Connection refused")},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", list,
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build"}, &o, &e, Deps{SSH: s})
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(o.String(), "RELEASE ADOPTED machine=hulk") {
		t.Fatalf("the machine that worked has no receipt:\n%s", o.String())
	}
	line := ""
	for _, l := range strings.Split(e.String(), "\n") {
		if strings.HasPrefix(l, "RELEASE REFUSED machine=vision") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no refusal for vision:\n%s", e.String())
	}
	// A refusal with no remedy is a line that tells somebody to go and find out.
	if !strings.Contains(line, "Connection refused") || !strings.Contains(line, "ssh ") {
		t.Fatalf("the refusal carries no cause and remedy: %s", line)
	}
	if !strings.Contains(e.String(), "adopted=1 refused=1") {
		t.Fatalf("the verdict does not count both:\n%s", e.String())
	}
}

// A remote that answered something other than the install line is not an
// adoption, however cheerfully it exited: the receipt is read from the output,
// never from the exit code.
func TestAdoptRefusesAMachineWhoseInstallSaidNothing(t *testing.T) {
	from := built(t, "v0.16.0", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "bash: nova-update: command not found\n"}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: s})
	if code != 1 || !strings.Contains(e.String(), "RELEASE REFUSED machine=hulk") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

func TestAdoptRefusesAMachineNameThatIsNotOne(t *testing.T) {
	from := built(t, "v0.16.0", "nova-update")
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk; rm -rf /\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		t.Fatalf("code=%d out=%s", code, o.String())
	}
	if len(s.runs) != 0 || len(s.sends) != 0 {
		t.Fatalf("a bad machine name still reached ssh: %v %v", s.runs, s.sends)
	}
}

func TestAdoptRefusesAnEmptyMachineList(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "# nobody\n\n"), "--ssh", "/usr/bin/ssh",
		"--from", built(t, "v0.16.0", "nova-update"), "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "no machine") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

// ---------------------------------------------------------------------------
// the forge edge
// ---------------------------------------------------------------------------

// The first real `cut --dry-run` against this repository asked the compare
// endpoint for v0.15.2...dev -- 551 pull requests, well over the 64 KiB cap
// every other child here is held to. The capture cancelled the child, and what
// the verb printed was `signal: killed`: a refusal naming a symptom, with a
// remedy about the forge answering, for a cause that was entirely ours. The
// ceiling stays -- a runaway gh must not be able to fill memory -- but reaching
// it says so by name.
func TestAForgeReadThatFillsTheCeilingSaysSoRatherThanKilled(t *testing.T) {
	args := []string{"api", "repos/o/n/compare/v0.15.2...head"}
	err := apiError(args, true, fmt.Errorf("signal: killed"))
	if err == nil {
		t.Fatal("a capture that hit the ceiling was reported as success")
	}
	if strings.Contains(err.Error(), "killed") {
		t.Fatalf("the refusal still names the signal: %v", err)
	}
	for _, s := range []string{"ceiling", "nearer tag", "compare"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("the refusal does not carry %q: %v", s, err)
		}
	}
	// An ordinary failure is still an ordinary failure, with gh's own words.
	err = apiError(args, false, fmt.Errorf("HTTP 404"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a real failure lost its cause: %v", err)
	}
	if err := apiError(args, false, nil); err != nil {
		t.Fatalf("a read that answered was reported as an error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// the verb itself
// ---------------------------------------------------------------------------

func TestReleaseRefusesAnUnknownSubverbAndNamesTheFour(t *testing.T) {
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"ship"}, &o, &e, Deps{}); code != 2 {
		t.Fatal(code)
	}
	for _, verb := range []string{"cut", "build", "install", "adopt"} {
		if !strings.Contains(e.String(), verb) {
			t.Fatalf("the refusal does not name %s: %s", verb, e.String())
		}
	}
	o.Reset()
	e.Reset()
	if code := Run("nova-update", nil, &o, &e, Deps{}); code != 2 {
		t.Fatal(code)
	}
}

// Progress belongs on stderr and the result on stdout, so that a caller reading
// the receipt off stdout reads receipts and nothing else (Glenn 2026-09-17:
// programs say what they are doing).
func TestProgressGoesToStderrAndReceiptsToStdout(t *testing.T) {
	from := built(t, "v0.16.0", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("%d %s", code, e.String())
	}
	if !strings.Contains(e.String(), "release: ") {
		t.Fatalf("no progress on stderr: %q", e.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(o.String()), "\n") {
		if line != "" && !strings.HasPrefix(line, "RELEASE ") {
			t.Fatalf("stdout carries something that is not a receipt: %q", line)
		}
	}
}
