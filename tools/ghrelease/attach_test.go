package main

import (
	"strings"
	"testing"
)

// The attach verb is the release job's last step, which was a run block of
// about ninety lines: the notes-file check, the tag-exists check, the
// tag-is-this-tree check, then the sums and the upload. Each refusal is pinned
// here, and the order: nothing is fetched, summed or uploaded past a refusal.

const attachSHA = "0123456789abcdef0123456789abcdef01234567"

func attachWorld(t *testing.T, tagStatus, taggedSHA string) *harness {
	t.Helper()
	h := newHarness(t)
	h.vars = map[string]string{
		"TAG": "v0.14.0", "GITHUB_REPOSITORY": "example/repo", "GITHUB_SHA": attachSHA, "GH_TOKEN": "a-token-the-fake-never-reads",
	}
	h.tools("nova-bus")
	h.write(uploadNotes, "# 0.14.0 release notes\n", 0o644)
	h.write("dist/nova-bus_v0.14.0_linux_amd64", "bytes", 0o755)
	h.gh.api = func(args []string) (string, int) {
		path := args[len(args)-1]
		switch {
		case strings.Contains(path, "git/ref/tags/"):
			switch tagStatus {
			case "404":
				return httpAnswer("404", "Not Found", ""), 1
			case "200":
				return httpAnswer("200", "OK", `{"ref": "refs/tags/v0.14.0"}`), 0
			}
			return httpAnswer(tagStatus, "Nope", ""), 1
		case strings.Contains(path, "releases/tags/"):
			return httpAnswer("404", "Not Found", ""), 1
		}
		return `[]`, 0
	}
	h.runner.output = func(c command) (string, int) {
		if c.name == "git" && len(c.args) > 0 && c.args[0] == "rev-parse" {
			return taggedSHA + "\n", 0
		}
		return "", 0
	}
	return h
}

func TestAttachFetchesTheTagChecksItIsThisTreesSumsAndUploads(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.wantRC(h.do("attach"), 0)

	calls := h.runner.called()
	if len(calls) != 2 {
		t.Fatalf("%d programs run, want git fetch and git rev-parse: %v", len(calls), calls)
	}
	if got := strings.Join(calls[0].args, " "); calls[0].name != "git" || got != "fetch --depth=1 origin +refs/tags/v0.14.0:refs/tags/v0.14.0" {
		t.Errorf("fetched with %s %s", calls[0].name, got)
	}
	if got := strings.Join(calls[1].args, " "); got != "rev-parse refs/tags/v0.14.0^{commit}" {
		t.Errorf("resolved with git %s", got)
	}
	if got := strings.Join(h.gh.apis[0], " "); got != "-i repos/example/repo/git/ref/tags/v0.14.0" {
		t.Errorf("asked %s", got)
	}
	h.mustContain("SHA256SUMS over 1 artifacts:")
	wantRelease(t, h, "create", "v0.14.0", "--draft", "--verify-tag", "--title", "v0.14.0", "--notes-file", uploadNotes,
		"dist/SHA256SUMS", "dist/nova-bus_v0.14.0_linux_amd64")
}

func TestAttachRefusesATagThatIsNotVPrefixed(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.vars["TAG"] = "0.14.0"
	h.wantRC(h.do("attach"), 1)
	h.mustContain("refusing: 0.14.0 is not a v-prefixed tag")
	if len(h.gh.apis) != 0 || len(h.runner.called()) != 0 {
		t.Fatal("something ran past a refused tag shape")
	}
}

func TestAttachRefusesWhenTheNotesFileIsMissingBeforeAnyAPICall(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.vars["TAG"] = "v7.0.0"
	h.wantRC(h.do("attach"), 1)
	h.mustContain("refusing: docs/RELEASE-NOTES-7.0.0.md does not exist in the tagged tree; write it, land it, and tag the commit that carries it")
	if len(h.gh.apis) != 0 || len(h.runner.called()) != 0 {
		t.Fatal("something ran past a missing notes file")
	}
}

func TestAttachRefusesATagThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "404", attachSHA)
	h.wantRC(h.do("attach"), 1)
	h.mustContain("refusing: v0.14.0 is not a tag in this repository")
	if len(h.runner.called()) != 0 {
		t.Fatal("git ran after the tag was refused")
	}
	wantNoRelease(t, h)
}

func TestAttachRefusesANonAnswerAboutTheTag(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "403", attachSHA)
	h.wantRC(h.do("attach"), 1)
	h.mustContain("asking GitHub about repos/example/repo/git/ref/tags/v0.14.0 answered neither 200 nor 404 (gh exit 1):")
	wantNoRelease(t, h)
}

func TestAttachRefusesATagThatIsAnotherTreesBeforeSummingOrUploading(t *testing.T) {
	t.Parallel()
	// A dispatch on a branch named like a tag passes the shape check, and a real
	// tag of that name would otherwise have its published binaries and checksum
	// file replaced by a build from an unrelated tree.
	other := "fedcba9876543210fedcba9876543210fedcba98"
	h := attachWorld(t, "200", other)
	h.wantRC(h.do("attach"), 1)
	h.mustContain("refusing: v0.14.0 is the tag for " + other + ", but this run built " + attachSHA)
	h.mustContain("attaching here would replace v0.14.0's published artifacts with a build from another tree")
	h.mustNotContain("SHA256SUMS over")
	wantNoRelease(t, h)
}

func TestAttachPassesAFailedFetchOrResolveOn(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.runner.output = func(c command) (string, int) { return "fatal: could not read from remote\n", 128 }
	h.wantRC(h.do("attach"), 128)
	h.mustContain("fatal: could not read from remote")
	wantNoRelease(t, h)
}

func TestAttachStopsAtSumsWhenDistIsNotTheShippedSet(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.write("dist/stray", "x", 0o644)
	h.wantRC(h.do("attach"), 1)
	h.mustContain("is not the shipped set")
	wantNoRelease(t, h)
}

func TestAttachAPublishedReleaseIsRefusedAfterTheSums(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.gh.api = func(args []string) (string, int) {
		path := args[len(args)-1]
		if strings.Contains(path, "git/ref/tags/") {
			return httpAnswer("200", "OK", "{}"), 0
		}
		return httpAnswer("200", "OK", `{"id": 55, "draft": false}`), 0
	}
	h.wantRC(h.do("attach"), 1)
	h.mustContain("v0.14.0 already has a published release 55")
	wantNoRelease(t, h)
}
