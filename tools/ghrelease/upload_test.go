package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The upload verb, pinned by every case the release-checks job drove through a
// fake gh (nova-tools#199): tag 404 with a draft listed, tag 404 with none,
// published 200, a published release in the listing, six malformed listings,
// two drafts, an absent notes file, and a non-answer (403). The fake reproduces
// `gh api -i` for the tag lookup and the JSON array of the paginated listing,
// and records every gh release call instead of making it.

const uploadTag = "v0.14.0"

var uploadNotes = filepath.ToSlash(filepath.Join("docs", "RELEASE-NOTES-0.14.0.md"))

type uploadCase struct {
	status string // the tag lookup's status: 200, 404, 403
	body   string // the tag lookup's body on a 200
	list   string // the paginated listing
	listRC int
}

func uploadWorld(t *testing.T, c uploadCase) *harness {
	t.Helper()
	h := newHarness(t)
	h.vars = map[string]string{"GITHUB_REPOSITORY": "example/repo", "TAG": uploadTag, "GH_TOKEN": "a-token-the-fake-never-reads"}
	h.write(uploadNotes, "# 0.14.0 release notes\n", 0o644)
	h.write("dist/nova-bus_v0.14.0_linux_amd64", "", 0o755)
	h.gh.api = func(args []string) (string, int) {
		path := args[len(args)-1]
		if strings.Contains(path, "releases/tags/") {
			switch c.status {
			case "404":
				return httpAnswer("404", "Not Found", ""), 1
			case "403":
				return httpAnswer("403", "Forbidden", ""), 1
			}
			return httpAnswer("200", "OK", c.body), 0
		}
		return c.list, c.listRC
	}
	return h
}

func upList(t *testing.T, name string) uploadCase {
	return uploadCase{status: "404", list: fixture(t, "testdata/upload/"+name+".json")}
}

func wantRelease(t *testing.T, h *harness, want ...string) {
	t.Helper()
	if len(h.gh.releases) != 1 || strings.Join(h.gh.releases[0], " ") != strings.Join(want, " ") {
		t.Fatalf("gh release calls %v, want one %v\n%s", h.gh.releases, want, h.all())
	}
}

func wantNoRelease(t *testing.T, h *harness) {
	t.Helper()
	if len(h.gh.releases) != 0 {
		t.Fatalf("the verb reached gh release after a refusal: %v\n%s", h.gh.releases, h.all())
	}
}

func TestUploadTag404WithADraftInTheListUploadsWithClobber(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "single-draft-list"))
	h.wantRC(h.do("upload"), 0)
	wantRelease(t, h, "upload", "v0.14.0", "--clobber", "dist/nova-bus_v0.14.0_linux_amd64")
}

func TestUploadTag404WithNoDraftCreatesADraftWithTheCanonicalNotesFile(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "empty-list"))
	h.wantRC(h.do("upload"), 0)
	wantRelease(t, h, "create", "v0.14.0", "--draft", "--verify-tag", "--title", "v0.14.0", "--notes-file", uploadNotes, "dist/nova-bus_v0.14.0_linux_amd64")
	for _, a := range h.gh.releases[0] {
		if a == "--generate-notes" {
			t.Fatal("the release was created with --generate-notes instead of --notes-file")
		}
	}
}

func TestUploadAPublishedReleaseOnTheTagLookupIsRefusedInOneLine(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, uploadCase{status: "200", body: fixture(t, "testdata/upload/published.json")})
	h.wantRun(1, "refusing: v0.14.0 already has a published release 387734569; upload to its draft, never a published release", "upload")
	wantNoRelease(t, h)
}

func TestUploadADraftOnTheTagLookupIsUploadedTo(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, uploadCase{status: "200", body: `{"id": 7, "tag_name": "v0.14.0", "draft": true}`})
	h.wantRC(h.do("upload"), 0)
	wantRelease(t, h, "upload", "v0.14.0", "--clobber", "dist/nova-bus_v0.14.0_linux_amd64")
	if len(h.gh.apis) != 1 {
		t.Fatalf("%d API calls; a 200 answers the question and the listing is not asked", len(h.gh.apis))
	}
}

