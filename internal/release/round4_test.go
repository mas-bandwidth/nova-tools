package release

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The fourth release dogfood (rowan-child-release-4, 2026-09-18), one test per
// numbered lesson in docs/SPEC-RELEASE.md. Every one of these failed before the
// change that follows it, and none of them reaches the network or a machine.
// ---------------------------------------------------------------------------

// fakeGit is the local checkout `cut --local-diff` reads. It answers one range
// and records what it was asked, so a test can assert the verb composed
// `<base>...<head>` rather than `<base>..<head>` -- two ranges that differ by
// every commit the base has that the head does not.
type fakeGit struct {
	names map[string][]string
	asked []string
	fail  error
}

func (g *fakeGit) DiffNames(_ context.Context, dir, base, head string) ([]string, error) {
	g.asked = append(g.asked, dir+" "+base+"..."+head)
	if g.fail != nil {
		return nil, g.fail
	}
	return g.names[base+"..."+head], nil
}

// truncatedForge answers the compare with exactly CompareFileCap files, of
// which some are sensitive: the shape of the fourth dogfood's range, where the
// forge named 300 and the range really touched 58 sensitive paths.
func truncatedForge() *fakeForge {
	f := cutForge()
	many := []string{"internal/secrets/seal.go", "cmd/nova-sandbox/main.go"}
	for i := len(many); i < CompareFileCap; i++ {
		many = append(many, fmt.Sprintf("docs/ordinary-%d.md", i))
	}
	f.files = map[string][]string{"v0.15.10...abc123abc123def": many}
	return f
}

// LESSON 4. THE TRUNCATION IS NAMED FIRST. The fourth dogfood's compare
// answered with exactly 300 files -- the forge's ceiling -- and `cut` refused
// naming 24 sensitive paths out of the 58 the range really touched. The
// refusal was right by accident: the hits it found were in the prefix it could
// see, and a range whose only sensitive file sat past file 300 would have been
// cut clean. A list that may be short is not classified at all, and the
// refusal says THAT before it says anything about what it found in it.
func TestCutNamesTheTruncationBeforeTheHitsItFoundInIt(t *testing.T) {
	f := truncatedForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("a truncated file list was classified anyway: code=%d out=%s", code, out.String())
	}
	for _, want := range []string{
		"RELEASE CUT REFUSED",
		"reason=compare-truncated",
		fmt.Sprintf("files=%d", CompareFileCap),
		"--paths-from",
		"--local-diff",
		"v0.15.10...abc123abc123def",
	} {
		if !strings.Contains(errs.String(), want) {
			t.Errorf("the truncation refusal does not carry %q:\n%s", want, errs.String())
		}
	}
	if len(f.tagged) != 0 {
		t.Fatalf("a refused cut tagged %v", f.tagged)
	}
}

// AND JOHNNY'S READ DOES NOT GET PAST IT. --security-read is a read OF A LIST,
// and the list is the thing that may be short: a read of a prefix of the truth
// vouches for a prefix of the truth. The only way past a truncated compare is
// a complete list.
func TestCutRefusesATruncatedRangeEvenWithASecurityRead(t *testing.T) {
	f := truncatedForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path,
		"--security-read", "johnny-4b9200ddc994"}, &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("a read got past a truncated list: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(errs.String(), "reason=compare-truncated") {
		t.Fatalf("the refusal is not the truncation one: %s", errs.String())
	}
	if len(f.tagged) != 0 {
		t.Fatalf("a refused cut tagged %v", f.tagged)
	}
}

