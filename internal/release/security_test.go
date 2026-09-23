package release

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Johnny's decisions on SPEC-RELEASE (#1337), one test per decision.
//
// 1. A cut whose range touched the sensitive paths refuses without his read.
// 2. The tag is an ANNOTATED tag and the annotation carries the SUMS digest.
// 3. A leaked release can be PULLED: the artifacts go, the tag stays, and the
//    changelog says so.
// ---------------------------------------------------------------------------

// sensitiveForge is cutForge with a range that touched two of the listed paths
// and three that are not on any list, so the classification is asserted to pick
// the two rather than to notice that something changed.
func sensitiveForge() *fakeForge {
	f := cutForge()
	f.files = map[string][]string{
		"v0.15.10...abc123abc123def": {
			"internal/release/cut.go",
			"internal/secrets/seal.go",
			"docs/SPEC-UPDATE.md",
			"cmd/nova-secrets/main.go",
			"README.md",
		},
	}
	return f
}

// THE GATE. A range that touched the secrets, sandbox, image or coordination
// paths is not cut on one person's judgement at the keyboard: it is cut after
// Johnny has read it, and the read is NAMED on the command line so the receipt
// carries who vouched for it.
func TestCutRefusesASensitiveRangeWithoutJohnnysRead(t *testing.T) {
	f := sensitiveForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("a sensitive range was cut with nobody's read: code=%d out=%s", code, out.String())
	}
	// The refusal NAMES the paths. "something sensitive changed" sends a
	// person back to the compare view to work out what.
	for _, want := range []string{"internal/secrets/seal.go", "cmd/nova-secrets/main.go", "--security-read"} {
		if !strings.Contains(errs.String(), want) {
			t.Errorf("the refusal does not carry %q: %s", want, errs.String())
		}
	}
	// And it names nothing that is not on the list.
	if strings.Contains(errs.String(), "README.md") {
		t.Errorf("the refusal names a path that is not sensitive: %s", errs.String())
	}
	if len(f.tagged) != 0 {
		t.Fatalf("a refused cut tagged %v", f.tagged)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused cut wrote %s", path)
	}
}

// With the read named, the cut goes through and SAYS SO on its own line: a
// release that crossed the sensitive list is a fact somebody reads off the
// terminal and out of a log months later.
func TestCutWithJohnnysReadSaysSoOnItsOwnLine(t *testing.T) {
	f := sensitiveForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path,
		"--security-read", "https://forge.test/mas-bandwidth/nova-tools/pull/1337#issuecomment-99"}, &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	want := "RELEASE CUT SENSITIVE paths=2 read=https://forge.test/mas-bandwidth/nova-tools/pull/1337#issuecomment-99"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("no sensitive line %q in:\n%s", want, out.String())
	}
	if len(f.tagged) != 1 {
		t.Fatalf("tagged %v", f.tagged)
	}
}

// A range that touched nothing on the list says nothing: the line exists to
// mark the exception, and a line printed every time is a line nobody reads.
func TestCutOfAnOrdinaryRangeSaysNothingAboutSensitivePaths(t *testing.T) {
	f := cutForge()
	f.files = map[string][]string{"v0.15.10...abc123abc123def": {"docs/SPEC-UPDATE.md", "internal/release/cut.go"}}
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f)); code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if strings.Contains(out.String(), "SENSITIVE") {
		t.Fatalf("an ordinary cut claimed a sensitive range: %s", out.String())
	}
}