func TestUploadAPublishedReleaseInTheListingIsRefusedNamingItsId(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "published-list"))
	h.wantRun(1, "v0.14.0 already has a published release 387734569; upload to its draft, never a published release", "upload")
	wantNoRelease(t, h)
}

func TestUploadAListingThatIsNotAListOfReleasesIsRefusedFailClosed(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"not-an-array", "string-draft", "null-draft", "null-id", "string-id", "null-tag"} {
		h := uploadWorld(t, upList(t, name))
		h.wantRC(h.do("upload"), 1)
		if !strings.Contains(h.errb.String(), "is invalid or non-array") {
			t.Errorf("%s: not refused fail-closed:\n%s", name, h.all())
		}
		if len(h.gh.releases) != 0 {
			t.Errorf("%s: reached gh release on an invalid listing: %v", name, h.gh.releases)
		}
	}
}

func TestUploadMoreThanOneDraftIsAmbiguousAndRefused(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "two-drafts-list"))
	h.wantRun(1, "refusing: v0.14.0 has 2 drafts, so there is no one draft to upload to; delete all but one draft for v0.14.0, then re-run this workflow", "upload")
	wantNoRelease(t, h)
}

func TestUploadAnAbsentNotesFileIsRefusedBeforeAnyAPICall(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "empty-list"))
	h.write(uploadNotes, "", 0o644) // present but empty is absent: the check is -s
	h.wantRun(1, "refusing: "+uploadNotes+" does not exist in the tagged tree; write it, land it, and tag the commit that carries it", "upload")
	if len(h.gh.apis) != 0 {
		t.Fatalf("an API call was made when the notes file was absent: %v", h.gh.apis)
	}
	wantNoRelease(t, h)

	h = uploadWorld(t, upList(t, "empty-list"))
	h.vars["TAG"] = "v9.9.9"
	h.wantRun(1, "docs/RELEASE-NOTES-9.9.9.md does not exist", "upload")
}

func TestUploadANonAnswerIsRefusedNotReadAsAbsence(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, uploadCase{status: "403"})
	h.wantRun(1, "asking GitHub about repos/example/repo/releases/tags/v0.14.0 answered neither 200 nor 404 (gh exit 1):", "upload")
	h.mustContain("HTTP/2 403 Forbidden")
	wantNoRelease(t, h)
	if len(h.gh.apis) != 1 {
		t.Fatalf("%d API calls after a 403; the create path must not be walked", len(h.gh.apis))
	}
}

func TestUploadAFailedListingCallIsRefusedNotReadAsNoDrafts(t *testing.T) {
	t.Parallel()
	c := upList(t, "empty-list")
	c.list, c.listRC = "gh: HTTP 502\n", 1
	h := uploadWorld(t, c)
	h.wantRun(1, "asking GitHub about repos/example/repo/releases failed (gh exit 1):", "upload")
	wantNoRelease(t, h)
}

func TestUploadReadsEveryPageOfAPaginatedListing(t *testing.T) {
	t.Parallel()
	// gh api --paginate prints one JSON array per page, back to back.
	page1 := `[{"id": 1, "tag_name": "v0.13.0", "draft": false}]`
	page2 := `[{"id": 2, "tag_name": "v0.14.0", "draft": true}]`
	h := uploadWorld(t, uploadCase{status: "404", list: page1 + "\n" + page2 + "\n"})
	h.wantRC(h.do("upload"), 0)
	wantRelease(t, h, "upload", "v0.14.0", "--clobber", "dist/nova-bus_v0.14.0_linux_amd64")

	// A published release on the second page is found too.
	page2 = `[{"id": 3, "tag_name": "v0.14.0", "draft": false}]`
	h = uploadWorld(t, uploadCase{status: "404", list: page1 + page2})
	h.wantRun(1, "already has a published release 3;", "upload")
}

