package main

import (
	"io"
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
	h.runner.stream = func(c command, stdout, stderr io.Writer) int {
		if c.name == "git" && len(c.args) > 0 && c.args[0] == "rev-parse" {
			io.WriteString(stdout, taggedSHA+"\n")
		}
		return 0
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
	h.wantRun(1, "refusing: 0.14.0 is not a v-prefixed tag", "attach")
	h.wantNothingPast("a refused tag shape")
}

func TestAttachRefusesWhenTheNotesFileIsMissingBeforeAnyAPICall(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.vars["TAG"] = "v7.0.0"
	h.wantRun(1, "refusing: docs/RELEASE-NOTES-7.0.0.md does not exist in the tagged tree; write it, land it, and tag the commit that carries it", "attach")
	h.wantNothingPast("a missing notes file")
}

func TestAttachRefusesATagThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "404", attachSHA)
	h.wantRun(1, "refusing: v0.14.0 is not a tag in this repository", "attach")
	if len(h.runner.called()) != 0 {
		t.Fatal("git ran after the tag was refused")
	}
	wantNoRelease(t, h)
}

func TestAttachRefusesANonAnswerAboutTheTag(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "403", attachSHA)
	h.wantRun(1, "asking GitHub about repos/example/repo/git/ref/tags/v0.14.0 answered neither 200 nor 404 (gh exit 1):", "attach")
	wantNoRelease(t, h)
}

func TestAttachRefusesATagThatIsAnotherTreesBeforeSummingOrUploading(t *testing.T) {
	t.Parallel()
	// A dispatch on a branch named like a tag passes the shape check, and a real
	// tag of that name would otherwise have its published binaries and checksum
	// file replaced by a build from an unrelated tree.
	other := "fedcba9876543210fedcba9876543210fedcba98"
	h := attachWorld(t, "200", other)
	h.wantRun(1, "refusing: v0.14.0 is the tag for "+other+", but this run built "+attachSHA, "attach")
	h.mustContain("attaching here would replace v0.14.0's published artifacts with a build from another tree")
	h.mustNotContain("SHA256SUMS over")
	wantNoRelease(t, h)
}

func TestAttachPassesAFailedFetchOn(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.runner.output = func(c command) (string, int) { return "fatal: could not read from remote\n", 128 }
	h.wantRun(128, "fatal: could not read from remote", "attach")
	wantNoRelease(t, h)
}

func TestAttachPassesAFailedResolveOn(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.runner.stream = func(c command, stdout, stderr io.Writer) int {
		io.WriteString(stderr, "fatal: ambiguous argument\n")
		return 128
	}
	h.wantRun(128, "fatal: ambiguous argument", "attach")
	h.mustNotContain("SHA256SUMS over")
	wantNoRelease(t, h)
}

// A warning git prints on stderr while it resolves the tag is the log's, never
// part of the sha compared with the one this run built.
func TestAttachResolvesTheTagFromStdoutAlone(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.runner.stream = func(c command, stdout, stderr io.Writer) int {
		io.WriteString(stderr, "warning: refname 'v0.14.0' is ambiguous.\n")
		io.WriteString(stdout, attachSHA+"\n")
		return 0
	}
	h.wantRun(0, "SHA256SUMS over 1 artifacts:", "attach")
	h.mustNotContain("is the tag for")
}

func TestAttachStopsAtSumsWhenDistIsNotTheShippedSet(t *testing.T) {
	t.Parallel()
	h := attachWorld(t, "200", attachSHA)
	h.write("dist/stray", "x", 0o644)
	h.wantRun(1, "is not the shipped set", "attach")
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
	h.wantRun(1, "v0.14.0 already has a published release 55", "attach")
	wantNoRelease(t, h)
}