// The classification is by PREFIX and by nothing else: no guessing from a file
// name, no substring anywhere in the path. `internal/secretsanta/` is not
// `internal/secrets/`, and a tool called `nova-secrets-viewer` under cmd/ is
// its own directory, not this one.
func TestSensitiveClassifiesByPrefixAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		path string
		hit  bool
	}{
		{"internal/secrets/seal.go", true},
		{"internal/secrets/deep/down/x.go", true},
		{"cmd/nova-secrets/main.go", true},
		{"internal/sandbox/run.go", true},
		{"cmd/nova-sandbox/main.go", true},
		{"infra/image/Dockerfile", true},
		{"scripts/coordination/reap.sh", true},
		{"internal/secretsanta/x.go", false},
		{"cmd/nova-secrets-viewer/main.go", false},
		{"docs/SPEC-SECRETS.md", false},
		{"internal/release/cut.go", false},
		{"a/internal/secrets/x.go", false},
		{"infra/images/x", false},
	} {
		got := Sensitive([]string{tc.path})
		if (len(got) == 1) != tc.hit {
			t.Errorf("Sensitive(%q) = %v, want hit=%v", tc.path, got, tc.hit)
		}
	}
	// Deduplicated and sorted, so the refusal reads the same twice.
	got := Sensitive([]string{"internal/sandbox/b.go", "internal/secrets/a.go", "internal/sandbox/b.go"})
	if fmt.Sprint(got) != "[internal/sandbox/b.go internal/secrets/a.go]" {
		t.Fatalf("got %v", got)
	}
}

// A FILE LIST AT THE CEILING IS A LIST THAT MAY BE SHORT. The forge names at
// most CompareFileCap files for one compare, so a range at that number cannot
// be classified at all -- and a gate that reads a truncated list is a gate that
// passes the one file it did not see.
//
// THE REMEDY CHANGED after the fourth release dogfood (2026-09-18). It used to
// be Johnny's read -- but his read is a read OF A LIST, and the list is the
// thing that may be short, so a read got past the truncation while vouching for
// a prefix of the truth. It is now a complete list from a checkout, which is
// what --local-diff and --paths-from produce. See the lessons in
// docs/SPEC-RELEASE.md and TestCutNamesTheTruncationBeforeTheHitsItFoundInIt.
func TestCutRefusesARangeTooBigToClassify(t *testing.T) {
	f := cutForge()
	var many []string
	for i := 0; i < CompareFileCap; i++ {
		many = append(many, fmt.Sprintf("docs/ordinary-%d.md", i))
	}
	f.files = map[string][]string{"v0.15.10...abc123abc123def": many}
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", path}, &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("a range too big to classify was cut anyway: code=%d", code)
	}
	if !strings.Contains(errs.String(), "reason=compare-truncated") || !strings.Contains(errs.String(), "files="+fmt.Sprint(CompareFileCap)) {
		t.Fatalf("the refusal does not say why it cannot classify: %s", errs.String())
	}
	if !strings.Contains(errs.String(), "--paths-from") {
		t.Fatalf("the refusal does not carry the remedy: %s", errs.String())
	}
	if len(f.tagged) != 0 {
		t.Fatalf("a refused cut tagged %v", f.tagged)
	}
}