// LESSON 5. THE LOCAL LIST IS PRODUCED BY THE VERB. --local-diff names a
// checkout and `cut` runs git in it, so the complete list is the tool's answer
// rather than something a person assembled -- and a hand-written list is
// exactly what a classification gate must never read.
func TestCutLocalDiffClassifiesTheCompleteListItProduced(t *testing.T) {
	f := truncatedForge()
	g := &fakeGit{names: map[string][]string{
		// The complete range: the two the forge's prefix showed, plus one it
		// never reached -- the file the ceiling hid.
		"v0.15.10...abc123abc123def": {
			"internal/secrets/seal.go",
			"cmd/nova-sandbox/main.go",
			"internal/sandbox/run.go",
			"README.md",
		},
	}}
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	deps := cutDeps(t, f)
	deps.Git = g
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path, "--local-diff", dir}, &out, &errs, deps)
	// Still a refusal -- the complete list is sensitive -- but for the RIGHT
	// reason, and naming the path the truncated list could not see.
	if code != 2 {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
	if strings.Contains(errs.String(), "compare-truncated") {
		t.Fatalf("the local list was still treated as truncated: %s", errs.String())
	}
	for _, want := range []string{"internal/sandbox/run.go", "--security-read", "3 paths"} {
		if !strings.Contains(errs.String(), want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, errs.String())
		}
	}
	if len(g.asked) != 1 || g.asked[0] != dir+" v0.15.10...abc123abc123def" {
		t.Fatalf("git was asked %v, want one three-dot range in %s", g.asked, dir)
	}
}

// With the read named, a complete local list cuts, and the receipt says the
// classification did not come from the forge: which list a gate read is the
// first thing anybody auditing it asks.
func TestCutLocalDiffWithAReadSaysWhereTheListCameFrom(t *testing.T) {
	f := truncatedForge()
	g := &fakeGit{names: map[string][]string{
		"v0.15.10...abc123abc123def": {"internal/secrets/seal.go", "README.md"},
	}}
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	deps := cutDeps(t, f)
	deps.Git = g
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path, "--local-diff", dir,
		"--security-read", "johnny-4b9200ddc994"}, &out, &errs, deps)
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT PATHS source=local-diff files=2") {
		t.Fatalf("no paths receipt in:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT SENSITIVE paths=1 read=johnny-4b9200ddc994") {
		t.Fatalf("no sensitive line in:\n%s", out.String())
	}
}

// --local-diff with --paths-from WRITES the list, so the same complete answer
// can be carried to the host doing the cut and read back there. The file
// carries the range it was produced for.
func TestCutLocalDiffWritesThePathsFileItClassified(t *testing.T) {
	f := truncatedForge()
	g := &fakeGit{names: map[string][]string{
		"v0.15.10...abc123abc123def": {"README.md", "docs/SPEC-UPDATE.md"},
	}}
	dir := t.TempDir()
	paths := filepath.Join(dir, "paths.txt")
	deps := cutDeps(t, f)
	deps.Git = g
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", filepath.Join(dir, "CHANGELOG.md"),
		"--local-diff", dir, "--paths-from", paths}, &out, &errs, deps)
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	written, err := os.ReadFile(paths)
	if err != nil {
		t.Fatalf("--local-diff --paths-from wrote no file: %v", err)
	}
	for _, want := range []string{PathsHeaderPrefix, "v0.15.10...abc123abc123def", "README.md", "docs/SPEC-UPDATE.md"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the written list does not carry %q:\n%s", want, string(written))
		}
	}
	// And it reads back on a second cut, with no git and no forge file list.
	f2 := truncatedForge()
	var out2, errs2 bytes.Buffer
	if code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.1", "--changelog", filepath.Join(dir, "CHANGELOG.md"),
		"--paths-from", paths}, &out2, &errs2, cutDeps(t, f2)); code != 0 {
		t.Fatalf("reading back the list refused: code=%d errs=%s", code, errs2.String())
	}
	if !strings.Contains(out2.String(), "RELEASE CUT PATHS source=paths-from files=2") {
		t.Fatalf("no paths receipt in:\n%s", out2.String())
	}
}

