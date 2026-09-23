package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// Schema's go tests resolve serialize runtimes as siblings of the checkout
// (../serialize.go from the job clone; ../../serialize.go from a nested package,
// as ci-full.yml clones them). nova-merge rebuilds --root/<name> every run, so a
// watcher symlink beside repo/ vanishes. --sibling <name>=<url>@<ref> stages those
// clones beside repo/ as part of the rebuild (nova-tools #2499 item 2 / #2508).
//
// Nothing here clones the real serialize.go repository. The fixture is a tiny
// local bare git repo reached over file://.

const siblingStatTest = `package sibling

import (
	"os"
	"testing"
)

func TestSerializeSibling(t *testing.T) {
	if _, err := os.Stat("../serialize.go"); err != nil {
		t.Fatal(err)
	}
}
`

// addSiblingNeedPR lands a member whose go test stats ../serialize.go from the
// job clone — the shape schema's tests use against a sibling runtime.
func addSiblingNeedPR(t *testing.T, l *lab, n int) string {
	t.Helper()
	l.git(l.work, "checkout", "-q", "-B", "pr-sibling", "dev")
	l.write("sibling_test.go", siblingStatTest)
	sha := l.commit("need serialize.go sibling")
	l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	l.git(l.work, "checkout", "-q", "main")
	l.heads[n] = sha
	l.host.PRs[n] = merge.PR{Number: n, HeadOID: sha, Base: "dev"}
	l.host.SetCheckRuns(sha, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: sha})
	return sha
}