// The read id travels into a one-line receipt, so it is held to the field law
// before anything is tagged: whitespace would split the line and `=` is what
// internal/oneline reads as a separator.
func TestCutRefusesASecurityReadNoReceiptCouldCarry(t *testing.T) {
	for _, bad := range []string{"", "   ", "note 1234", "read=1234", "nota\tb", "-leading-dash"} {
		if err := ValidSecurityRead(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{
		"johnny-4b9200ddc994",
		"https://forge.test/mas-bandwidth/nova-tools/pull/1337#issuecomment-3014",
		"note:1337",
	} {
		if err := ValidSecurityRead(good); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
	// And the flag is validated even when the range is ordinary: a read that
	// could not be printed is a read nobody could look up.
	f := cutForge()
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v0.16.0",
		"--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md"), "--security-read", "read=1"}, &out, &errs, cutDeps(t, f)); code != 2 {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
}

// ---------------------------------------------------------------------------
// Decision 2: the tag is annotated and the annotation carries the digest.
// ---------------------------------------------------------------------------

// THE TAG CARRIES THE DIGEST. Before this, `cut` POSTed git/refs -- a
// LIGHTWEIGHT tag, which is a name pointing at a commit and nothing else, so
// the only place the digest lived was the changelog, and `adopt` had to be
// handed it by a person retyping it.
func TestTheTagIsAnnotatedAndCarriesTheSumsDigest(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	out := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	sums := filepath.Join(ArtifactDir(out, "v0.16.0", goos, goarch), SumsFile)
	digest, err := fileSum(sums)
	if err != nil {
		t.Fatal(err)
	}
	f := cutForge()
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v0.16.0",
		"--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md"), "--sums", sums}, &o, &e, cutDeps(t, f)); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	message := f.messages["v0.16.0"]
	if message == "" {
		t.Fatalf("the tag carries no annotation at all: %v", f.messages)
	}
	if !strings.Contains(message, "v0.16.0") || !strings.Contains(message, "abc123abc123def") {
		t.Errorf("the annotation does not say what it is: %q", message)
	}
	if got := SumsInAnnotation(message); got != digest {
		t.Fatalf("the annotation carries sums=%q, want %q (message %q)", got, digest, message)
	}
}

// The two halves of an annotated tag are two calls, in one order: the OBJECT
// first, then the ref that points at it. A ref created first would point at the
// commit, which is the lightweight tag this replaces.
func TestTheAnnotatedTagIsTheObjectThenTheRef(t *testing.T) {
	object := tagObjectArgs("o/n", "v0.16.0", "abc123", "v0.16.0\n\nsums=deadbeef\n")
	joined := strings.Join(object, " ")
	for _, want := range []string{"--method POST", "repos/o/n/git/tags", "tag=v0.16.0", "object=abc123", "type=commit", "message=v0.16.0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the tag-object call does not carry %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "git/refs") {
		t.Errorf("the tag-object call is a ref call: %s", joined)
	}
	ref := strings.Join(tagRefArgs("o/n", "v0.16.0", "objectsha"), " ")
	for _, want := range []string{"--method POST", "repos/o/n/git/refs", "ref=refs/tags/v0.16.0", "sha=objectsha"} {
		if !strings.Contains(ref, want) {
			t.Errorf("the ref call does not carry %q: %s", want, ref)
		}
	}
	// THE REF POINTS AT THE TAG OBJECT, never at the commit: a ref pointing at
	// the commit is the lightweight tag, with the annotation orphaned.
	if strings.Contains(ref, "sha=abc123") {
		t.Errorf("the ref points at the commit rather than at the tag object: %s", ref)
	}
}

// A digest is only a digest on its own line: a sha mentioned in prose inside a
// release note must not be read as the one the release was cut with.
func TestSumsInAnnotationReadsOnlyItsOwnLine(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	if got := SumsInAnnotation("v0.16.0\n\nCut from abc.\nsums=" + digest + "\n"); got != digest {
		t.Errorf("got %q", got)
	}
	for _, message := range []string{
		"v0.16.0\n\nnothing here\n",
		"v0.16.0\n\nsee sums=" + digest + " below\n",
		"v0.16.0\n\nsums=nothex\n",
	} {
		if got := SumsInAnnotation(message); got != "" {
			t.Errorf("%q gave %q", message, got)
		}
	}
}