// A HAND-WRITTEN LIST IS NOT AN ANSWER. The file has to be the one the verb
// produced, and it has to be for THIS range: a list somebody typed, or one left
// over from a different pair of commits, classifies a range that is not the one
// being cut.
func TestCutRefusesAPathsFileNobodyProduced(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body, wants string
	}{
		{"hand written", "internal/secrets/seal.go\nREADME.md\n", "was not written by"},
		{"another range", PathsHeaderPrefix + "v0.15.3...deadbeef\nREADME.md\n", "v0.15.10...abc123abc123def"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := filepath.Join(dir, tc.name+".txt")
			if err := os.WriteFile(paths, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			f := truncatedForge()
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
				"--version", "v0.16.0", "--changelog", filepath.Join(dir, "CHANGELOG.md"),
				"--paths-from", paths}, &out, &errs, cutDeps(t, f))
			if code != 2 {
				t.Fatalf("code=%d out=%s", code, out.String())
			}
			if !strings.Contains(errs.String(), tc.wants) {
				t.Fatalf("the refusal does not say why: %s", errs.String())
			}
			if len(f.tagged) != 0 {
				t.Fatalf("a refused cut tagged %v", f.tagged)
			}
		})
	}
}

// LESSON 6. AN UNSUPPORTED PAIR REFUSES BEFORE THE FIRST COMPILE, AND LEAVES
// NOTHING BEHIND. The fourth dogfood handed `--platform darwin-arm64,darwin-amd64`
// to a binary that took --platform as one string: it failed at tool 1 of 21
// with the compiler's own `unsupported GOOS/GOARCH pair` and left an empty
// directory of that name in the release tree for somebody to find later.
func TestBuildRefusesAnUnsupportedPairBeforeBuildingAnything(t *testing.T) {
	tc := &fakeToolchain{}
	out1 := t.TempDir()
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0",
		"--out", out1, "--source", buildSource(t), "--platform", "linux-amd64",
		"--platform", "darwin-arm64,darwin-amd64", "--platform", "plan9-vax"},
		&out, &errs, Deps{Toolchain: tc})
	if code != 2 {
		t.Fatalf("an unsupported pair built anyway: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(errs.String(), "plan9-vax") {
		t.Fatalf("the refusal does not name the pair: %s", errs.String())
	}
	if len(tc.calls) != 0 {
		t.Fatalf("a refused build compiled %d packages", len(tc.calls))
	}
	// NOTHING IS LEFT BEHIND: not the bad platform's directory, and not the
	// good ones' either. A half-made release root is a root somebody reasons
	// about.
	entries, err := os.ReadDir(out1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused build left %v in the release root", entries)
	}
}

// EVERY PLATFORM IS BUILT AND EVERY PLATFORM IS NAMED. The repeated form used
// to keep the LAST flag silently: one cheerful receipt line, a release half a
// platform short, and nobody the wiser until a bench asked for a binary that
// was never made.
func TestBuildBuildsEveryPlatformAndNamesEachInTheReceipt(t *testing.T) {
	tc := &fakeToolchain{}
	root := t.TempDir()
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0",
		"--out", root, "--source", buildSource(t),
		"--platform", "darwin-arm64,darwin-amd64", "--platform", "linux-amd64"},
		&out, &errs, Deps{Toolchain: tc})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-amd64"} {
		if !strings.Contains(out.String(), "RELEASE BUILT version=v0.16.0 platform="+platform+" ") {
			t.Errorf("no receipt line for %s:\n%s", platform, out.String())
		}
		if _, err := os.Stat(filepath.Join(root, "v0.16.0", platform, SumsFile)); err != nil {
			t.Errorf("%s has no %s: %v", platform, SumsFile, err)
		}
	}
	// And one line that names them all, so a reader does not have to count.
	if !strings.Contains(out.String(), "RELEASE BUILD OK version=v0.16.0 platforms=darwin-arm64,darwin-amd64,linux-amd64") {
		t.Fatalf("no summary line naming every platform:\n%s", out.String())
	}
}