// siblingRemote builds a tiny bare repository with one commit at ref, and
// returns the file:// URL git clones it from. The working tree holds a marker
// so a test can say the clone is this fixture, not an empty directory.
func siblingRemote(t *testing.T, l *lab, ref string) string {
	t.Helper()
	src := filepath.Join(l.dir, "sibling-src")
	remote := filepath.Join(l.dir, "sibling.git")
	l.git(l.dir, "init", "-q", "-b", "main", src)
	l.git(l.dir, "init", "-q", "--bare", "-b", "main", remote)
	for _, at := range []string{src, remote} {
		for _, kv := range quietRepoSettings() {
			l.git(at, "config", kv[0], kv[1])
		}
	}
	if err := os.WriteFile(filepath.Join(src, "SIBLING"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.git(src, "add", "-A")
	l.git(src, "-c", "user.name=fixture", "-c", "user.email=fixture@localhost", "commit", "-q", "-m", "sibling")
	l.git(src, "tag", ref)
	l.git(src, "push", "-q", fileURL(remote), "HEAD:refs/heads/main")
	l.git(src, "push", "-q", fileURL(remote), ref)
	return fileURL(remote)
}

// THE RED: without --sibling, a tree that stats ../serialize.go from the job
// clone fails at go test. That is the schema landing hole: the gate rebuilt
// --root and the sibling was not there.
func TestBatchWithoutSiblingTheTreeCannotStatIt(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	addSiblingNeedPR(t, l, 4)
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-nosib", "--pr", "4",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 1 {
		t.Fatalf("a tree that stats a missing sibling is BATCH FAIL, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL")
	contains(t, stdout, "step=test")
	contains(t, stdout, "tests=TestSerializeSibling")
	absent(t, stdout, "BATCH OK")
	if _, err := os.Stat(filepath.Join(root, "integration-nosib", "serialize.go")); err == nil {
		t.Error("without --sibling the checkout must not grow a serialize.go neighbour")
	}
}

// THE GREEN: the same tree, with --sibling serialize.go=<file:// fixture>@<ref>,
// finds the sibling beside repo/ and the test that stats ../serialize.go passes.
func TestBatchSiblingStagesBesideTheClone(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	addSiblingNeedPR(t, l, 4)
	url := siblingRemote(t, l, "v1.0.0")
	root := filepath.Join(l.dir, "batch")
	spec := "serialize.go=" + url + "@v1.0.0"

	exit, stdout, stderr := l.run("batch", "--name", "integration-sib", "--pr", "4",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--sibling", spec)

	if exit != 0 {
		t.Fatalf("a staged sibling is a green batch, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH OK")
	contains(t, stderr, "BATCH SIBLING name=serialize.go ref=v1.0.0")
	sib := filepath.Join(root, "integration-sib", "serialize.go")
	if _, err := os.Stat(sib); err != nil {
		t.Fatalf("the sibling must sit beside repo/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sib, "SIBLING")); err != nil {
		t.Fatalf("the sibling clone is not the fixture: %v", err)
	}
	clone := filepath.Join(root, "integration-sib", "repo")
	if _, err := os.Stat(filepath.Join(clone, "..", "serialize.go")); err != nil {
		t.Fatalf("from the job clone, ../serialize.go must exist: %v", err)
	}
}

// Two --sibling flags stage two checkouts, in the order given.
func TestBatchSiblingIsRepeatable(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	url := siblingRemote(t, l, "v1.0.0")
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-twosib", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--sibling", "serialize.go="+url+"@v1.0.0",
		"--sibling", "serialize.c="+url+"@v1.0.0")

	if exit != 0 {
		t.Fatalf("two staged siblings are a green batch, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH SIBLING name=serialize.go ref=v1.0.0")
	contains(t, stderr, "BATCH SIBLING name=serialize.c ref=v1.0.0")
	work := filepath.Join(root, "integration-twosib")
	for _, name := range []string{"serialize.go", "serialize.c"} {
		if _, err := os.Stat(filepath.Join(work, name, "SIBLING")); err != nil {
			t.Errorf("sibling %s is missing: %v", name, err)
		}
	}
}

func TestBatchRefusesABadSiblingFlag(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	url := siblingRemote(t, l, "v1.0.0")

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--sibling", "serialize.go"}, "--sibling wants <name>=<url>@<ref>"},
		{[]string{"--sibling", "serialize.go=" + url}, "--sibling wants <name>=<url>@<ref>"},
		{[]string{"--sibling", "serialize.go=@v1.0.0"}, "--sibling wants <name>=<url>@<ref>"},
		{[]string{"--sibling", "../escape=" + url + "@v1.0.0"}, "--sibling name is one path element"},
		{[]string{"--sibling", "repo=" + url + "@v1.0.0"}, `reserved for the batch's own checkout`},
		{[]string{"--sibling", "tmp=" + url + "@v1.0.0"}, `reserved for the batch's own checkout`},
		{[]string{"--sibling", "serialize.go=" + url + "@v1.0.0", "--sibling", "serialize.go=" + url + "@v1.0.0"}, "named more than once"},
	}
	for _, c := range cases {
		args := append([]string{"batch", "--name", "integration-badsib", "--pr", "1",
			"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m"}, c.args...)
		exit, stdout, stderr := l.run(args...)
		if exit != 2 {
			t.Errorf("%v: a bad --sibling is exit 2, got %d\nstderr: %s", c.args, exit, stderr)
		}
		contains(t, stderr, c.want)
		absent(t, stdout, "BATCH OK")
		absent(t, stderr, "BATCH MERGED")
	}
}

func TestBatchRefusesASiblingItCannotClone(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	missing := fileURL(filepath.Join(l.dir, "no-such-sibling.git"))

	exit, stdout, stderr := l.run("batch", "--name", "integration-missib", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--sibling", "serialize.go="+missing+"@v1.0.0")

	if exit != 2 {
		t.Fatalf("a sibling that cannot be cloned is BATCH REFUSED, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "serialize.go")
	absent(t, stdout, "BATCH OK")
	absent(t, stderr, "BATCH MERGED")
}

func TestParseSibling(t *testing.T) {
	t.Parallel()
	ok, err := parseSibling("serialize.go=file:///path/to/s.git@v1.16.2")
	if err != nil {
		t.Fatalf("a well-formed spec is accepted, got %v", err)
	}
	if ok.Name != "serialize.go" || ok.URL != "file:///path/to/s.git" || ok.Ref != "v1.16.2" {
		t.Errorf("got %+v", ok)
	}
	// The last @ splits url from ref, so an ssh URL keeps its user@host.
	ssh, err := parseSibling("serialize.go=git@example.test:mas-bandwidth/serialize.go.git@v1.16.2")
	if err != nil {
		t.Fatalf("an ssh URL is accepted, got %v", err)
	}
	if ssh.URL != "git@example.test:mas-bandwidth/serialize.go.git" || ssh.Ref != "v1.16.2" {
		t.Errorf("ssh parse got %+v", ssh)
	}
	if _, err := parseSibling("serialize.go=git@example.test:mas-bandwidth/serialize.go.git"); err == nil {
		t.Error("an ssh URL with no @ref must be refused")
	}
	if _, err := parseSibling("serialize.go=file:///path/to/s.git"); err == nil {
		t.Error("a spec with no @ref must be refused")
	}
	if !safepath.NameOK("serialize.go") {
		t.Error("serialize.go is the name schema clones; NameOK must admit it")
	}
}