// ADOPT READS THE DIGEST OFF THE TAG. The digest reaching the adopting host
// through the tag OBJECT is the whole point of decision 2: it travelled by git,
// not beside the bits, and nobody has to transcribe it.
func TestAdoptReadsTheDigestFromTheTagObject(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	served := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	digest, err := fileSum(filepath.Join(served, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{messages: map[string]string{"v0.16.0": Annotation("v0.16.0", "abc123", digest)}}
	s := &fakeSSH{
		serves: map[string]string{"hulk": served},
		answer: map[string]string{"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--repo", "o/n",
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s, Forge: f})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "RELEASE ADOPTED machine=vision version=v0.16.0") {
		t.Fatalf("no receipt:\n%s", o.String())
	}
}

// And when the two disagree, BOTH are named, along with where the expected one
// came from: which of the two is wrong is the whole question, and "the digest
// does not match" cannot answer it.
func TestAdoptRefusesWhenTheTagDigestAndTheBitsDisagree(t *testing.T) {
	goos, goarch := platformOf(t, "linux-amd64")
	onHulk := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	served := ArtifactDir(onHulk, "v0.16.0", goos, goarch)
	cut := strings.Repeat("cd", 32)
	f := &fakeForge{messages: map[string]string{"v0.16.0": Annotation("v0.16.0", "abc123", cut)}}
	s := &fakeSSH{serves: map[string]string{"hulk": served}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--repo", "o/n",
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s, Forge: f})
	if code != 2 {
		t.Fatalf("a release the tag disowns was adopted: code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), cut) || !strings.Contains(e.String(), "tag") {
		t.Fatalf("the refusal does not name the tag's digest: %s", e.String())
	}
	if len(s.sends) != 0 {
		t.Fatalf("it was pushed anyway: %v", s.sends)
	}
}

// A tag with no annotation -- a lightweight ref, which is every tag this tool
// created before today -- is said plainly, with the flag that gets past it.
func TestAdoptSaysSoWhenTheTagCarriesNoDigest(t *testing.T) {
	f := &fakeForge{messages: map[string]string{"v0.16.0": "v0.16.0\n\nCut from abc123.\n"}}
	s := &fakeSSH{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt", "--no-certify", "--version", "v0.16.0",
		"--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--from", "hulk:/releases", "--stage", t.TempDir(), "--repo", "o/n",
		"--bin", "~/.local/bin", "--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s, Forge: f})
	if code != 2 {
		t.Fatalf("code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), "--expect-sums") || !strings.Contains(e.String(), "v0.16.0") {
		t.Fatalf("the refusal does not say what to do: %s", e.String())
	}
	if len(s.fetches) != 0 {
		t.Fatalf("a release was fetched with nothing to check it against: %v", s.fetches)
	}
}

// ---------------------------------------------------------------------------
// Decision 3, case H: a leaked release is pulled.
// ---------------------------------------------------------------------------

// pulled lays down a release here and says that two machines hold it, which is
// the state a pull starts from.
func pulled(t *testing.T, version string) (out string, s *fakeSSH, changelog string) {
	t.Helper()
	out = built(t, version, "linux-amd64", "nova-bus", "nova-update")
	goos, goarch := platformOf(t, "linux-amd64")
	body, err := os.ReadFile(filepath.Join(ArtifactDir(out, version, goos, goarch), SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	s = &fakeSSH{remoteSums: map[string]string{"vision": string(body), "mini": string(body)}}
	changelog = filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# nova-tools changelog\n\n## "+version+" — 2026-09-18\n\n- #1 the leak\n\n## v0.15.10 — 2026-09-17\n\n- #0 older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return out, s, changelog
}

// H. A release is found to have shipped something it should not have. The
// artifacts go -- here and on every machine that holds them -- and the TAG
// STAYS, marked in the changelog as pulled: a tag that vanishes is a history
// that cannot be read, and this estate has never force-moved or deleted one.
func TestPullDeletesTheArtifactsHereAndOnEveryMachine(t *testing.T) {
	out, s, changelog := pulled(t, "v0.16.0")
	goos, goarch := platformOf(t, "linux-amd64")
	local := ArtifactDir(out, "v0.16.0", goos, goarch)
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"pull", "--version", "v0.16.0", "--out", out,
		"--changelog", changelog, "--machines", machinesFile(t, "vision\nmini\n"),
		"--ssh", "/usr/bin/ssh", "--dest", "~/nova-bench/build",
		"--reason", "it shipped a sealed key", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	// Gone from here, checksum file and all, so nothing reads this root as a
	// release any more.
	for _, name := range []string{"nova-bus", "nova-update", SumsFile} {
		if _, err := os.Stat(filepath.Join(local, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still here: %v", name, err)
		}
	}
	if found := VersionsUnder(out, goos, goarch); len(found) != 0 {
		t.Errorf("%s still holds %v", out, found)
	}
	// And gone from each machine, BY NAME: a pull that ran `rm -rf` on a
	// directory composed from a flag is one typo away from the fleet.
	for _, machine := range []string{"vision", "mini"} {
		var rm string
		for _, run := range s.runs {
			if strings.HasPrefix(run, machine+": rm ") {
				rm = run
			}
		}
		if rm == "" {
			t.Fatalf("%s was never asked to delete anything: %v", machine, s.runs)
		}
		for _, want := range []string{"~/nova-bench/build/v0.16.0/linux-amd64/nova-bus", "~/nova-bench/build/v0.16.0/linux-amd64/" + SumsFile} {
			if !strings.Contains(rm, want) {
				t.Errorf("%s was not asked to delete %s: %s", machine, want, rm)
			}
		}
		// NAMED FILES, NEVER A TREE. `rm -rf <a path composed from a flag>` is
		// the one command this verb must not be able to become.
		if !strings.HasPrefix(rm, machine+": rm -f ") || strings.Contains(rm, " -r ") || strings.Contains(rm, "-rf") {
			t.Errorf("the pull went recursive on %s: %s", machine, rm)
		}
		if !strings.Contains(o.String(), "RELEASE PULLED machine="+machine+" version=v0.16.0") {
			t.Errorf("no receipt for %s:\n%s", machine, o.String())
		}
	}
	if !strings.Contains(o.String(), "RELEASE PULL OK") {
		t.Errorf("no summary line:\n%s", o.String())
	}
	// The changelog says it was pulled, and why, and the section is still
	// there: the record of the release is the thing being kept.
	body, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"## v0.16.0 — 2026-09-18", PulledPrefix, "it shipped a sealed key", "## v0.15.10 — 2026-09-17"} {
		if !strings.Contains(text, want) {
			t.Errorf("the changelog does not carry %q:\n%s", want, text)
		}
	}
	if strings.Index(text, PulledPrefix) < strings.Index(text, "## v0.16.0") {
		t.Errorf("the pulled note is not under its own section:\n%s", text)
	}
}

// A pull deletes THE RELEASE'S OWN FILES and nothing else: the names come from
// the checksum file, exactly as `--retire` takes its names from the set it
// installed. Anything else sitting in that directory is somebody's, not this
// verb's.
func TestPullDeletesOnlyWhatTheChecksumFileNames(t *testing.T) {
	out, s, changelog := pulled(t, "v0.16.0")
	goos, goarch := platformOf(t, "linux-amd64")
	local := ArtifactDir(out, "v0.16.0", goos, goarch)
	stray := filepath.Join(local, "notes.txt")
	if err := os.WriteFile(stray, []byte("somebody's"), 0o644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"pull", "--version", "v0.16.0", "--out", out,
		"--changelog", changelog, "--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("the pull deleted a file that was not the release's: %v", err)
	}
	for _, run := range s.runs {
		if strings.Contains(run, "notes.txt") {
			t.Fatalf("a machine was asked to delete a file that is not in the release: %s", run)
		}
	}
}

// --dry-run answers the question without doing any of it, the same way
// `adopt --dry-run` does: nothing deleted here, nothing deleted there, and the
// changelog untouched.
func TestPullDryRunDeletesNothing(t *testing.T) {
	out, s, changelog := pulled(t, "v0.16.0")
	goos, goarch := platformOf(t, "linux-amd64")
	local := ArtifactDir(out, "v0.16.0", goos, goarch)
	before, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"pull", "--version", "v0.16.0", "--out", out,
		"--changelog", changelog, "--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--dest", "~/build", "--dry-run", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if _, err := os.Stat(filepath.Join(local, SumsFile)); err != nil {
		t.Fatalf("--dry-run deleted the release: %v", err)
	}
	after, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("--dry-run wrote the changelog:\n%s", after)
	}
	for _, run := range s.runs {
		if strings.HasPrefix(strings.SplitN(run, ": ", 2)[1], "rm ") {
			t.Fatalf("--dry-run deleted something on a machine: %s", run)
		}
	}
	if !strings.Contains(o.String(), "RELEASE WOULD PULL machine=vision") {
		t.Fatalf("--dry-run says nothing about what it would do:\n%s", o.String())
	}
}

// A machine's --dest is interpolated into a command the far side's shell
// parses, so it is held to ValidRemotePath BEFORE any command is composed --
// the same rule adopt has, on the verb that deletes rather than the one that
// installs.
func TestPullRefusesAPathTheRemoteShellWouldReadAsSyntax(t *testing.T) {
	out, s, changelog := pulled(t, "v0.16.0")
	for _, hostile := range []string{"/tmp/x; rm -rf /", "~/build/../../etc", "relative/build", "/tmp/$(id)"} {
		var o, e bytes.Buffer
		code := Run("nova-update", []string{"pull", "--version", "v0.16.0", "--out", out,
			"--changelog", changelog, "--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
			"--dest", hostile, "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
		if code != 2 {
			t.Errorf("--dest %q was accepted: code=%d", hostile, code)
		}
		if len(s.runs) != 0 {
			t.Fatalf("--dest %q reached a machine: %v", hostile, s.runs)
		}
	}
}

// The mark is idempotent and refuses a version the changelog never had: a pull
// run twice must not stack two notes, and a pull of a version this changelog
// does not describe is a pull somebody aimed at the wrong file.
func TestMarkPulledIsIdempotentAndRefusesAnUnknownVersion(t *testing.T) {
	text := "# nova-tools changelog\n\n## v0.16.0 — 2026-09-18\n\n- #1 the leak\n"
	note := PulledNote(at(t), "it shipped a sealed key")
	once, err := MarkPulled(text, "v0.16.0", note)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(once, note) {
		t.Fatalf("the note is not there:\n%s", once)
	}
	twice, err := MarkPulled(once, "v0.16.0", note)
	if err != nil {
		t.Fatal(err)
	}
	if twice != once {
		t.Fatalf("a second pull stacked a second note:\n%s", twice)
	}
	if _, err := MarkPulled(text, "v0.99.0", note); err == nil {
		t.Fatal("a version the changelog never had was marked")
	}
	// A heading for another version is not this one's: v0.16.0 must not match
	// v0.16.0-rc1's section, nor the other way round.
	if _, err := MarkPulled("# c\n\n## v0.16.0-rc1 — 2026-09-18\n\n- #1 x\n", "v0.16.0", note); err == nil {
		t.Fatal("v0.16.0 was marked on v0.16.0-rc1's section")
	}
}

// A pull whose --out does not hold the release cannot know what the release's
// files are called, and refuses rather than guessing at a directory listing:
// the names are the release's own, read from the checksum file it was built
// with.
func TestPullRefusesWhenItCannotNameTheFiles(t *testing.T) {
	_, s, changelog := pulled(t, "v0.16.0")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"pull", "--version", "v0.16.0", "--out", t.TempDir(),
		"--changelog", changelog, "--machines", machinesFile(t, "vision\n"), "--ssh", "/usr/bin/ssh",
		"--dest", "~/build", "--platform", "linux-amd64"}, &o, &e, Deps{SSH: s})
	if code != 2 {
		t.Fatalf("code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), SumsFile) {
		t.Fatalf("the refusal does not say what it could not read: %s", e.String())
	}
	if len(s.runs) != 0 {
		t.Fatalf("it reached a machine anyway: %v", s.runs)
	}
}