// LESSON 7. NO TAG, STILL A DIGEST. A dev build has no annotated tag, so
// `adopt --repo` has nothing to read and the fourth dogfood had to compute
// --expect-sums ON THE MACHINE BEING ADOPTED FROM -- which is the one machine
// whose word about its own bits proves nothing. `build` writes the digest of
// the SHA256SUMS it just verified, on the coordinator, beside the artifacts.
func TestBuildWritesTheSumsDigestBesideTheArtifacts(t *testing.T) {
	tc := &fakeToolchain{}
	root := t.TempDir()
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"build", "--version", "v0.16.0",
		"--out", root, "--source", buildSource(t), "--platform", "linux-amd64"},
		&out, &errs, Deps{Toolchain: tc}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	dir := ArtifactDir(root, "v0.16.0", "linux", "amd64")
	want, err := fileSum(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, DigestFile))
	if err != nil {
		t.Fatalf("build wrote no %s: %v", DigestFile, err)
	}
	if strings.TrimSpace(string(body)) != want {
		t.Fatalf("%s says %q, the %s hashes to %s", DigestFile, strings.TrimSpace(string(body)), SumsFile, want)
	}
	for _, line := range []string{"RELEASE BUILT version=v0.16.0 platform=linux-amd64", "RELEASE BUILD OK version=v0.16.0"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("no %q in:\n%s", line, out.String())
		}
	}
	if !strings.Contains(out.String(), "sums="+want) {
		t.Fatalf("no receipt carries the digest %s:\n%s", want, out.String())
	}
	// The digest file is NOT in the checksum file it is the digest of, and a
	// second build over the same directory answers the same digest.
	sums, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sums), DigestFile) {
		t.Fatalf("%s lists %s, so its own digest depends on itself:\n%s", SumsFile, DigestFile, string(sums))
	}
}

