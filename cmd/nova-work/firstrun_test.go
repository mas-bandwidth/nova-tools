package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestHelpExamplesRunThroughTheComparator: the help's `example:` block is a
// first run (docs/ONBOARDING.md point 6), and each of its lines runs, in order,
// against the recorded conversation with GitHub the other tests use
// (internal/workgh/testdata/reliable: one public repository of twenty issues,
// read at fifteen a page), and prints what is written here.
//
// $ORG and $REPO are the reader's. Here they stand for the recorded
// organization and repository, both on the command line and where the tool
// prints them back; ./tree.lisp stands for a file of this test's own; seconds=
// and the tree's sha256 (the tree records the instant of its import) are the
// other values not compared.
func TestHelpExamplesRunThroughTheComparator(t *testing.T) {
	t.Parallel()

	sitting := []onboarding.Step{
		{Line: "$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run", Want: []string{
			"PLAN OK org=$ORG repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15",
			"REPO OK repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2",
			"IMPORT OK org=$ORG out=- repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=10586ae9aa4a3eaa5fbd2b5278461fc61979db7110637f007d8d3a811b91aec6 calls=3 points=3 rest=0 seconds=0.0 dry_run=true",
		}},
		{Line: "$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp", Want: []string{
			"PLAN OK org=$ORG repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15",
			"REPO OK repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2",
			"IMPORT OK org=$ORG out=./tree.lisp repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=10586ae9aa4a3eaa5fbd2b5278461fc61979db7110637f007d8d3a811b91aec6 calls=3 points=3 rest=0 seconds=0.0 dry_run=false",
		}},
		{Line: "$ nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15", Want: []string{
			"VERIFY OK tree=./tree.lisp sha256=10586ae9aa4a3eaa5fbd2b5278461fc61979db7110637f007d8d3a811b91aec6 repos=1 issues=20 comments=74 calls=3 points=3 rest=0 seconds=0.0 differences=0",
		}},
	}
	examples, err := onboarding.ExampleLines(banner, "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range sitting {
		want = append(want, strings.TrimPrefix(s.Line, "$ "))
	}
	if strings.Join(examples, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the banner's example block is not the sitting this test runs\nbanner:\n  %s\nwant:\n  %s", strings.Join(examples, "\n  "), strings.Join(want, "\n  "))
	}

	const org, repo = "mas-bandwidth", "reliable" // the recording's
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	stand := strings.NewReplacer("$ORG", org, "$REPO", repo, "./tree.lisp", tree)
	elide := func(name, pattern, as string) onboarding.Norm {
		n, err := onboarding.Elide(name, pattern, as)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	norms := []onboarding.Norm{
		onboarding.Path("./tree.lisp", tree),
		elide("the recorded organization, written $ORG", org, "$ORG"),
		elide("the recorded repository, written $REPO", `\b`+repo+`\b`, "$REPO"),
		elide("the seconds the run took", `seconds=\S+`, "seconds=-"),
		elide("the tree's hash, which covers the instant it was imported", `sha256=[0-9a-f]{64}`, "sha256=-"),
	}
	for _, s := range sitting {
		args := strings.Fields(stand.Replace(strings.TrimPrefix(s.Line, "$ nova-work ")))
		code, out, errs := do(t, replay(t), args...)
		if code != 0 {
			t.Errorf("the example %s exits %d; stderr: %s", s.Line, code, errs)
		}
		for _, p := range onboarding.Compare(s, onboarding.Result{Code: code, Stdout: out, Stderr: errs}, norms) {
			t.Error(p)
		}
	}
}