func TestUploadNothingAtAllFromTheListingIsRefusedNotReadAsNoDrafts(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, uploadCase{status: "404", list: ""})
	h.wantRun(1, "releases listing is empty", "upload")
	wantNoRelease(t, h)
}

func TestUploadAttachesEveryFileOfDistInOrderAndNoDotfile(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "single-draft-list"))
	h.write("dist/SHA256SUMS", "x", 0o644)
	h.write("dist/.hidden", "x", 0o644)
	h.wantRC(h.do("upload"), 0)
	wantRelease(t, h, "upload", "v0.14.0", "--clobber", "dist/SHA256SUMS", "dist/nova-bus_v0.14.0_linux_amd64")
}

func TestUploadAnEmptyDistIsRefusedNotPassedToGhAsAGlob(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "single-draft-list"))
	h.remove("dist/nova-bus_v0.14.0_linux_amd64")
	h.wantRun(1, "dist/ holds no artifact to attach", "upload")
	wantNoRelease(t, h)
}

func TestUploadPassesGhReleaseFailureOn(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, upList(t, "single-draft-list"))
	h.gh.releaseRC = 4
	h.wantRC(h.do("upload"), 4)
}

func TestUploadAnUnreadableTagAnswerIsRefused(t *testing.T) {
	t.Parallel()
	h := uploadWorld(t, uploadCase{status: "200", body: "not json"})
	h.wantRun(1, "is not a release object", "upload")
	wantNoRelease(t, h)
}

func TestUploadNeedsTheWholeEnvironmentAndTakesNoArguments(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"GITHUB_REPOSITORY", "TAG", "GH_TOKEN"} {
		h := uploadWorld(t, upList(t, "empty-list"))
		delete(h.vars, missing)
		h.wantRun(2, fmt.Sprintf("%s is not set", missing), "upload")
	}
	h := uploadWorld(t, upList(t, "empty-list"))
	h.wantRun(2, "usage:", "upload", "x")
}

func TestSummarizeReleasesCountsOnlyTheTagAskedFor(t *testing.T) {
	t.Parallel()
	listing := `[
	  {"id": 1, "tag_name": "v0.13.0", "draft": false},
	  {"id": 2, "tag_name": "v0.14.0-rc1", "draft": false},
	  {"id": 3, "tag_name": "v0.14.0", "draft": true},
	  {"id": 4, "tag_name": "v0.15.0", "draft": true}]`
	pub, drafts, err := summarizeReleases(listing, "v0.14.0")
	if err != nil || pub != "" || drafts != 1 {
		t.Fatalf("published %q drafts %d err %v, want none published and one draft", pub, drafts, err)
	}
	pub, drafts, err = summarizeReleases(`[{"id": 9, "tag_name": "v0.14.0", "draft": false}, {"id": 10, "tag_name": "v0.14.0", "draft": false}]`, "v0.14.0")
	if err != nil || pub != "9" || drafts != 0 {
		t.Fatalf("published %q drafts %d err %v, want the first published id", pub, drafts, err)
	}
}

func TestSummarizeReleasesRefusesWhatIsNotAListOfReleases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, phrase string }{
		{"empty", "", "empty"},
		{"object", `{"message": "Not Found"}`, "not an array"},
		{"mixed", `[] {"a": 1}`, "not an array"},
		{"not json", "gh: HTTP 500", "invalid"},
		{"a string item", `["x"]`, "missing required metadata"},
		{"zero id", `[{"id": 0, "tag_name": "t", "draft": true}]`, "missing required metadata"},
		{"negative id", `[{"id": -5, "tag_name": "t", "draft": true}]`, "missing required metadata"},
		{"empty tag", `[{"id": 5, "tag_name": "", "draft": true}]`, "missing required metadata"},
		{"numeric draft", `[{"id": 5, "tag_name": "t", "draft": 1}]`, "missing required metadata"},
		{"missing draft", `[{"id": 5, "tag_name": "t"}]`, "missing required metadata"},
	} {
		_, _, err := summarizeReleases(c.in, "t")
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.phrase) {
			t.Errorf("%s: error %q lacks %q", c.name, err, c.phrase)
		}
	}
}