// AND ADOPT READS IT FROM THE COORDINATOR'S OWN BUILD OUTPUT. --expect-sums-from
// names a local file; a digest read off the far side would be the far side
// vouching for itself.
func TestAdoptExpectSumsFromReadsTheCoordinatorsDigestFile(t *testing.T) {
	built, version := stagedRelease(t)
	digest, err := fileSum(filepath.Join(built, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	digestPath := filepath.Join(dir, DigestFile)
	if err := os.WriteFile(digestPath, []byte(digest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{
		answer:  map[string]string{"hulk": "nova-update " + version + " linux/amd64 go1.26.5\nRELEASE INSTALLED version=" + version + " tools=3 skipped=0 retired=0"},
		serves:  map[string]string{"build-host": built},
		refuse:  map[string]error{},
		sends:   nil,
		fetches: nil,
	}
	machines := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(machines, []byte("hulk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", version,
		"--machines", machines, "--ssh", "ssh", "--from", "build-host:/home/gaffer/release",
		"--stage", filepath.Join(dir, "stage"), "--expect-sums-from", digestPath,
		"--bin", "~/.local/bin", "--dest", "~/nova-release", "--platform", "linux-amd64"},
		&out, &errs, Deps{SSH: s, Self: func() string { return version }})
	if code != 0 {
		t.Fatalf("code=%d errs=%s out=%s", code, errs.String(), out.String())
	}
	// THE VERB NEVER HASHES A REMOTE FILE. Not `sha256sum`, not `shasum`, not
	// `openssl dgst`: a digest computed where the bits live is not evidence
	// about the bits.
	for _, run := range s.runs {
		for _, banned := range []string{"sha256sum", "shasum", "openssl", "md5"} {
			if strings.Contains(run, banned) {
				t.Fatalf("adopt asked a machine to hash something: %s", run)
			}
		}
	}
}

// A --expect-sums-from that names another machine is refused BY NAME: the
// whole point of the digest is that it did not travel with the bits.
func TestAdoptRefusesADigestFileOnTheFarSide(t *testing.T) {
	dir := t.TempDir()
	machines := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(machines, []byte("hulk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machines, "--ssh", "ssh", "--from", "build-host:/home/gaffer/release",
		"--stage", filepath.Join(dir, "stage"), "--expect-sums-from", "build-host:/home/gaffer/release/SUMS.digest",
		"--bin", "~/.local/bin", "--dest", "~/nova-release", "--platform", "linux-amd64"},
		&out, &errs, Deps{SSH: &fakeSSH{}, Self: func() string { return "v0.16.0" }})
	if code != 2 {
		t.Fatalf("a far-side digest file was accepted: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(errs.String(), "--expect-sums-from") || !strings.Contains(errs.String(), "build-host") {
		t.Fatalf("the refusal does not name the file: %s", errs.String())
	}
}

// LESSON 8. INSTALL ON THE COORDINATOR FIRST. `adopt` fans out the release it
// is holding, and the nova-update that runs the fan-out is the one on THIS
// machine: a Studio a release behind cannot adopt a release it does not
// understand, and the fourth dogfood found that out with --from in hand and no
// way to use it.
func TestAdoptRefusesWhenTheLocalToolPredatesTheRelease(t *testing.T) {
	built, _ := stagedRelease(t)
	dir := t.TempDir()
	machines := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(machines, []byte("hulk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.17.0",
		"--machines", machines, "--ssh", "ssh", "--from", filepath.Dir(filepath.Dir(built)),
		"--bin", "~/.local/bin", "--dest", "~/nova-release", "--platform", "linux-amd64"},
		&out, &errs, Deps{SSH: s, Self: func() string { return "v0.16.0" }})
	if code != 2 {
		t.Fatalf("a stale coordinator adopted anyway: code=%d out=%s", code, out.String())
	}
	for _, want := range []string{"v0.16.0", "v0.17.0", "release install"} {
		if !strings.Contains(errs.String(), want) {
			t.Errorf("the refusal does not carry %q: %s", want, errs.String())
		}
	}
	if len(s.runs) != 0 || len(s.sends) != 0 {
		t.Fatalf("a refused adopt touched a machine: runs=%v sends=%v", s.runs, s.sends)
	}
}

// AND `pull` TAKES IT WITH THE REST. SUMS.digest is the one file `build`
// writes that SHA256SUMS does not list, so a pull deleting only the listed
// names would leave it behind -- and the `rmdir` that follows refuses a
// directory that is not empty, deliberately, which would have turned a file
// this tool wrote into a refusal on every machine in the fleet.
func TestPullDeletesTheDigestFileToo(t *testing.T) {
	built, version := stagedRelease(t)
	root := filepath.Dir(filepath.Dir(built))
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# nova-tools changelog\n\n## "+version+" — 2026-09-18\n\n- #1 x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"pull", "--version", version, "--out", root,
		"--changelog", changelog, "--platform", "linux-amd64", "--reason", "a test"},
		&out, &errs, Deps{}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if _, err := os.Stat(built); !os.IsNotExist(err) {
		left, _ := os.ReadDir(built)
		t.Fatalf("the artifact directory survived the pull, holding %v", left)
	}
}

// buildSource is a --source that looks like a nova-tools checkout: three
// cmd/nova-* directories and nothing else the build reads.
func buildSource(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	for _, tool := range []string{"nova-bus", "nova-update", "nova-work"} {
		if err := os.MkdirAll(filepath.Join(source, "cmd", tool), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

// stagedRelease builds a release with the fake toolchain and answers the
// artifact directory and its version, so an adopt test has real bytes and a
// real checksum file to verify.
func stagedRelease(t *testing.T) (string, string) {
	t.Helper()
	root, version := t.TempDir(), "v0.16.0"
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"build", "--version", version,
		"--out", root, "--source", buildSource(t), "--platform", "linux-amd64"},
		&out, &errs, Deps{Toolchain: &fakeToolchain{}}); code != 0 {
		t.Fatalf("staging a release refused: %s", errs.String())
	}
	return ArtifactDir(root, version, "linux", "amd64"), version
}
