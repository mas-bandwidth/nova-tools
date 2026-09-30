package main

import (
	"fmt"
	"strings"
)

func init() {
	register(verb{
		name:    "attach",
		summary: "check the tag is this tree's, sum the shipped set and attach it to the release draft",
		help: `usage: go run ./tools/ghrelease attach

The release job's last step: checks that the tag is one this run may attach to,
computes SHA256SUMS over dist/ ("ghrelease sums"), and attaches the set to the
release draft ("ghrelease upload"). Exit 0 attached; 1 refused; 2 the
environment is incomplete.

Environment: TAG, GITHUB_REPOSITORY, GITHUB_SHA, GH_TOKEN.

The refusals, in order, each before any API call that changes anything:

  - the tag is not v-prefixed;
  - the release notes file docs/RELEASE-NOTES-<TAG without its leading v>.md is
    missing or empty in the tagged tree;
  - the tag does not exist in the repository. The tag is verified on both paths,
    not only the create one: gh's --verify-tag refuses to invent a tag on create
    and the upload path takes no such flag, and the trigger cannot carry the
    guarantee because the workflow is also dispatched by hand, against a ref that
    may be a branch;
  - the tag is not this tree's tag. Existence is not enough: under a tag push
    the ref that triggered the run is the tag, so the two are one fact, but under
    a dispatch they are not, and a tag can be force-moved after its release was
    published. Attaching a build from an unrelated tree would replace a published
    release's binaries and its SHA256SUMS with assets whose checksum file agrees
    with itself and with nothing anybody tagged. The tag is fetched rather than
    assumed present: checkout is shallow and carries no tags, and a stale local
    tag is overwritten.

Then "ghrelease sums <TAG> dist" refuses a dist/ that is not exactly the shipped
set, and "ghrelease upload" attaches it to the draft or refuses a published
release.
`,
		do: doAttach,
	})
}

func doAttach(e env, args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(e.stderr, "usage: %s attach   (the environment carries the rest; run: go run ./tools/ghrelease help attach)\n", tool)
		return 2
	}
	v, ok := e.requireEnv("attach", "TAG", "GITHUB_REPOSITORY", "GITHUB_SHA", "GH_TOKEN")
	if !ok {
		return 2
	}
	tag, repo, sha := v["TAG"], v["GITHUB_REPOSITORY"], v["GITHUB_SHA"]

	if !strings.HasPrefix(tag, "v") {
		fmt.Fprintf(e.stdout, "refusing: %s is not a v-prefixed tag\n", tag)
		return 1
	}
	if !e.notesPresent(tag) {
		fmt.Fprintf(e.stdout, "refusing: %s does not exist in the tagged tree; write it, land it, and tag the commit that carries it\n", notesFile(tag))
		return 1
	}

	got, ok := e.ask(fmt.Sprintf("repos/%s/git/ref/tags/%s", repo, tag))
	if !ok {
		return 1
	}
	if got.code == "404" {
		fmt.Fprintf(e.stdout, "refusing: %s is not a tag in this repository\n", tag)
		return 1
	}

	git := func(args ...string) (string, int) {
		return e.runner().Output(command{dir: e.dir, name: "git", args: args})
	}
	if out, rc := git("fetch", "--depth=1", "origin", fmt.Sprintf("+refs/tags/%s:refs/tags/%s", tag, tag)); rc != 0 {
		fmt.Fprintln(e.stdout, chomp(out))
		return rc
	}
	out, rc := git("rev-parse", fmt.Sprintf("refs/tags/%s^{commit}", tag))
	if rc != 0 {
		fmt.Fprintln(e.stdout, chomp(out))
		return rc
	}
	if tagged := strings.TrimSpace(out); tagged != sha {
		fmt.Fprintf(e.stdout, "refusing: %s is the tag for %s, but this run built %s\n", tag, tagged, sha)
		fmt.Fprintf(e.stdout, "attaching here would replace %s's published artifacts with a build from another tree\n", tag)
		return 1
	}

	// The sums are computed here, in the step that attaches them, over the bytes
	// on this disk; what upload attaches is that directory and nothing else.
	if rc := doSums(e, []string{tag, "dist"}); rc != 0 {
		return rc
	}
	return uploadRelease(e, repo, tag)
}
